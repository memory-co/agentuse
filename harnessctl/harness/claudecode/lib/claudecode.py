"""Claude Code stream-json control adapter.

Busy input is sent to the native queue and bound only on consumption receipts.
User replay alone is receipt, never proof of consumption.
"""
import json
import time
import uuid

NAME = 'claudecode'
BINARY = 'claude'
# Claude Code 2.1.316 exposes command_lifecycle and user_message_uuids.
# These identify consumed inputs, including those folded between tool rounds.
STEER_WHILE_BUSY = True


def argv(config):
    args = [BINARY, '-p', '--input-format', 'stream-json', '--output-format', 'stream-json',
            '--verbose', '--replay-user-messages', '--permission-mode', 'bypassPermissions',
            '--permission-prompt-tool', 'stdio']
    from pathlib import Path
    resources = Path(config['resources_dir'])
    if config['resources']['agents'] is not None:
        args += ['--append-system-prompt-file', str(resources/'agents.md')]
    if config['resources']['skills']:
        args += ['--plugin-dir', str(resources/'native')]
    if config['resume']:
        args += ['--resume', config['resume']]
    if config['model']:
        args += ['--model', config['model']]
    return args


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
    r.log('execution.started', evidence='adapter_submission')
    write_user(r, item, 'next')
    item['started_turn'] = r.turn


def steer(r, item):
    write_user(r, item, 'next')
    # No input.delivered yet: writing stdin is not proof of native delivery.


def cancel(r, drop_queued=False):
    if drop_queued:
        for native_id, item in r.claude_writes.items():
            if not item.get('delivered'):
                key = 'ctl:withdraw:' + native_id
                r.pending[key] = ('cancel_async_message', item, time.monotonic())
                r.write(dict(type='control_request', request_id=key,
                             request=dict(subtype='cancel_async_message', message_uuid=native_id)))
    if not r.turn:
        return
    r.claude_interrupt = True
    key = 'ctl:interrupt:' + str(uuid.uuid4())
    r.write(dict(type='control_request', request_id=key,
                 request=dict(subtype='interrupt', cancel_queued=drop_queued)))
    r.pending[key] = ('interrupt', None, time.monotonic())


def reply(r, request, answer):
    from runtime import question_answers
    native = request['native']
    body = native['request']
    if request['kind'] == 'permission':
        answer = answer.strip()
        if answer == 'allow':
            result = dict(behavior='allow', updatedInput=body.get('input', {}))
        elif answer == 'deny':
            result = dict(behavior='deny', message='Denied by caller')
        else:
            from runtime import Rejected
            raise Rejected('approval answer must be allow or deny', 2)
    else:
        answers = question_answers(request, answer)
        updated = dict(body.get('input', {}))
        updated['answers'] = {q['question']: ', '.join(answers[public['id']])
                              for q, public in zip(body['input']['questions'], request['public']['questions'])}
        result = dict(behavior='allow', updatedInput=updated)
    r.write(dict(type='control_response', response=dict(subtype='success',
                 request_id=native['request_id'], response=result)))


def decline(r, native):
    r.write(dict(type='control_response', response=dict(subtype='error',
                 request_id=native['request_id'], error='Execution cancelled')))


def consumed(r, item, native):
    if item.get('resolved') or item.get('cancelled') or item.get('delivered'):
        return
    item['native_queued'] = False
    if not r.turn:
        # The previous result won the race: native now starts a new interaction.
        from runtime import identity
        r.turn = 'turn_' + identity()
        r.active_run_id = identity()
        r.work_inputs = []
        r.messages = {}
        r.message_order = []
        r.work_usage = None
        r.work_cost = None
        r.last_error = None
        r.log('execution.started', evidence='native_consumption', native=native)
    outcome = 'started_turn' if item.get('started_turn') or not r.work_inputs else 'injected'
    r.delivered(item, outcome, native, turn=r.turn, evidence='native_consumption')


def before_finished(r, native):
    # Initial inputs are already bound when submitted. Supplemental inputs must
    # have a consumption receipt; neither stdin writes nor replay prove it.
    for item in r.claude_writes.values():
        if native is not None and item.get('started_turn') == r.turn and not item.get('delivered'):
            consumed(r, item, native)


