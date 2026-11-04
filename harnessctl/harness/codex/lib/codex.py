"""Codex app-server JSON-RPC adapter."""
NAME = 'codex'
BINARY = 'codex'
STEER_WHILE_BUSY = True


def argv(config):
    args = [BINARY, 'app-server', '--listen', 'stdio://']
    if config['model']:
        import json
        args += ['-c', 'model=' + json.dumps(config['model'])]
    return args


def open_thread(r):
    params = dict(cwd=r.config['cwd'], approvalPolicy='never', sandbox='danger-full-access')
    if r.config['resources']['agents'] is not None:
        from pathlib import Path
        params['developerInstructions'] = (Path(r.config['resources_dir'])/'agents.md').read_text(encoding='utf8')
    resume = r.config.get('resume')
    if resume:
        params['threadId'] = resume
    r.request('thread/resume' if resume else 'thread/start', params)


def initialize(r):
    r.request('initialize', dict(clientInfo=dict(name='harnessctl', version='0.4.0'),
                                 capabilities=dict(experimentalApi=True)))


def text(value):
    return [dict(type='text', text=value, text_elements=[])]


def start_turn(r, item):
    r.request('turn/start', dict(threadId=r.thread, input=text(item['text'])), item)


def steer(r, item):
    item['expected_turn'] = r.turn
    r.request('turn/steer', dict(threadId=r.thread, expectedTurnId=r.turn, input=text(item['text'])), item)


def cancel(r, drop_queued=False):
    if r.turn:
        r.request('turn/interrupt', dict(threadId=r.thread, turnId=r.turn))
    elif r.starting:
        r.cancel_starting = True


def reply(r, request, answer):
    from runtime import question_answers
    native = request['native']
    if request['kind'] == 'permission':
        answer = answer.strip()
        if answer not in ('allow', 'deny'):
            from runtime import Rejected
            raise Rejected('approval answer must be allow or deny', 2)
        result = dict(decision='accept' if answer == 'allow' else 'decline')
    else:
        answers = question_answers(request, answer)
        result = dict(answers={key: dict(answers=value) for key, value in answers.items()})
    r.write(dict(id=native['id'], result=result))


def decline(r, native):
    r.write(dict(id=native['id'], error=dict(code=-32800, message='Execution cancelled')))


def before_finished(r, native):
    pass


