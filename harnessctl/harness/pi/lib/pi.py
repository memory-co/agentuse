"""Adapter for Pi's native JSONL RPC mode."""
from pathlib import Path
import re
import subprocess
from time import monotonic

from runtime import Rejected, question_answers

NAME = 'pi'
BINARY = 'pi'
STEER_WHILE_BUSY = False
MIN_VERSION = (1, 6, 0)
DIALOGS = ('confirm', 'select', 'input', 'editor')


def session_dir(config):
    return Path(config['resources_dir']).parent / 'pi-sessions'


def session_path(config, value):
    if not value or not Path(value).is_absolute():
        raise RuntimeError('Pi did not provide an absolute session path')
    path = Path(value).resolve()
    if path.parent != session_dir(config).resolve():
        raise RuntimeError('Pi session is outside the adapter state directory')
    return path


def argv(config):
    version = subprocess.run([BINARY, '--version'], cwd=config['cwd'],
                             capture_output=True, text=True, timeout=15)
    match = re.fullmatch(r'v?(\d+)\.(\d+)\.(\d+)(?:\+[^\s]+)?', version.stdout.strip())
    if version.returncode or not match or tuple(map(int, match.groups())) < MIN_VERSION:
        raise RuntimeError('Pi 1.6.0 or later is required for the agent_settled RPC event')

    sessions = session_dir(config)
    args = [BINARY, '--mode', 'rpc', '--session-dir', str(sessions)]
    if config.get('resume'):
        resume = session_path(config, config['resume'])
        if not resume.is_file():
            raise RuntimeError('Pi session file is missing; refusing to start a new session')
        args.extend(['--session', str(resume)])
    if config.get('model'):
        args.extend(['--model', config['model']])
    resources = config['resources']
    directory = Path(config['resources_dir'])
    if resources['agents'] is not None:
        args.extend(['--append-system-prompt', str(directory / 'agents.md')])
    for skill in resources['skills']:
        args.extend(['--skill', str(directory / 'skills' / skill['name'] / 'SKILL.md')])
    return args


def command(r, method, **params):
    r.counter += 1
    key = 'ctl:' + str(r.counter)
    r.pending[key] = (method, None, monotonic())
    r.write(dict(type=method, id=key, **params))
    return key


def initialize(r):
    command(r, 'get_state')


def start_turn(r, item):
    r.pi_reply_expected = command(r, 'prompt', message=item['text'])
    # Prompt preflight can wait for a human to answer an extension dialog.
    r.pending.pop(r.pi_reply_expected)
    r.pi_abort = None
    r.pi_dialogs = set()
    r.pi_message_number = 0
    r.pi_stop = None
    r.pi_error = None
    r.turn = r.pi_reply_expected
    r.starting = False
    r.log('execution.started', evidence='adapter_submission')


def cancel(r, drop_queued=False):
    if r.turn and r.pi_abort is None:
        r.pi_abort = command(r, 'abort')
        # An editor dialog can keep the native tool awaiting input during abort.
        for key in r.pi_dialogs:
            r.write(dict(type='extension_ui_response', id=key, cancelled=True))
        r.pi_dialogs.clear()


def decline(r, native):
    r.write(dict(type='extension_ui_response', id=native['id'], cancelled=True))


def reply(r, request, answer):
    native = request['native']
    if request['kind'] == 'permission':
        answer = answer.strip()
        if answer not in ('allow', 'deny'):
            raise Rejected('approval answer must be allow or deny', 2)
        fields = dict(confirmed=answer == 'allow')
    else:
        value = question_answers(request, answer)['value'][0]
        if native['method'] == 'select' and value not in native['options']:
            raise Rejected('answer must match one of the Pi selection options', 2)
        fields = dict(value=value)
    r.write(dict(type='extension_ui_response', id=native['id'], **fields))
    r.pi_dialogs.discard(native['id'])


def before_finished(r, native):
    pass


def dialog(r, f):
    method = f.get('method')
    if method not in DIALOGS or not f.get('id'):
        return
    if not r.turn or r.cancelling:
        decline(r, f)
        return
    title = f.get('title') or 'Pi'
    r.pi_dialogs.add(f['id'])
    if method == 'confirm':
        message = f.get('message')
        r.open_request(f, 'permission', title + '\n\n' + message if message else title)
    else:
        options = [dict(label=label, description='') for label in f.get('options', [])]
        question = dict(id='value', text=title, header='', multiple=False, options=options)
        r.open_request(f, 'question', title, [question])


