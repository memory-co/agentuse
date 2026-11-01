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

PROTOCOL = 'harnessctl/1'
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
    os.replace(temp, path)


def address(events):
    # Abstract Unix socket avoids pathname limits for deeply nested workspaces.
    # The worker checks SO_PEERCRED before reading commands.
    digest = hashlib.sha256(os.fsencode(str(events))).hexdigest()
    return '\0harnessctl-' + str(os.getuid()) + '-' + digest


def append_event(events, event_type, **fields):
    with open(str(events) + '.d/events.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        with open(events, 'a', encoding='utf8') as out:
            out.write(json.dumps(dict(type=event_type, ts=timestamp(), **fields), ensure_ascii=False) + '\n')
            out.flush()
            os.fsync(out.fileno())


def rpc(events, command):
    try:
        with socket.socket(socket.AF_UNIX) as sock:
            sock.settimeout(5)
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

    def emit(self, event_type, **fields):
        fields.pop('turn', None)  # Native turn identity is adapter-private.
        append_event(self.events, event_type, **fields)

    def snapshot(self):
        state = ('cancelling' if self.cancelling else 'awaiting_reply' if self.requests
                 else 'running' if self.turn or self.starting or self.queued
                 else 'failed' if self.failed else 'idle')
        return dict(state=state, queued=len(self.queued), requests=[v['public'] for v in self.requests.values()],
                    native_session=self.thread if self.thread != 'pending' else None,
                    adapter=self.backend.NAME, pid=os.getpid(), live=True)

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
        self.emit('session.ready', native=native)

    def open_request(self, native, kind, message, questions=None):
        if self.cancelling:
            self.backend.decline(self, native)
            return
        key = identity()
        if kind == 'question' and not questions:
            raise RuntimeError('native question has no supported question schema')
        if not isinstance(message, str):
            message = json.dumps(message, ensure_ascii=False)
        public = dict(request_id=key, kind=kind, message=message)
        if questions is not None:
            public['questions'] = questions
        self.requests[key] = dict(native=native, kind=kind, public=public)
        self.emit('request.opened', native=native, **public)

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
        item['delivered'] = True
        self.emit('input.delivered', input=item['id'], outcome=outcome, native=native, **fields)

    def dropped(self, item, reason):
        if item.get('delivered'):
            return
        item['delivered'] = True
        self.emit('input.dropped', input=item['id'], reason=reason)

    def accept(self, command):
        item = dict(id=identity(), text=command['prompt'])
        # This flag prevents silent context resets after a runtime dies before
        # the native backend has supplied its persistent resume identity.
        self.config['history_started'] = True
        persisted = {k: v for k, v in self.config.items() if k != 'resume'}
        write_json(self.directory / 'config.json', persisted)
        self.inputs[item['id']] = item
        self.failed = False
        self.last_activity = time.monotonic()
        self.emit('input.accepted', input=item['id'])
        if self.turn and not self.cancelling and not self.requests:
            self.backend.steer(self, item)
        else:
            self.queued.append(item)
            self.drain()

    def drain(self):
        if self.stopping or not self.thread or self.turn or self.starting or not self.queued:
            return
        item = self.queued.popleft()
        self.starting = True
        self.backend.start_turn(self, item)

    def finished(self, status, native, **fields):
        turn = self.turn
        if turn is None and not self.starting:
            return
        self.backend.before_finished(self, native)
        self.emit('turn.finished', turn=turn, status=status, native=native, **fields)
        for key in list(self.requests):
            self.emit('request.expired', request_id=key, reason='turn_finished')
        self.requests.clear()
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
        if verb == 'send':
            self.accept(cmd)
        elif verb == 'cancel':
            while self.queued:
                self.dropped(self.queued.popleft(), 'cancelled')
            for item in self.inputs.values():
                if not item.get('delivered'):
                    item['cancelled'] = True
                    self.dropped(item, 'cancelled')
            for key in self.requests:
                self.emit('request.expired', request_id=key, reason='cancelled')
            self.requests.clear()
            self.cancelling = bool(self.turn or self.starting)
            self.backend.cancel(self, drop_queued=True)
            self.emit('cancel.accepted')
            self.last_activity = time.monotonic()
        elif verb == 'reply':
            key = cmd['request_id']
            if key not in self.requests:
                raise Rejected('unknown, replied or expired request', 4)
            self.backend.reply(self, self.requests[key], cmd['answer'])
            del self.requests[key]
            self.emit('request.replied', request_id=key)
            self.last_activity = time.monotonic()
        else:
            raise Rejected('unknown command', 2)
        self.save()

    def serve_connection(self, listener):
        conn, _ = listener.accept()
        with conn:
            conn.settimeout(1)
            _, uid, _ = struct.unpack('3i', conn.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            if uid != os.getuid():
                return
            try:
                raw = conn.makefile('rb').readline(MAX_COMMAND + 1)
                if len(raw) > MAX_COMMAND:
                    raise Rejected('command too large', 2)
                cmd = json.loads(raw)
                result = dict(ok=True, value=self.command(cmd))
            except Rejected as exc:
                result = dict(ok=False, error=str(exc), code=exc.code)
            except (ValueError, KeyError, TypeError) as exc:
                result = dict(ok=False, error='invalid command: ' + str(exc), code=2)
            try:
                conn.sendall(json.dumps(result).encode() + b'\n')
            except (BrokenPipeError, ConnectionResetError):
                pass  # Accepted commands survive the caller disconnecting.

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
                                    self.emit('error', message='invalid backend JSON', native=frame)
                                else:
                                    self.backend.frame(self, frame)
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
                    self.emit('error', message=str(exc))
                finally:
                    self.stopping = True
                    if self.turn or self.starting:
                        self.finished('interrupted' if self.exit_reason == 'terminated' else 'failed', None)
                    for item in self.inputs.values():
                        self.dropped(item, self.exit_reason)
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
                    failed = self.exit_reason not in ('idle', 'terminated') or self.failed
                    self.emit('runtime.stopped', reason=self.exit_reason)
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
    return dict(state=state['state'], queued=state.get('queued', 0),
                requests=state.get('requests', []), cwd=config['cwd'], model=config.get('model'))


def parser():
    p = argparse.ArgumentParser(description='harnessctl/1: send, cancel, status, reply')
    sub = p.add_subparsers(dest='verb', required=True)
    for verb in ('send', 'cancel', 'status', 'reply'):
        sp = sub.add_parser(verb)
        sp.add_argument('events', type=Path)
        if verb == 'send':
            sp.add_argument('--cwd')
            sp.add_argument('--model')
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
            state = json.loads(state_path.read_text()) if state_path.exists() else dict(state='idle')
            if state.pop('live', False):
                # The runtime never completed its own cleanup. Do not replay
                # uncertain inputs; the next send resumes only native history.
                for request in state.get('requests', []):
                    append_event(events, 'request.expired', request_id=request['request_id'], reason='runtime_lost')
                state.update(state='failed', queued=0, requests=[])
                append_event(events, 'runtime.stopped', reason='lost')
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


def launch(events, backend, config):
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
            Runtime(events, backend, config).run()
            return 0
        args = parser().parse_args()
        content = body(args.prompt) if args.verb == 'send' else body(args.answer) if args.verb == 'reply' else None
        events = args.events.expanduser().resolve()
        directory = Path(str(events) + '.d')
        if not directory.exists() and args.verb != 'send':
            raise Rejected('conversation does not exist', 4)
        directory.mkdir(mode=0o700, exist_ok=True)
        # This lock serializes first creation, restarts, send and cancel; two
        # concurrent first sends can never create two native contexts.
        with open(directory / 'control.lock', 'a') as control:
            fcntl.flock(control, fcntl.LOCK_EX)
            path = directory / 'config.json'
            if path.exists():
                config = json.loads(path.read_text())
                if not events.is_file():
                    raise Rejected('event file is missing; refusing to silently recreate conversation history')
                if config.get('adapter') != backend.NAME:
                    raise Rejected('event file belongs to another adapter', 2)
                if args.verb == 'send':
                    if args.cwd is not None and str(Path(args.cwd).expanduser().resolve(strict=True)) != config['cwd']:
                        raise Rejected('cwd is bound to this event path; use a different event file', 2)
                    if args.model is not None and args.model != config.get('model'):
                        raise Rejected('model is bound to this event path; use a different event file', 2)
            else:
                if args.verb != 'send':
                    raise Rejected('conversation does not exist', 4)
                cwd = str(Path(args.cwd or os.getcwd()).expanduser().resolve(strict=True))
                if not Path(cwd).is_dir():
                    raise Rejected('cwd must be a directory', 2)
                try:
                    fd = os.open(events, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                    os.close(fd)
                except FileExistsError as exc:
                    raise Rejected('existing event file has no adapter state; refusing to overwrite it', 2) from exc
                config = dict(adapter=backend.NAME, cwd=cwd, model=args.model, history_started=False)
                write_json(path, config)
            state, live = inspect_state(events, config)
            if args.verb == 'status':
                print(json.dumps(state, ensure_ascii=False))
                return 0
            if args.verb == 'send':
                if not live:
                    launch(events, backend, config)
                rpc(events, dict(verb='send', prompt=content))
            elif args.verb == 'cancel':
                if live:
                    rpc(events, dict(verb='cancel'))
                else:
                    if backend_alive(directory):
                        raise Rejected('runtime lost but native process still running; cancellation cannot be confirmed')
                    append_event(events, 'cancel.accepted')
            else:
                if not live:
                    raise Rejected('request has expired', 4)
                rpc(events, dict(verb='reply', request_id=args.request_id, answer=content))
        return 0
    except Rejected as exc:
        print(str(exc), file=sys.stderr)
        return exc.code
    except (OSError, ValueError, KeyError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