def frame(r, f):
    typ = f.get('type')
    # Results list all inputs consumed in this native interaction; assistant /
    # stream frames may carry the same correlation before the final result.
    ids = f.get('user_message_uuids')
    if not isinstance(ids, list):
        ids = [f.get('user_message_uuid')]
    for key in ids:
        item = r.claude_writes.get(key) if isinstance(key, str) else None
        if item:
            consumed(r, item, f)
    if typ == 'command_lifecycle':
        item = r.claude_writes.get(f.get('command_uuid'))
        if item:
            item['native_queued'] = f.get('state') == 'queued' and not item.get('resolved')
            r.log('input.lifecycle', state=f.get('state'), input_id=item['id'], native=f)
            if f.get('state') == 'started':
                consumed(r, item, f)
            elif f.get('state') in ('cancelled', 'discarded', 'refused') and not item.get('delivered'):
                r.dropped(item, 'cancelled' if f['state'] == 'cancelled' else f['state'])
        return
    if typ == 'control_response':
        response = f.get('response', {})
        pending = r.pending.pop(response.get('request_id'), None)
        if pending and pending[0] == 'interrupt':
            r.log('control.response', operation='interrupt', native=f)
        if pending and pending[0] == 'initialize' and response.get('subtype') == 'error':
            raise RuntimeError('Claude control initialization failed: ' + str(response.get('error')))
        if pending and pending[0] == 'interrupt' and response.get('subtype') == 'error':
            r.claude_interrupt = False
            r.cancelling = False
            r.log('native.error', level='error', message=str(response.get('error')), native=f)
        return
    if typ == 'system' and f.get('subtype') == 'init':
        r.thread = f['session_id']
        r.native_ready(f)
    elif typ == 'user':
        item = r.claude_writes.get(f.get('uuid'))
        if item:
            item['observed'] = True
            r.log('input.observed', input_id=item['id'], evidence='user_replay', native=f)
            if item.get('started_turn'):
                r.delivered(item, 'started_turn', f, turn=item['started_turn'], evidence='user_replay')
        content = f.get('message', {}).get('content', [])
        if isinstance(content, list):
            for block in content:
                if block.get('type') == 'tool_result':
                    r.log('tool.finished', turn=r.turn, tool=block.get('tool_use_id'),
                           status='failed' if block.get('is_error') else 'completed', native=f)
    elif typ == 'assistant':
        # A next-priority input can create a further native turn after a result.
        if not r.turn:
            r.turn = 'turn_' + str(uuid.uuid4())
            r.log('execution.started', evidence='assistant_output', native=f)
        blocks = f.get('message', {}).get('content', [])
        texts = [block.get('text', '') for block in blocks if block.get('type') == 'text']
        if not all(isinstance(text, str) for text in texts):
            raise ValueError('native complete message must contain string text blocks')
        if texts:
            r.message(''.join(texts), f.get('message', {}).get('id') or f.get('uuid'))
        for block in blocks:
            if block.get('type') == 'tool_use':
                r.log('tool.started', turn=r.turn, tool=block.get('id'), title=block.get('name'), status='running', native=f)
    elif typ == 'stream_event':
        event = f.get('event', {})
        if event.get('type') == 'message_start':
            r.claude_stream_key = event.get('message', {}).get('id')
        if event.get('type') == 'content_block_delta' and event.get('delta', {}).get('type') == 'text_delta':
            r.chunk(event['delta']['text'], getattr(r, 'claude_stream_key', None))
    elif typ == 'result':
        if not r.turn:
            r.turn = 'turn_' + str(uuid.uuid4())
            r.log('execution.started', evidence='result', native=f)
        raw = f.get('usage')
        counts = None if raw is None else dict(input_tokens=raw.get('input_tokens',0)+raw.get('cache_read_input_tokens',0)+raw.get('cache_creation_input_tokens',0),output_tokens=raw.get('output_tokens',0),cache_read_tokens=raw.get('cache_read_input_tokens',0),cache_write_tokens=raw.get('cache_creation_input_tokens',0))
        r.usage(counts, f.get('total_cost_usd'))
        status = 'interrupted' if r.claude_interrupt else 'failed' if f.get('is_error') else 'completed'
        r.claude_interrupt = False
        r.finished(status, f, output=f.get('result'), error='; '.join(f.get('errors', [])) or None)
    elif typ == 'control_request':
        body = f.get('request', {})
        if body.get('subtype') == 'can_use_tool':
            if body.get('tool_name') == 'AskUserQuestion':
                from runtime import question_specs
                r.open_request(f, 'question', 'Please answer the questions',
                               question_specs(body.get('input', {}).get('questions', [])))
            else:
                command = body.get('input', {}).get('command')
                r.open_request(f, 'permission', command or body.get('tool_name', 'Tool execution'))
        else:
            r.write(dict(type='control_response', response=dict(subtype='error',
                         request_id=f['request_id'], error='Unsupported native request')))
            r.log('native.error', level='error', message='Unsupported native request', native=f)
    elif typ == 'user_message_rejected':
        item = r.claude_writes.get(f.get('uuid'))
        if item:
            r.dropped(item, f.get('reason', 'native_rejected'))