def response(r, f):
    key = f.get('id')
    if key is not None and r.turn and key == r.pi_reply_expected:
        method = 'prompt'
    else:
        pending = r.pending.pop(key, None)
        if pending is None:
            return
        method = pending[0]
    error = f.get('error') or f'Pi {method} failed'
    data = f.get('data') or {}
    if method == 'get_state':
        if not f.get('success'):
            raise RuntimeError(error)
        path = str(session_path(r.config, data.get('sessionFile')))
        if r.config.get('resume') and path != r.config['resume']:
            raise RuntimeError('Pi did not restore the expected native session')
        r.thread = path
        model = (data.get('model') or {}).get('id')
        r.model(model, session=True)
        r.native_ready(dict(sessionId=data.get('sessionId'), model=model))
        r.drain()
    elif method == 'prompt':
        r.pi_reply_expected = None
        started = f.get('success') and data.get('disposition') == 'started'
        if r.cancelling:
            # Abort may have completed while the prompt was still in preflight.
            if r.pi_abort is None:
                if started:
                    cancel(r)
                else:
                    r.finished('cancelled', f)
        elif not f.get('success'):
            r.finished('failed', f, error=error)
        elif data.get('disposition') == 'handled':
            r.finished('completed', f)
        elif not started:
            # A queued native prompt would execute outside its harness run.
            raise RuntimeError('Pi did not start the prompt in the current run')
    elif method == 'abort':
        if not f.get('success'):
            raise RuntimeError(error)
        r.pi_abort = None
        if r.pi_reply_expected is None:
            r.finished('cancelled', f)


def assistant_end(r, message):
    r.model(message.get('model'))
    r.pi_stop = message.get('stopReason')
    r.pi_error = message.get('errorMessage')
    text = ''.join(block['text'] for block in message.get('content', [])
                   if block.get('type') == 'text')
    if text:
        r.message(text, r.pi_message_number)

    usage = message.get('usage') or {}
    counts = [usage.get(key) for key in ('input', 'output', 'cacheRead', 'cacheWrite')]
    if not all(type(value) is int and value >= 0 for value in counts):
        return
    input_tokens, output_tokens, cache_read, cache_write = counts
    current = dict(input_tokens=input_tokens + cache_read + cache_write,
                   output_tokens=output_tokens, cache_read_tokens=cache_read,
                   cache_write_tokens=cache_write)
    previous = r.work_usage or {}
    total = {key: value + previous.get(key, 0) for key, value in current.items()}
    cost = (usage.get('cost') or {}).get('total')
    r.usage(total, (r.work_cost or 0) + cost if isinstance(cost, (int, float)) and cost >= 0 else None)


def frame(r, f):
    kind = f.get('type')
    if kind == 'response':
        response(r, f)
        return
    if kind == 'extension_ui_request':
        dialog(r, f)
        return
    if not r.turn or f.get('parentToolCallId'):
        return
    message = f.get('message') or {}
    if kind == 'message_start' and message.get('role') == 'assistant':
        r.pi_message_number += 1
        r.model(message.get('model'))
    elif kind == 'message_update':
        event = f.get('assistantMessageEvent') or {}
        if event.get('type') == 'text_delta':
            r.chunk(event['delta'], r.pi_message_number)
    elif kind == 'message_end' and message.get('role') == 'assistant':
        assistant_end(r, message)
    elif kind in ('tool_execution_start', 'tool_execution_end'):
        topic = 'tool.started' if kind == 'tool_execution_start' else 'tool.finished'
        r.log(topic, tool=f.get('toolName'), tool_call_id=f.get('toolCallId'), is_error=f.get('isError'))
    elif kind == 'agent_settled' and not r.cancelling:
        if f.get('aborted') or r.pi_stop == 'aborted':
            r.finished('cancelled', f)
        elif r.pi_stop == 'error':
            r.finished('failed', f, error=r.pi_error or 'Pi model execution failed')
        else:
            r.finished('completed', f)
