"""Codex app-server JSON-RPC adapter."""
NAME = 'codex'
BINARY = 'codex'


def argv(config):
    args = [BINARY, 'app-server', '--listen', 'stdio://']
    if config['model']:
        import json
        args += ['-c', 'model=' + json.dumps(config['model'])]
    return args + config['backend_args']


def initialize(r):
    r.request('initialize', dict(clientInfo=dict(name='harnessctl', version='0.1.0'),
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
    import json
    native = request['native']
    if request['kind'] == 'approval':
        if answer not in ('allow', 'deny'):
            from runtime import Rejected
            raise Rejected('approval answer must be allow or deny', 2)
        result = dict(decision='accept' if answer == 'allow' else 'decline')
    else:
        result = json.loads(answer)
    r.write(dict(id=native['id'], result=result))


def before_finished(r, native):
    pass


def frame(r, f):
    method = f.get('method')
    if method and 'id' in f:
        key = str(f['id'])
        # Only known approval methods receive allow/deny translation. Unknown
        # requests remain raw questions, never implicitly approved.
        approval = method in ('item/commandExecution/requestApproval', 'item/fileChange/requestApproval')
        request = dict(native=f, kind='approval' if approval else 'question')
        r.requests[key] = request
        r.emit('request.opened', request=key, kind=request['kind'], summary=method,
               payload=f.get('params', {}), native=f)
        return
    if 'id' in f:
        pending = r.pending.pop(f['id'], None)
        if pending is None:
            return
        method, item, _ = pending
        if f.get('error'):
            reason = f['error'].get('message', 'native request failed')
            if method == 'turn/steer':
                r.emit('input.rejected', input=item['id'], reason=reason, native=f)
                # Retry as a new turn only for the documented idle race, never
                # for auth errors or arbitrary backend failures.
                if 'no active turn' in reason.lower() or 'expected active turn' in reason.lower():
                    if item['if_idle'] == 'start':
                        r.queued.append(item)
                        r.emit('input.queued', input=item['id'], reason='steer_race')
                        r.drain()
                    else:
                        r.dropped(item, 'no_active_turn')
                else:
                    r.dropped(item, reason)
            elif method == 'turn/start':
                r.dropped(item, reason)
                r.finished('failed', f)
            elif method == 'turn/interrupt':
                r.emit('error', message=reason, native=f)
            else:
                raise RuntimeError(method + ': ' + reason)
            return
        result = f.get('result', {})
        if method == 'initialize':
            r.write(dict(method='initialized', params={}))
            params = dict(cwd=r.config['cwd'])
            resume = r.config.get('resume')
            if resume:
                params['threadId'] = resume
            r.request('thread/resume' if resume else 'thread/start', params)
        elif method in ('thread/start', 'thread/resume'):
            r.thread = result['thread']['id']
            r.emit('session.started', adapter=NAME, native_session=r.thread, native=f)
            r.drain()
        elif method == 'turn/start':
            r.turn = result['turn']['id']
            r.starting = False
            r.emit('turn.started', turn=r.turn, native=f)
            r.delivered(item, 'started_turn', f, turn=r.turn, evidence='rpc_response')
            if getattr(r, 'cancel_starting', False):
                r.cancel_starting = False
                cancel(r)
        elif method == 'turn/interrupt':
            r.emit('control.response', operation='interrupt', native=f)
        elif method == 'turn/steer':
            turn = result.get('turnId')
            if not turn or turn != item.get('expected_turn'):
                r.dropped(item, 'missing or mismatched turnId in steer response')
            else:
                r.delivered(item, 'injected', f, turn=turn, evidence='rpc_response')
        return
    p = f.get('params', {})
    if method == 'error':
        r.emit('error', message=p.get('error', {}).get('message', 'backend error'), native=f)
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
        r.finished(status, f)
    elif method == 'item/agentMessage/delta':
        r.emit('message.delta', turn=p.get('turnId'), text=p.get('delta', ''), native=f)
    elif method in ('item/started', 'item/completed'):
        item = p.get('item', {})
        if item.get('type') == 'agentMessage' and method == 'item/completed':
            r.emit('message', turn=p.get('turnId'), text=item.get('text', ''), native=f)
        elif item.get('type') in ('commandExecution','fileChange','mcpToolCall','webSearch'):
            r.emit('tool.started' if method == 'item/started' else 'tool.finished',
                   turn=p.get('turnId'), tool=item.get('id'), title=item.get('command',item.get('type')),
                   status=item.get('status'), native=f)
        elif item.get('type') == 'userMessage':
            r.emit('input.observed', turn=p.get('turnId'), native=f)
    elif method == 'turn/plan/updated':
        r.emit('plan', turn=p.get('turnId'), steps=p.get('plan', []), native=f)
    elif method == 'thread/tokenUsage/updated':
        r.emit('usage', turn=p.get('turnId'), usage=p.get('tokenUsage'), native=f)
