"""Linux CLI/runtime shared by adapters; Python standard library only."""
import argparse
import collections
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import queue
import signal
import socket
import struct
import subprocess
import sys
import threading
import time
import uuid

from events import validate_event

PROTOCOL = 'harnessctl/3'
IDLE_SECONDS = 60
MAX_TEXT = 256 * 1024
MAX_COMMAND = 2 * 1024 * 1024


class Rejected(Exception):
    def __init__(self, message, code=1):
        super().__init__(message)
        self.code = code


def identity():
    return str(uuid.uuid4())


def timestamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def write_json(path, value):
    temp = path.with_name(path.name + '.tmp')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(value, out, ensure_ascii=False)
        out.write('\n')
        out.flush()
        os.fsync(out.fileno())
    os.replace(temp, path)
    sync_directory(path.parent)


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def address(events):
    # Abstract Unix socket avoids pathname limits for deeply nested workspaces.
    # The worker checks SO_PEERCRED before reading commands.
    digest = hashlib.sha256(os.fsencode(str(events))).hexdigest()
    return '\0harnessctl-' + str(os.getuid()) + '-' + digest


def append_event(events, event_type, **fields):
    event = dict(type=event_type, ts=timestamp(), **fields)
    validate_event(event)
    line = json.dumps(event, ensure_ascii=False, allow_nan=False) + '\n'
    with open(str(events) + '.d/events.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        with open(events, 'a+b') as out:
            out.seek(0, os.SEEK_END)
            offset = out.tell()
            if offset:
                out.seek(-1, os.SEEK_END)
                if out.read(1) != b'\n':
                    raise OSError('incomplete event tail; refusing to append until recovered')
            try:
                out.write(line.encode('utf8'))
                out.flush()
                if event_type != 'log':
                    os.fsync(out.fileno())
            except OSError:
                out.truncate(offset)
                raise


def append_log(events, topic, message='', level='debug', data=None, run_id=None):
    try:
        append_event(events, 'log', topic=topic, message=message, level=level, data=data, run_id=run_id)
    except (OSError, ValueError, TypeError):
        pass


def replay_critical(events):
    pending = {}
    requests = {}
    if events.exists():
        # Only called after taking the control and dead-runtime locks. An
        # unfinished final line was never a committed/acknowledged event.
        with open(str(events)+'.d/events.lock', 'a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            raw = events.read_bytes()
            if raw and not raw.endswith(b'\n'):
                raw = raw[:raw.rfind(b'\n')+1]
                with open(events, 'r+b') as output:
                    output.truncate(len(raw))
                    output.flush()
                    os.fsync(output.fileno())
        for line in raw.decode('utf8').splitlines():
            event = json.loads(line)
            if event['type'] == 'accepted':
                pending[event['input_id']] = event
            elif event['type'] == 'result':
                for key in event['input_ids']:
                    pending.pop(key, None)
            elif event['type'] == 'request':
                if event['state'] == 'pending':
                    requests[event['request_id']] = event
                else:
                    requests.pop(event['request_id'], None)
    return pending, requests


def rpc(events, command):
    try:
        with socket.socket(socket.AF_UNIX) as sock:
            sock.settimeout(300 if command.get('verb') == 'send' else 5)
            sock.connect(address(events))
            sock.sendall(json.dumps(command).encode() + b'\n')
            reply = sock.makefile('rb').readline(MAX_COMMAND + 1)
            result = json.loads(reply)
    except (OSError, ValueError) as exc:
        raise Rejected('session runtime unavailable; use status to inspect', 4) from exc
    if not result.get('ok'):
        raise Rejected(result.get('error', 'command failed'), result.get('code', 1))
    return result.get('value')


class Runtime:
    def __init__(self, events, backend, config):
        self.events = events
        self.directory = Path(str(events) + '.d')
        self.backend = backend
        self.config = config
        self.proc = None
        self.thread = None
        self.turn = None
        self.starting = False
        self.stopping = False
        self.inputs = {}  # input id -> exact request and terminal delivery receipt
        self.queued = collections.deque()
        self.requests = {}
        self.pending = {}
        self.counter = 0
        self.frames = queue.Queue()
        self.exit_reason = 'backend_exited'
        self.last_status = None
        self.cancelling = False
        self.failed = False
        self.last_activity = time.monotonic()
        self.shutdown_lock = None
        self.work_inputs = []
        self.active_run_id = None
        self.waiters = {}
        self.messages = {}
        self.message_order = []
        self.work_usage = None
        self.work_cost = None
        self.last_error = None
        self.finishing = None

    def emit(self, event_type, **fields):
        fields.setdefault('run_id', self.active_run_id)
        append_event(self.events, event_type, **fields)

    def log(self, topic, message='', level='debug', **data):
        if level == 'error':
            self.last_error = str(message)
        append_log(self.events, topic, str(message), level, data or None, self.active_run_id)

    def chunk(self, text, key=None):
        if not isinstance(text, str):
            raise ValueError('native text delta must be a string')
        slot = self.message_slot(key or '_stream')
        slot['buffer'] += text
        slot['dirty'] = True
        self.log('text.delta', data_text=text, message_id=slot['id'])

    def message_slot(self, key):
        if key not in self.messages:
            self.messages[key] = dict(id=identity(), text=None, buffer='', dirty=False)
            self.message_order.append(key)
        return self.messages[key]

    def message(self, text, key=None):
        if not isinstance(text, str):
            raise ValueError('native complete message must be a string')
        key = key or ('_stream' if '_stream' in self.messages and self.messages['_stream']['dirty'] else identity())
        slot = self.message_slot(key)
        if slot['text'] != text:
            self.emit('message', message_id=slot['id'], text=text)
        slot.update(text=text, buffer=text, dirty=False)

    def flush_messages(self, output=None):
        for key in list(self.message_order):
            slot = self.messages[key]
            if slot['dirty']:
                self.message(slot['buffer'], key)
        if output is not None:
            key = self.message_order[-1] if self.message_order else None
            self.message(output, key)

    def usage(self, usage, cost=None):
        self.work_usage = usage
        if cost is not None:
            self.work_cost = cost
        self.log('usage', usage=usage, cost_usd=cost)

    def result(self, items, status, output='', usage=None, cost=None, error=None):
        unresolved = [item for item in items if not item.get('resolved')]
        if not unresolved:
            return
        self.emit('result', run_id=unresolved[0]['run_id'], input_ids=[item['id'] for item in unresolved], status=status,
                  output=output, usage=usage, cost_usd=cost, error=error)
        for item in unresolved:
            item['resolved'] = True

    def expire_requests(self, reason):
        for request in self.requests.values():
            self.emit('request', **dict(request['public'], state='expired', reason=reason))
        self.requests.clear()

    def snapshot(self):
        state = ('cancelling' if self.cancelling else 'awaiting_reply' if self.requests
                 else 'running' if self.turn or self.starting or self.queued
                 else 'failed' if self.failed else 'idle')
        return dict(state=state, queued=len(self.queued), requests=[v['public'] for v in self.requests.values()],
                    native_session=self.thread if self.thread != 'pending' else None,
                    adapter=self.backend.NAME, pid=os.getpid(), live=True, run_id=self.active_run_id,
                    unresolved_inputs=[item['id'] for item in self.inputs.values() if not item.get('resolved')])

    def public_status(self):
        return public_status(self.snapshot(), self.config)

    def save(self):
        value = self.snapshot()
        if value != self.last_status:
            write_json(self.directory / 'status.json', value)
            self.last_status = value

    def native_ready(self, native):
        if not isinstance(self.thread, str) or not self.thread or self.thread == 'pending':
            raise RuntimeError('backend did not provide a resumable context')
        write_json(self.directory / 'native.json', dict(resume=self.thread))
        self.log('session.ready', native=native)

    def open_request(self, native, kind, message, questions=None):
        if self.cancelling:
            self.backend.decline(self, native)
            return
        key = identity()
        if kind == 'question' and not questions:
            raise RuntimeError('native question has no supported question schema')
        if not isinstance(message, str):
            message = json.dumps(message, ensure_ascii=False)
        public = dict(run_id=self.active_run_id, request_id=key, state='pending', kind=kind, message=message)
        if questions is not None:
            public['questions'] = questions
        self.emit('request', **public)
        self.requests[key] = dict(native=native, kind=kind, public=public)

    def write(self, frame):
        self.proc.stdin.write(json.dumps(frame, ensure_ascii=False) + '\n')
        self.proc.stdin.flush()

    def request(self, method, params, context=None):
        self.counter += 1
        key = 'ctl:' + str(self.counter)
        self.pending[key] = (method, context, time.monotonic())
        self.write(dict(id=key, method=method, params=params))
        return key

    def delivered(self, item, outcome, native=None, **fields):
        if item.get('delivered'):
            return
        if not item.get('accepted'):
            self.confirm_input(item, self.active_run_id)
            self.work_inputs.append(item)
        item['delivered'] = True
        self.log('input.delivered', input_id=item['id'], outcome=outcome, native=native, **fields)

    def dropped(self, item, reason):
        if item.get('resolved'):
            return
        if not item.get('accepted'):
            self.respond_waiter(item, reason or 'Input was not accepted')
            item['resolved'] = True
            return
        item['delivered'] = True
        if item in self.work_inputs:
            self.work_inputs.remove(item)
        status = 'cancelled' if reason in ('cancelled', 'terminated') else 'failed'
        self.result([item], status, error=None if status == 'cancelled' else reason or 'Input failed')
        self.log('input.dropped', input_id=item['id'], reason=reason)

    def requeue(self, item):
        if item in self.work_inputs:
            self.work_inputs.remove(item)
        self.confirm_input(item, identity())
        self.queued.append(item)
        self.drain()

    def respond_waiter(self, item, error=None):
        conn = self.waiters.pop(item['id'], None)
        if conn:
            try:
                payload = dict(ok=False, code=1, error=error) if error else dict(ok=True, value=None)
                conn.sendall(json.dumps(payload).encode()+b'\n')
            except (OSError, ValueError):
                pass
            finally:
                conn.close()

    def confirm_input(self, item, run_id):
        item['run_id'] = run_id
        self.emit('accepted', run_id=run_id, input_id=item['id'], text=item['text'])
        item['accepted'] = True
        self.respond_waiter(item)

    def accept(self, command):
        item = dict(id=identity(), text=command['prompt'])
        self.config['history_started'] = True
        persisted = {k: v for k, v in self.config.items() if k != 'resume'}
        write_json(self.directory / 'config.json', persisted)
        self.failed = False
        self.last_activity = time.monotonic()
        self.inputs[item['id']] = item
        if self.turn and not self.finishing and not self.cancelling and not self.requests and self.backend.STEER_WHILE_BUSY:
            # A native receipt decides the run. Do not publish a provisional
            # assignment which a completion/steer race could make incorrect.
            self.backend.steer(self, item)
            return item
        self.confirm_input(item, identity())
        self.queued.append(item)
        self.drain()

    def drain(self):
        if self.stopping or self.finishing or not self.thread or self.turn or self.starting or not self.queued:
            return
        item = self.queued.popleft()
        self.starting = True
        self.work_inputs = [item]
        self.active_run_id = item['run_id']
        self.messages = {}
        self.message_order = []
        self.work_usage = None
        self.work_cost = None
        self.last_error = None
        self.backend.start_turn(self, item)

    def finished(self, status, native, **fields):
        if self.turn is None and not self.starting:
            return
        self.finishing = (status, native, fields)
        self.complete_finished()

    def complete_finished(self):
        if not self.finishing:
            return
        if not self.stopping and any(method == 'turn/steer' for method, _, _ in self.pending.values()):
            return  # Wait for the in-flight input's receipt before resolving it.
        status, native, fields = self.finishing
        self.backend.before_finished(self, native)
        self.flush_messages(fields.get('output'))
        self.expire_requests('turn_finished')
        status = 'cancelled' if status == 'interrupted' else status
        output = '\n\n'.join(self.messages[key]['text'] or '' for key in self.message_order)
        error = (fields.get('error') or self.last_error or 'Execution failed') if status == 'failed' else None
        self.result(self.work_inputs, status, output, self.work_usage, self.work_cost, error)
        self.finishing = None
        self.work_inputs = []
        self.active_run_id = None
        self.turn = None
        self.starting = False
        self.cancelling = False
        self.failed = status == 'failed'
        self.last_activity = time.monotonic()
        self.drain()

    def command(self, cmd):
        verb = cmd['verb']
        if verb == 'status':
            return self.public_status()
        if verb == '_recycle':
            if self.turn or self.starting or self.queued or self.requests or self.pending:
                raise Rejected('cannot change environment while work is active', 2)
            self.stopping = True
            self.exit_reason = 'reconfigured'
            return
        if verb == 'send':
            return self.accept(cmd)
        elif verb == 'cancel':
            while self.queued:
                self.dropped(self.queued.popleft(), 'cancelled')
            for item in self.inputs.values():
                if not item.get('accepted') and not item.get('resolved'):
                    item['cancelled'] = True
                    self.dropped(item, 'cancelled')
            for item in self.work_inputs:
                item['cancelled'] = True
            self.expire_requests('cancelled')
            self.cancelling = bool(self.turn or self.starting)
            self.backend.cancel(self, drop_queued=True)
            self.log('cancel.accepted')
            self.last_activity = time.monotonic()
        elif verb == 'reply':
            key = cmd['request_id']
            if key not in self.requests:
                raise Rejected('unknown, replied or expired request', 4)
            self.backend.reply(self, self.requests[key], cmd['answer'])
            request = self.requests[key]
            answer = cmd['answer'].strip() if request['kind'] == 'permission' else json.loads(cmd['answer'])
            self.emit('request', **dict(request['public'], state='replied', answer=answer))
            del self.requests[key]
            self.last_activity = time.monotonic()
        else:
            raise Rejected('unknown command', 2)
        self.save()

    def serve_connection(self, listener):
        conn, _ = listener.accept()
        deferred = False
        try:
            conn.settimeout(1)
            _, uid, _ = struct.unpack('3i', conn.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid != os.getuid():
                return
            try:
                raw = conn.makefile('rb').readline(MAX_COMMAND + 1)
                if len(raw) > MAX_COMMAND:
                    raise Rejected('command too large', 2)
                cmd = json.loads(raw)
                value = self.command(cmd)
                if cmd['verb'] == 'send' and value is not None:
                    self.waiters[value['id']] = conn
                    deferred = True
                    return
                result = dict(ok=True, value=value)
            except Rejected as exc:
                result = dict(ok=False, error=str(exc), code=exc.code)
            except (ValueError, KeyError, TypeError) as exc:
                result = dict(ok=False, error='invalid command: ' + str(exc), code=2)
            try:
                conn.sendall(json.dumps(result).encode() + b'\n')
            except (BrokenPipeError, ConnectionResetError):
                pass
        finally:
            if not deferred:
                conn.close()

    def read_frames(self):
        try:
            for line in self.proc.stdout:
                try:
                    self.frames.put(json.loads(line))
                except ValueError:
                    self.frames.put(dict(_invalid=line[:4096]))
        finally:
            self.frames.put(None)

    def run(self):
        with open(self.directory / 'runtime.lock', 'a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with socket.socket(socket.AF_UNIX) as listener:
                listener.bind(address(self.events))
                listener.listen(16)
                listener.settimeout(.05)
                def stop(*_):
                    self.exit_reason = 'terminated'
                    self.stopping = True
                signal.signal(signal.SIGTERM, stop)
                signal.signal(signal.SIGINT, stop)
                try:
                    with open(self.directory / 'backend.stderr.log', 'a') as stderr:
                        argv = self.backend.argv(self.config)
                        self.proc = subprocess.Popen(argv, cwd=self.config['cwd'], stdin=subprocess.PIPE,
                                                     stdout=subprocess.PIPE, stderr=stderr, text=True,
                                                     encoding='utf8', errors='replace', start_new_session=True)
                        write_json(self.directory / 'process.json', dict(pid=os.getpid(), backend_pid=self.proc.pid))
                        threading.Thread(target=self.read_frames, daemon=True).start()
                        self.backend.initialize(self)
                        self.save()
                        while not self.stopping:
                            # Bound the batch so chatty output cannot starve control commands.
                            for _ in range(128):
                                try:
                                    frame = self.frames.get_nowait()
                                except queue.Empty:
                                    break
                                if frame is None:
                                    self.stopping = True
                                    break
                                if '_invalid' in frame:
                                    self.log('native.error', message='invalid backend JSON', level='error', native=frame)
                                else:
                                    self.backend.frame(self, frame)
                                    self.complete_finished()
                            if self.stopping:
                                break
                            self.save()
                            try:
                                self.serve_connection(listener)
                            except socket.timeout:
                                pass
                            if (not self.turn and not self.starting and not self.queued and not self.requests
                                    and not self.pending and time.monotonic()-self.last_activity > IDLE_SECONDS):
                                shutdown = open(self.directory / 'control.lock', 'a')
                                try:
                                    fcntl.flock(shutdown, fcntl.LOCK_EX | fcntl.LOCK_NB)
                                except BlockingIOError:
                                    shutdown.close()
                                else:
                                    self.shutdown_lock = shutdown
                                    self.exit_reason = 'idle'
                                    self.stopping = True
                            if any(time.monotonic()-p[2] > 60 for p in self.pending.values()):
                                raise RuntimeError('native control response timed out')
                except Exception as exc:
                    self.exit_reason = 'error'
                    self.log('runtime.error', message=str(exc) or type(exc).__name__, level='error')
                    for slot in self.messages.values():
                        if not isinstance(slot['buffer'], str):
                            slot.update(buffer='', dirty=False)
                    self.finishing = None
                    self.work_usage = None
                    self.work_cost = None
                finally:
                    self.stopping = True
                    try:
                        if self.turn or self.starting:
                            self.finished('interrupted' if self.exit_reason == 'terminated' else 'failed', None)
                        for item in self.inputs.values():
                            self.dropped(item, self.exit_reason)
                    except (OSError, ValueError, TypeError) as error:
                        self.failed = True
                        self.log('storage.error', message=str(error), level='error')
                        for item in self.inputs.values():
                            self.respond_waiter(item, 'Critical event could not be saved')
                    if self.proc:
                        # Backend and its tools share a process group owned by this runtime.
                        try:
                            os.killpg(self.proc.pid, signal.SIGTERM)
                            self.proc.wait(timeout=2)
                        except subprocess.TimeoutExpired:
                            pass
                        except ProcessLookupError:
                            pass
                        try:
                            os.killpg(self.proc.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        self.proc.wait()
                    failed = self.exit_reason not in ('idle', 'terminated', 'reconfigured') or self.failed
                    self.log('runtime.stopped', reason=self.exit_reason)
                    write_json(self.directory / 'status.json', dict(self.snapshot(), state='failed' if failed else 'idle', live=False))
        if self.shutdown_lock:
            self.shutdown_lock.close()


def question_specs(questions, native_ids=False):
    return [dict(id=q['id'] if native_ids else 'q'+str(i+1),
                 text=q.get('question', ''), header=q.get('header', ''),
                 multiple=bool(q.get('multiSelect', False)),
                 options=[dict(label=o.get('label', ''), description=o.get('description', ''))
                          for o in q.get('options', [])]) for i, q in enumerate(questions)]


def question_answers(request, answer):
    try:
        value = json.loads(answer)
    except ValueError as exc:
        raise Rejected('question answer must be a JSON object keyed by question id', 2) from exc
    questions = request['public'].get('questions', [])
    if not questions or not isinstance(value, dict) or set(value) != {q['id'] for q in questions}:
        raise Rejected('answer must contain exactly the question ids from request.opened', 2)
    result = {}
    for q in questions:
        selected = value[q['id']]
        if isinstance(selected, str):
            selected = [selected]
        if not isinstance(selected, list) or not selected or not all(isinstance(x,str) and x.strip() for x in selected):
            raise Rejected('each answer must be nonempty text or a list of nonempty text', 2)
        if not q['multiple'] and len(selected) != 1:
            raise Rejected('question only accepts one answer', 2)
        result[q['id']] = selected
    return result


def public_status(state, config):
    return dict(state=state['state'], run_id=state.get('run_id'), queued=state.get('queued', 0),
                requests=state.get('requests', []), cwd=config['cwd'], model=config.get('model'))


def parser():
    p = argparse.ArgumentParser(description='harnessctl/3: init, send, cancel, status, reply')
    sub = p.add_subparsers(dest='verb', required=True)
    for verb in ('init', 'send', 'cancel', 'status', 'reply'):
        sp = sub.add_parser(verb)
        sp.add_argument('events', type=Path)
        if verb == 'init':
            sp.add_argument('--cwd')
            sp.add_argument('--model')
            sp.add_argument('--agents-md')
            sp.add_argument('--skills-dir', action='append')
        if verb == 'send':
            sp.add_argument('prompt', nargs='?', help='omitted: read UTF-8 stdin to EOF')
        if verb == 'reply':
            sp.add_argument('request_id')
            sp.add_argument('answer', nargs='?', help='omitted: read UTF-8 stdin to EOF')
    return p


def body(value):
    if value is None:
        if sys.stdin.isatty():
            raise Rejected('provide text as argument or pipe UTF-8 stdin', 2)
        data = sys.stdin.buffer.read(MAX_TEXT + 1)
        if len(data) > MAX_TEXT:
            raise Rejected('input exceeds 256 KiB', 2)
        try:
            value = data.decode('utf8')
        except UnicodeDecodeError as exc:
            raise Rejected('stdin must contain valid UTF-8', 2) from exc
    if not value.strip() or len(value.encode('utf8')) > MAX_TEXT:
        raise Rejected('input must be nonempty and at most 256 KiB', 2)
    return value


def inspect_state(events, config):
    directory = Path(str(events) + '.d')
    state_path = directory / 'status.json'
    try:
        return rpc(events, dict(verb='status')), True
    except Rejected:
        with open(directory / 'runtime.lock', 'a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise Rejected('runtime alive but unresponsive; command was not retried')
            try:
                state = json.loads(state_path.read_text()) if state_path.exists() else dict(state='idle')
            except ValueError:
                state = dict(state='idle')
            pending, requests = replay_critical(events)
            if state.pop('live', False) or pending or requests:
                # The runtime never completed its own cleanup. Do not replay
                # uncertain inputs; the next send resumes only native history.
                for request in requests.values():
                    fields = {key:value for key,value in request.items() if key not in ('type','ts')}
                    append_event(events, 'request', **dict(fields, state='expired', reason='runtime_lost'))
                if pending:
                    runs = {}
                    for item in pending.values():
                        runs.setdefault(item['run_id'], []).append(item['input_id'])
                    for run_id, input_ids in runs.items():
                        append_event(events, 'result', run_id=run_id, input_ids=input_ids, status='failed', output='',
                                     usage=None, cost_usd=None, error='Runtime lost; input outcome is uncertain')
                recovered = 'failed' if pending or requests or state.get('state')=='failed' else 'idle'
                state.update(state=recovered, queued=0, requests=[], unresolved_inputs=[], run_id=None)
                append_log(events, 'runtime.stopped', data=dict(reason='lost'))
                write_json(state_path, state)
            return public_status(state, config), False


def backend_alive(directory):
    path = directory / 'process.json'
    if not path.exists():
        return False
    group = json.loads(path.read_text())['backend_pid']
    # A runtime killed with SIGKILL may leave a native process or tools behind.
    # Never resume the same native history concurrently with that process group.
    for stat in Path('/proc').glob('[0-9]*/stat'):
        try:
            parts = stat.read_text().rsplit(')', 1)[1].split()
            if parts[0] != 'Z' and int(parts[2]) == group:
                return True
        except (OSError, ValueError, IndexError):
            continue
    return False


def environment_hash():
    ignored = {'_', 'SHLVL', 'PWD', 'OLDPWD'}
    data = {key:value for key,value in os.environ.items() if key not in ignored}
    return hashlib.sha256(json.dumps(data, sort_keys=True).encode()).hexdigest()


def recycle(events):
    rpc(events, dict(verb='_recycle'))
    directory = Path(str(events)+'.d')
    deadline = time.monotonic()+5
    while time.monotonic()<deadline:
        with open(directory/'runtime.lock','a') as lock:
            try:
                fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                return
            except BlockingIOError:
                pass
        time.sleep(.025)
    raise Rejected('runtime environment refresh timed out')


def launch(events, backend, config):
    import resources
    resources.validate(config)
    directory = Path(str(events) + '.d')
    native = directory / 'native.json'
    if backend_alive(directory):
        raise Rejected('previous native process is still running; refusing concurrent context recovery')
    if native.exists() and not isinstance(json.loads(native.read_text()).get('resume'), str):
        raise Rejected('invalid private context recovery data')
    if native.exists() and not json.loads(native.read_text()).get('resume'):
        raise Rejected('empty private context recovery data')
    if config.get('history_started') and not native.exists():
        raise Rejected('native context is unavailable; refusing to silently start a new conversation')
    with open(directory / 'runtime.stderr.log', 'a') as log:
        process = subprocess.Popen([sys.executable, str(Path(sys.argv[0]).resolve()), '_worker', str(events)],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                   stderr=log, start_new_session=True)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try:
            rpc(events, dict(verb='status'))
            return
        except Rejected:
            if process.poll() is not None:
                raise Rejected('runtime initialization failed; inspect events and private diagnostics')
            time.sleep(.025)
    raise Rejected('runtime initialization timed out; inspect status before retrying')


def initialize_session(events, backend, args):
    import resources
    import shutil
    directory = Path(str(events)+'.d')
    marker = directory/'initializing.json'
    if marker.exists():
        previous = json.loads(marker.read_text())
        stage = previous.get('stage', '')
        if not stage.startswith('.init-') or Path(stage).name != stage:
            raise Rejected('invalid initialization recovery marker')
        if events.exists() and (not events.is_file() or events.stat().st_size):
            raise Rejected('initialization has unexpected history; refusing to overwrite it')
        events.unlink(missing_ok=True)
        shutil.rmtree(directory/stage, ignore_errors=True)
        shutil.rmtree(directory/'resources', ignore_errors=True)
        marker.unlink()
    if events.exists() or (directory/'resources').exists():
        raise Rejected('existing event file/resources have no adapter state; refusing to overwrite them', 2)
    if args.model is not None and not args.model.strip():
        raise Rejected('model must be nonempty', 2)
    try:
        cwd = Path(args.cwd or os.getcwd()).expanduser().resolve(strict=True)
    except OSError as exc:
        raise Rejected(str(exc), 2) from exc
    if not cwd.is_dir():
        raise Rejected('cwd must be a directory', 2)
    stage = directory/('.init-'+identity())
    write_json(marker, dict(stage=stage.name))
    created = False
    try:
        snapshot = resources.prepare(args, stage, cwd, events)
        fd = os.open(events, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        os.fsync(fd)
        os.close(fd)
        created = True
        sync_directory(events.parent)
        stage.rename(directory/'resources')
        config = dict(protocol=PROTOCOL, adapter=backend.NAME, cwd=str(cwd), model=args.model,
                      history_started=False, environment_hash=environment_hash(),
                      resources_dir=str(directory/'resources'), resources=snapshot)
        # This is the commit point. Other commands hold the same control lock.
        write_json(directory/'config.json', config)
        marker.unlink()
        sync_directory(directory)
    except (OSError, ValueError) as exc:
        if not (directory/'config.json').exists():
            if created:
                events.unlink(missing_ok=True)
                shutil.rmtree(directory/'resources', ignore_errors=True)
            shutil.rmtree(stage, ignore_errors=True)
            marker.unlink(missing_ok=True)
        raise Rejected(str(exc), 2) from exc


def main(backend):
    os.umask(0o077)
    try:
        # Private entrypoint is not part of the command parser or public API.
        if len(sys.argv) == 3 and sys.argv[1] == '_worker':
            events = Path(sys.argv[2])
            directory = Path(str(events) + '.d')
            config = json.loads((directory / 'config.json').read_text())
            native = directory / 'native.json'
            config['resume'] = json.loads(native.read_text())['resume'] if native.exists() else None
            import resources
            resources.validate(config)
            Runtime(events, backend, config).run()
            return 0
        args = parser().parse_args()
        content = body(args.prompt) if args.verb == 'send' else body(args.answer) if args.verb == 'reply' else None
        events = args.events.expanduser().resolve()
        directory = Path(str(events) + '.d')
        if not directory.exists() and args.verb != 'init':
            raise Rejected('conversation does not exist; use init first', 4)
        directory.mkdir(mode=0o700, exist_ok=True)
        # Serializes init, recovery, and foreground commands for one event address.
        with open(directory / 'control.lock', 'a') as control:
            fcntl.flock(control, fcntl.LOCK_EX)
            path = directory / 'config.json'
            if args.verb == 'init' and not path.exists():
                initialize_session(events, backend, args)
                return 0
            if not path.exists():
                raise Rejected('conversation does not exist; use init first', 4)
            config = json.loads(path.read_text())
            if not events.is_file():
                raise Rejected('event file is missing; refusing to silently recreate conversation history')
            if config.get('protocol') != PROTOCOL:
                raise Rejected('event address uses an incompatible protocol; use a new event file', 2)
            if config.get('adapter') != backend.NAME:
                raise Rejected('event file belongs to another adapter', 2)
            import resources
            if args.verb in ('init', 'send'):
                resources.validate(config)
            if args.verb == 'init':
                try:
                    resources.compare(config, args, events)
                except (OSError, ValueError) as exc:
                    raise Rejected(str(exc), 2) from exc
                return 0
            state, live = inspect_state(events, config)
            if args.verb == 'status':
                print(json.dumps(state, ensure_ascii=False))
                return 0
            if args.verb == 'send':
                current_environment = environment_hash()
                if live and config.get('environment_hash') != current_environment:
                    recycle(events)
                    live = False
                if not live:
                    config['environment_hash'] = current_environment
                    write_json(path, config)
                if not live:
                    launch(events, backend, config)
                # Acceptance may wait for a native consumption boundary. Release
                # the process-control lock before waiting, so status/reply/cancel
                # and other sends remain available.
            elif args.verb == 'cancel':
                if live:
                    rpc(events, dict(verb='cancel'))
                else:
                    if backend_alive(directory):
                        raise Rejected('runtime lost but native process still running; cancellation cannot be confirmed')
                    append_log(events, 'cancel.accepted')
            else:
                if not live:
                    raise Rejected('request has expired', 4)
                rpc(events, dict(verb='reply', request_id=args.request_id, answer=content))
        if args.verb == 'send':
            rpc(events, dict(verb='send', prompt=content))
        return 0
    except Rejected as exc:
        print(str(exc), file=sys.stderr)
        return exc.code
    except (OSError, ValueError, KeyError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
