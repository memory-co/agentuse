"""Google Antigravity ACP v1 stdio adapter."""
import json
from pathlib import Path
from time import monotonic

NAME = 'agy'
BINARY = 'agy'
STEER_WHILE_BUSY = False


def argv(config):
    return [BINARY, 'acp']


def send(r, **fields):
    r.write(dict(jsonrpc='2.0', **fields))


def request(r, method, params):
    r.counter += 1
    key = 'ctl:' + str(r.counter)
    r.pending[key] = (method, None, monotonic())
    send(r, id=key, method=method, params=params)
    return key


def initialize(r):
    r.prompt_request = None
    request(r, 'initialize', dict(protocolVersion=1, clientCapabilities={},
                                  clientInfo=dict(name='harnessctl', version='0.1.0')))


def open_session(r, capabilities):
    params = dict(cwd=r.config['cwd'], mcpServers=[])
    resources = r.config['resources']
    directory = Path(r.config['resources_dir'])
    instructions = []
    if resources['agents'] is not None:
        instructions.append((directory/'agents.md').read_text(encoding='utf-8'))
    if resources['skills']:
        skills = [
            dict(**skill, path=str(directory/'skills'/skill['name']/'SKILL.md'))
            for skill in resources['skills']
        ]
        instructions.append('Available skills (read the listed SKILL.md when its description matches the task, '
                            'and resolve its file references relative to that SKILL.md):\n'
                            + json.dumps(skills, ensure_ascii=False))
    if instructions:
        extension = (capabilities.get('_meta') or {}).get('antigravity', {})
        if not extension.get('systemPrompt', {}).get('append'):
            raise RuntimeError('installed agy does not support appending session instructions')
        params['_meta'] = dict(antigravity=dict(systemPrompt=dict(append='\n\n'.join(instructions))))
    native = r.config.get('resume')
    if native:
        params['sessionId'] = native
    request(r, 'session/resume' if native else 'session/new', params)


def start_turn(r, item):
    params = dict(sessionId=r.thread, prompt=[dict(type='text', text=item['text'])])
    r.prompt_request = request(r, 'session/prompt', params)
    # Model turns have no control-RPC deadline. Cancellation restores the deadline.
    r.pending.pop(r.prompt_request)
    r.turn = r.prompt_request
    r.starting = False
    r.log('execution.started', evidence='adapter_submission')


def cancel(r, drop_queued=False):
    if r.prompt_request:
        r.pending[r.prompt_request] = ('session/prompt', None, monotonic())
        send(r, method='session/cancel', params=dict(sessionId=r.thread))


def permission(r, native, option=None):
    outcome = dict(outcome='cancelled') if option is None else dict(outcome='selected', optionId=option)
    send(r, id=native['id'], result=dict(outcome=outcome))


def decline(r, native):
    permission(r, native)


def reply(r, request, answer):
    from runtime import Rejected
    answer = answer.strip()
    if answer not in ('allow', 'deny'):
        raise Rejected('approval answer must be allow or deny', 2)
    options = request['native']['params']['options']
    kind = 'allow_once' if answer == 'allow' else 'reject_once'
    option = next((entry['optionId'] for entry in options if entry['kind'] == kind), None)
    if answer == 'allow' and option is None:
        raise Rejected('agy did not offer a one-time approval', 2)
    permission(r, request['native'], option)


def before_finished(r, native):
    pass


def handle_update(r, f):
    params = f.get('params', {})
    if not r.turn or params.get('sessionId') != r.thread:
        return
    update = params.get('update', {})
    kind = update.get('sessionUpdate')
    if kind == 'agent_message_chunk':
        content = update.get('content', {})
        if content.get('type') == 'text':
            r.chunk(content['text'])
    elif kind in ('tool_call', 'tool_call_update'):
        status = update.get('status')
        topic = 'tool.finished' if status in ('completed', 'failed') else 'tool.started'
        r.log(topic, turn=r.turn, tool=update.get('toolCallId'), title=update.get('title'),
              status=status, native=f)


def finish_prompt(r, f):
    r.prompt_request = None
    if 'error' in f:
        r.finished('failed', f, error=f['error'].get('message') or 'agy prompt failed')
        return
    result = f.get('result', {})
    usage = result.get('usage')
    if isinstance(usage, dict) and all(isinstance(usage.get(k), int) for k in ('inputTokens', 'outputTokens')):
        cache_read = usage.get('cachedReadTokens') or 0
        cache_write = usage.get('cachedWriteTokens') or 0
        r.usage(dict(input_tokens=usage['inputTokens'] + cache_read + cache_write,
                     output_tokens=usage['outputTokens'], cache_read_tokens=cache_read,
                     cache_write_tokens=cache_write))
    reason = result.get('stopReason')
    if reason == 'cancelled':
        r.finished('cancelled', f)
    elif reason == 'end_turn':
        r.finished('completed', f)
    else:
        r.finished('failed', f, error='agy stopped: '+str(reason))


def frame(r, f):
    method = f.get('method')
    key = f.get('id')
    if method == 'session/update':
        handle_update(r, f)
        return
    if method == 'session/request_permission' and key is not None:
        params = f.get('params', {})
        if not r.turn or params.get('sessionId') != r.thread:
            decline(r, f)
        else:
            r.open_request(f, 'permission', (params.get('toolCall') or {}).get('title') or 'Tool execution')
        return
    if method:
        if key is not None:
            send(r, id=key, error=dict(code=-32601, message='Unsupported ACP client method'))
        return
    if key is None:
        return
    pending = r.pending.pop(key, None)
    if key == r.prompt_request:
        finish_prompt(r, f)
        return
    if pending is None:
        return
    method = pending[0]
    if 'error' in f:
        raise RuntimeError(method+': '+str(f['error'].get('message') or 'agy request failed'))
    result = f.get('result', {})
    if method == 'initialize':
        if result.get('protocolVersion') != 1:
            raise RuntimeError('agy did not negotiate ACP v1')
        open_session(r, result.get('agentCapabilities') or {})
    elif method in ('session/new', 'session/resume'):
        native = result.get('sessionId') or r.config.get('resume')
        if not native or (r.config.get('resume') and native != r.config['resume']):
            raise RuntimeError('agy did not restore the expected native session')
        r.agy_session = native
        current = (result.get('models') or {}).get('currentModelId')
        model = r.config.get('model')
        if model and model != current:
            request(r, 'session/set_model', dict(sessionId=native, modelId=model))
        else:
            ready(r, f, current)
    elif method == 'session/set_model':
        ready(r, f, r.config['model'])


def ready(r, f, model):
    r.thread = r.agy_session
    r.model(model, session=True)
    r.native_ready(f)
    r.drain()
