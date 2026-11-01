"""Claude Code stream-json control adapter.

Queues are owned here; native `next` steering has no turn-correlated ack.
User replay is reported as observed, never falsely labelled injected.
"""
import json
import time
import uuid

NAME = 'claudecode'
BINARY = 'claude'


def argv(config):
    args = [BINARY, '-p', '--input-format', 'stream-json', '--output-format', 'stream-json',
            '--verbose', '--replay-user-messages', '--permission-prompt-tool', 'stdio']
    if config['resume']:
        args += ['--resume', config['resume']]
    if config['model']:
        args += ['--model', config['model']]
    return args + config['backend_args']


def initialize(r):
    r.claude_writes = {}
    r.claude_interrupt = False
    key = 'ctl:init'
    r.pending[key] = ('initialize', None, time.monotonic())
    r.write(dict(type='control_request', request_id=key, request=dict(subtype='initialize')))
    # Claude starts the native session only after its first user message.
    r.thread = r.config.get('resume') or 'pending'


def write_user(r, item, priority):
    # CLI UUID fields must remain UUIDs even when the caller uses a named ID.
    native_id = str(uuid.uuid4())
    r.claude_writes[native_id] = item
    r.write(dict(type='user', uuid=native_id, message=dict(role='user', content=item['text']),
                 parent_tool_use_id=None, session_id='' if r.thread == 'pending' else r.thread,
                 priority=priority))


def start_turn(r, item):
    r.turn = 'turn_' + str(uuid.uuid4())
    r.starting = False
    r.emit('turn.started', turn=r.turn, evidence='adapter_submission')
    write_user(r, item, 'next')
    item['started_turn'] = r.turn


def steer(r, item):
    write_user(r, item, 'next')
    # No input.delivered yet: writing stdin is not proof of native delivery.


def cancel(r, drop_queued=False):
    if not r.turn:
        return
    r.claude_interrupt = True
    key = 'ctl:interrupt:' + str(uuid.uuid4())
    r.write(dict(type='control_request', request_id=key,
                 request=dict(subtype='interrupt', cancel_queued=drop_queued)))
    r.pending[key] = ('interrupt', None, time.monotonic())


def reply(r, request, answer):
    native = request['native']
    body = native['request']
    if request['kind'] == 'approval':
        if answer == 'allow':
            result = dict(behavior='allow', updatedInput=body.get('input', {}))
        elif answer == 'deny':
            result = dict(behavior='deny', message='Denied by caller')
        else:
            from runtime import Rejected
            raise Rejected('approval answer must be allow or deny', 2)
    else:
        # Caller passes the native JSON response, not a second protocol envelope.
        result = json.loads(answer)
    r.write(dict(type='control_response', response=dict(subtype='success',
                 request_id=native['request_id'], response=result)))


def before_finished(r, native):
    # Retain honest uncertainty for native `next` messages: replay is receipt,
    # and result/echo ordering alone does not prove model consumption or turn.
    for item in r.claude_writes.values():
        if native is not None and item.get('started_turn') == r.turn and not item.get('delivered'):
            r.delivered(item, 'started_turn', native, turn=r.turn, evidence='native_result')
        if not item.get('delivered') and item.get('observed'):
            r.delivered(item, 'unknown', native, evidence='user_replay',
                        reason='native protocol has no turn-correlated steer receipt')


def frame(r, f):
    typ = f.get('type')
    if typ == 'control_response':
        response = f.get('response', {})
        pending = r.pending.pop(response.get('request_id'), None)
        if pending and pending[0] == 'interrupt':
            r.emit('control.response', operation='interrupt', native=f)
        if pending and pending[0] == 'initialize' and response.get('subtype') == 'error':
            raise RuntimeError('Claude control initialization failed: ' + str(response.get('error')))
        if pending and pending[0] == 'interrupt' and response.get('subtype') == 'error':
            r.claude_interrupt = False
            r.emit('error', message=str(response.get('error')), native=f)
        return
    if typ == 'system' and f.get('subtype') == 'init':
        r.thread = f['session_id']
        r.emit('session.started', adapter=NAME, native_session=r.thread, native=f)
    elif typ == 'user':
        item = r.claude_writes.get(f.get('uuid'))
        if item:
            item['observed'] = True
            r.emit('input.observed', input=item['id'], evidence='user_replay', native=f)
            if item.get('started_turn'):
                r.delivered(item, 'started_turn', f, turn=item['started_turn'], evidence='user_replay')
        content = f.get('message', {}).get('content', [])
        if isinstance(content, list):
            for block in content:
                if block.get('type') == 'tool_result':
                    r.emit('tool.finished', turn=r.turn, tool=block.get('tool_use_id'),
                           status='failed' if block.get('is_error') else 'completed', native=f)
    elif typ == 'assistant':
        # A next-priority input can create a further native turn after a result.
        if not r.turn:
            r.turn = 'turn_' + str(uuid.uuid4())
            r.emit('turn.started', turn=r.turn, evidence='assistant_output', native=f)
        for block in f.get('message', {}).get('content', []):
            if block.get('type') == 'text':
                r.emit('message', turn=r.turn, text=block.get('text', ''), native=f)
            elif block.get('type') == 'tool_use':
                r.emit('tool.started', turn=r.turn, tool=block.get('id'), title=block.get('name'), native=f)
    elif typ == 'stream_event':
        event = f.get('event', {})
        if event.get('type') == 'content_block_delta' and event.get('delta', {}).get('type') == 'text_delta':
            r.emit('message.delta', turn=r.turn, text=event['delta']['text'], native=f)
    elif typ == 'result':
        if not r.turn:
            r.turn = 'turn_' + str(uuid.uuid4())
            r.emit('turn.started', turn=r.turn, evidence='result', native=f)
        r.emit('usage', turn=r.turn, usage=f.get('usage'), cost_usd=f.get('total_cost_usd'), native=f)
        status = 'interrupted' if r.claude_interrupt else 'failed' if f.get('is_error') else 'completed'
        r.claude_interrupt = False
        r.finished(status, f, output=f.get('result'))
    elif typ == 'control_request':
        key = f['request_id']
        body = f.get('request', {})
        approval = body.get('subtype') == 'can_use_tool' and body.get('tool_name') != 'AskUserQuestion'
        request = dict(native=f, kind='approval' if approval else 'question')
        r.requests[key] = request
        r.emit('request.opened', request=key, kind=request['kind'],
               summary=body.get('tool_name',body.get('subtype')), payload=body, native=f)
    elif typ == 'user_message_rejected':
        item = r.claude_writes.get(f.get('uuid'))
        if item:
            r.dropped(item, f.get('reason', 'native_rejected'))
