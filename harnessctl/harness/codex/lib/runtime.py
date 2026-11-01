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
import shutil
import signal
import socket
import struct
import subprocess
import sys
import threading
import time
import uuid

PROTOCOL = 'harnessctl/0'
MAX_COMMAND = 1024 * 1024


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
        self.replies = set()
        self.pending = {}
        self.counter = 0
        self.frames = queue.Queue()
        self.exit_reason = 'backend_exited'
        self.last_status = None

    def emit(self, event_type, **fields):
        append_event(self.events, event_type, **fields)

    def snapshot(self):
        state = 'awaiting_reply' if self.requests else 'running' if self.turn or self.starting else 'idle'
        return dict(state=state, turn=self.turn, queued=len(self.queued),
                    open_requests=len(self.requests), native_session=self.thread,
                    adapter=self.backend.NAME, pid=os.getpid())

    def save(self):
        value = self.snapshot()
        if value != self.last_status:
            write_json(self.directory / 'status.json', value)
            self.last_status = value

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

    def accept(self, command, initial=False):
        key = command.get('id') or identity()
        item = dict(id=key, text=command['prompt'], delivery=command.get('delivery', 'steer'),
                    if_idle=command.get('if_idle', 'start'))
        if key in self.inputs:
            previous = {k: self.inputs[key][k] for k in item}
            if previous != item:
                raise Rejected('input id already used with different content', 2)
            return
        busy = self.turn is not None or self.starting
        if not initial and not busy and item['if_idle'] == 'fail':
            raise Rejected('no active turn', 5)
        self.inputs[key] = item
        self.emit('input.accepted', input=key, delivery='start' if initial else item['delivery'])
        if busy and item['delivery'] == 'steer' and self.turn:
            self.backend.steer(self, item)
        else:
            if busy and item['delivery'] == 'interrupt':
                self.queued.appendleft(item)
                self.backend.cancel(self)
            else:
                self.queued.append(item)
            if busy:
                self.emit('input.queued', input=key)
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
            self.emit('request.expired', request=key, reason='turn_finished')
        self.requests.clear()
        self.turn = None
        self.starting = False
        self.drain()

    def command(self, cmd):
        verb = cmd['verb']
        if verb == 'status':
            return self.snapshot()
        if verb == 'send':
            self.accept(cmd)
        elif verb == 'cancel':
            if cmd.get('drop_queued'):
                while self.queued:
                    self.dropped(self.queued.popleft(), 'cancelled')
            self.backend.cancel(self, drop_queued=bool(cmd.get('drop_queued')))
        elif verb == 'reply':
            key = cmd['request']
            if key in self.replies:
                raise Rejected('request already replied', 4)
            if key not in self.requests:
                raise Rejected('unknown or expired request', 4)
            request = self.requests[key]
            self.backend.reply(self, request, cmd['answer'])
            self.replies.add(key)
            del self.requests[key]
            # A successful write confirms submission, not native resolution.
            self.emit('request.replied', request=key)
        elif verb == 'close':
            self.exit_reason = 'closed'
            self.stopping = True
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
                        self.accept(self.config['initial'], initial=True)
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
                            if any(time.monotonic()-p[2] > 60 for p in self.pending.values()):
                                raise RuntimeError('native control response timed out')
                except Exception as exc:
                    self.exit_reason = 'error'
                    self.emit('error', message=str(exc))
                finally:
                    self.stopping = True
                    if self.turn or self.starting:
                        self.finished('interrupted' if self.exit_reason in ('closed','terminated') else 'failed', None)
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
                    self.emit('session.exited', reason=self.exit_reason)
                    write_json(self.directory / 'status.json', dict(self.snapshot(), state='exited'))


def parser():
    p = argparse.ArgumentParser(description='harnessctl/0 standalone harness adapter')
    sub = p.add_subparsers(dest='verb', required=True)
    sub.add_parser('doctor')
    for verb in ('start', 'send', 'cancel', 'reply', 'close', 'status'):
        sp = sub.add_parser(verb)
        sp.add_argument('events', type=Path)
        if verb in ('start', 'send'):
            sp.add_argument('--id')
            sp.add_argument('prompt')
        if verb == 'start':
            sp.add_argument('--cwd', default=os.getcwd())
            sp.add_argument('--resume', help='native session/thread ID')
            sp.add_argument('--model')
            sp.add_argument('--backend-arg', action='append', default=[], help='repeat with --backend-arg=VALUE')
        if verb == 'send':
            sp.add_argument('--delivery', choices=('steer','queue','interrupt'), default='steer')
            sp.add_argument('--if-idle', choices=('start','fail'), default='start')
        if verb == 'cancel':
            sp.add_argument('--drop-queued', action='store_true')
        if verb == 'reply':
            sp.add_argument('request')
            sp.add_argument('answer')
    sp = sub.add_parser('_worker', help=argparse.SUPPRESS)
    sp.add_argument('events', type=Path)
    return p