def frame(r, f):
    method = f.get('method')
    if method and 'id' in f:
        params = f.get('params', {})
        approval = method in ('item/commandExecution/requestApproval', 'item/fileChange/requestApproval')
        if approval:
            r.open_request(f, 'permission', params.get('command') or params.get('reason') or method)
        elif method == 'item/tool/requestUserInput':
            from runtime import question_specs
            r.open_request(f, 'question', 'Please answer the questions', question_specs(params.get('questions', []), True))
        else:
            r.write(dict(id=f['id'], error=dict(code=-32601, message='Unsupported native request')))
            r.log('native.error', level='error', message='Unsupported native request: ' + method, native=f)
        return
    if 'id' in f:
        pending = r.pending.pop(f['id'], None)
        if pending is None:
            return
        method, item, _ = pending
        if item is not None and item.get('cancelled') and method == 'turn/steer':
            return
        if f.get('error'):
            reason = f['error'].get('message', 'native request failed')
            if method == 'turn/steer':
                r.log('input.rejected', input_id=item['id'], reason=reason, native=f)
                # Retry as a new turn only for the documented idle race, never
                # for auth errors or arbitrary backend failures.
                if 'no active turn' in reason.lower() or 'expected active turn' in reason.lower():
                    r.requeue(item)
                else:
                    r.dropped(item, reason)
            elif method == 'turn/start':
                r.finished('failed', f, error=reason)
            elif method == 'turn/interrupt':
                r.cancelling = False
                r.log('native.error', level='error', message=reason, native=f)
            else:
                raise RuntimeError(method + ': ' + reason)
            return
        result = f.get('result', {})
        if method == 'initialize':
            r.write(dict(method='initialized', params={}))
            if r.config['resources']['skills']:
                from pathlib import Path
                r.request('skills/extraRoots/set', dict(extraRoots=[str(Path(r.config['resources_dir'])/'skills')]))
            else:
                open_thread(r)
        elif method == 'skills/extraRoots/set':
            r.request('skills/list', dict(cwds=[r.config['cwd']], forceReload=True))
        elif method == 'skills/list':
            from pathlib import Path
            root = Path(r.config['resources_dir'])/'skills'
            found = {skill['name'] for group in result.get('data', []) for skill in group.get('skills', [])
                     if skill.get('enabled', True) and root in Path(skill.get('path', '')).parents}
            expected = {skill['name'] for skill in r.config['resources']['skills']}
            if not expected <= found:
                raise RuntimeError('native Codex did not discover configured skills: '+','.join(sorted(expected-found)))
            open_thread(r)
        elif method in ('thread/start', 'thread/resume'):
            r.thread = result['thread']['id']
            r.native_ready(f)
            r.drain()
        elif method == 'turn/start':
            r.turn = result['turn']['id']
            r.starting = False
            r.log('execution.started', evidence='rpc_response', native=f)
            r.delivered(item, 'started_turn', f, turn=r.turn, evidence='rpc_response')
            if getattr(r, 'cancel_starting', False):
                r.cancel_starting = False
                cancel(r)
        elif method == 'turn/interrupt':
            r.log('control.response', operation='interrupt', native=f)
        elif method == 'turn/steer':
            turn = result.get('turnId')
            if not turn or turn != item.get('expected_turn'):
                r.dropped(item, 'missing or mismatched turnId in steer response')
            else:
                r.delivered(item, 'injected', f, turn=turn, evidence='rpc_response')
        return
    p = f.get('params', {})
    if method == 'error':
        r.log('native.error', level='error', message=p.get('error', {}).get('message', 'backend error'), native=f)
        return
    if p.get('threadId') and p['threadId'] != r.thread:
        return
    if method == 'turn/completed':
        turn = p.get('turn', {})
        if r.turn and turn.get('id') != r.turn:
            return
        status = turn.get('status', 'failed')
        if status not in ('completed', 'interrupted', 'failed'):
            status = 'failed'
        r.finished(status, f, error=(turn.get('error') or {}).get('message'))
    elif method == 'item/agentMessage/delta':
        r.chunk(p.get('delta', ''), p.get('itemId'))
    elif method in ('item/started', 'item/completed'):
        item = p.get('item', {})
        if item.get('type') == 'agentMessage' and method == 'item/completed':
            r.message(item.get('text', ''), item.get('id'))
        elif item.get('type') in ('commandExecution','fileChange','mcpToolCall','webSearch'):
            from events import tool_status
            r.log('tool.started' if method == 'item/started' else 'tool.finished',
                   turn=p.get('turnId'), tool=item.get('id'), title=item.get('command',item.get('type')),
                   status=tool_status(item.get('status'), 'running' if method == 'item/started' else 'unknown'), native=f)
        elif item.get('type') == 'userMessage':
            r.log('input.observed', native=f)
    elif method == 'turn/plan/updated':
        from events import plan_steps
        r.log('plan', steps=plan_steps(p.get('plan', [])), native=f)
    elif method == 'thread/tokenUsage/updated':
        raw = p.get('tokenUsage')
        # total is cumulative across the session; never bill it again as this turn.
        counts = raw.get('last') if raw else None
        r.usage(None if counts is None else dict(input_tokens=counts.get('inputTokens',0), output_tokens=counts.get('outputTokens',0),cache_read_tokens=counts.get('cachedInputTokens',0),cache_write_tokens=counts.get('cacheWriteInputTokens',0)))