def main(backend):
    os.umask(0o077)
    args = parser().parse_args()
    try:
        if args.verb == 'doctor':
            found = shutil.which(backend.BINARY)
            version = None
            if found:
                try:
                    result = subprocess.run([found, '--version'], capture_output=True, text=True, timeout=5)
                    if result.returncode == 0:
                        version = result.stdout.strip().splitlines()[0]
                except (OSError, subprocess.TimeoutExpired, IndexError):
                    pass
            ok = sys.platform == 'linux' and bool(version)
            print(json.dumps(dict(ok=ok, protocol=PROTOCOL, adapter=backend.NAME,
                                  backend=found, version=version, platform=sys.platform)))
            return 0 if ok else 1
        events = args.events.expanduser().absolute()
        directory = Path(str(events) + '.d')
        if args.verb == '_worker':
            config = json.loads((directory / 'config.json').read_text())
            Runtime(events, backend, config).run()
            return 0
        if args.verb == 'start':
            if not args.prompt.strip():
                raise Rejected('prompt must not be empty', 2)
            if len(json.dumps(vars(args), default=str).encode()) > MAX_COMMAND:
                raise Rejected('command too large', 2)
            cwd = str(Path(args.cwd).expanduser().resolve(strict=True))
            if not Path(cwd).is_dir():
                raise Rejected('cwd must be a directory', 2)
            if not shutil.which(backend.BINARY):
                raise Rejected('backend not installed: ' + backend.BINARY)
            try:
                directory.mkdir(mode=0o700)
            except FileExistsError as exc:
                raise Rejected('session exists; choose a new event file', 2) from exc
            try:
                fd = os.open(events, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                os.close(fd)
            except OSError:
                directory.rmdir()
                raise
            initial = dict(verb='send', prompt=args.prompt, id=args.id or identity())
            config = dict(cwd=cwd, resume=args.resume, model=args.model, backend_args=args.backend_arg, initial=initial)
            write_json(directory / 'config.json', config)
            with open(directory / 'runtime.stderr.log', 'a') as log:
                process = subprocess.Popen([sys.executable, str(Path(sys.argv[0]).resolve()), '_worker', str(events)],
                                           stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                           stderr=log, start_new_session=True)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                try:
                    rpc(events, dict(verb='status'))
                    return 0
                except Rejected:
                    if process.poll() is not None:
                        raise Rejected('runtime startup failed; inspect ' + str(directory / 'runtime.stderr.log'))
                    time.sleep(.025)
            raise Rejected('runtime startup timed out; inspect status before retrying')
        if not directory.is_dir():
            raise Rejected('session does not exist', 4)
        config = json.loads((directory / 'config.json').read_text())
        # Adapter selection is part of the address, not a way to switch a live backend.
        state = json.loads((directory / 'status.json').read_text())
        if state['adapter'] != backend.NAME:
            raise Rejected('event file belongs to another adapter', 2)
        if args.verb == 'status':
            try:
                value = rpc(events, dict(verb='status'))
            except Rejected:
                # Serialize loss detection; never append duplicate exit events.
                with open(directory / 'runtime.lock', 'a') as lock:
                    try:
                        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    except BlockingIOError:
                        raise Rejected('runtime alive but unresponsive')
                    value = json.loads((directory / 'status.json').read_text())
                    if value['state'] != 'exited':
                        append_event(events, 'session.exited', reason='lost')
                        value.update(state='exited', turn=None, queued=0, open_requests=0)
                        write_json(directory / 'status.json', value)
            print(json.dumps(value))
            return 0
        if state['state'] == 'exited':
            raise Rejected('session has exited', 4)
        cmd = vars(args).copy()
        cmd.pop('events')
        if args.verb == 'send' and not args.prompt.strip():
            raise Rejected('prompt must not be empty', 2)
        raw = json.dumps(cmd).encode()
        if len(raw) > MAX_COMMAND:
            raise Rejected('command too large', 2)
        rpc(events, cmd)
        return 0
    except Rejected as exc:
        print(str(exc), file=sys.stderr)
        return exc.code
    except (OSError, ValueError, KeyError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
