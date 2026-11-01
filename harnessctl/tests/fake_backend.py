#!/usr/bin/env python3
"""Deterministic native protocol fixture, never calls a model."""
import json
import os
import sys
import time

if '--version' in sys.argv:
    print('fake 1.0.0')
    raise SystemExit
claude = os.path.basename(sys.argv[0]) == 'claude'
turn = None
count = 0
initialized = False


def emit(value):
    print(json.dumps(value), flush=True)


def finish(status='completed'):
    global turn
    if claude:
        emit(dict(type='result', subtype='success', is_error=False, result='OK', usage={'input_tokens': 2}))
    else:
        emit(dict(method='turn/completed', params=dict(threadId='native-session', turn=dict(id=turn, status=status))))
    turn = None


for line in sys.stdin:
    f = json.loads(line)
    if claude:
        if f.get('type') == 'control_request':
            emit(dict(type='control_response', response=dict(subtype='success', request_id=f['request_id'], response={})))
            if f['request']['subtype'] == 'interrupt' and turn:
                finish('interrupted')
        elif f.get('type') == 'control_response':
            emit(dict(type='assistant', message=dict(content=[dict(type='text', text='answered')])))
            finish()
        elif f.get('type') == 'user':
            if not initialized:
                emit(dict(type='system', subtype='init', session_id='native-session'))
                initialized = True
            emit(f)  # replay confirms receipt, not consumption
            text = f['message']['content']
            turn = turn or 'active'
            if text == 'ask':
                emit(dict(type='control_request', request_id='question-1', request=dict(subtype='can_use_tool', tool_name='AskUserQuestion', input=dict(questions=[]))))
            elif text == 'question':
                emit(dict(type='control_request', request_id='approval-1', request=dict(subtype='can_use_tool', tool_name='Bash', input=dict(command='echo OK'))))
            elif text not in ('hold', 'starting_hold'):
                emit(dict(type='assistant', message=dict(content=[dict(type='text', text=text)])))
                finish()
    else:
        method = f.get('method')
        if method == 'initialize':
            emit(dict(id=f['id'], result={}))
        elif method in ('thread/start','thread/resume'):
            emit(dict(id=f['id'], result=dict(thread=dict(id='native-session'))))
        elif method == 'turn/start':
            if f['params']['input'][0]['text'] == 'starting_hold':
                time.sleep(.4)
            count += 1
            turn = 'turn-' + str(count)
            emit(dict(id=f['id'], result=dict(turn=dict(id=turn))))
            text = f['params']['input'][0]['text']
            if text == 'ask':
                emit(dict(id='question-1',method='item/tool/requestUserInput',params=dict(threadId='native-session',turnId=turn,questions=[])))
            elif text == 'question':
                emit(dict(id='approval-1',method='item/commandExecution/requestApproval',params=dict(threadId='native-session',turnId=turn,command='echo OK')))
            elif text not in ('hold', 'starting_hold'):
                emit(dict(method='item/completed', params=dict(threadId='native-session',turnId=turn,item=dict(type='agentMessage',text=text))))
                finish()
        elif method == 'turn/steer':
            if f['params']['input'][0]['text'] == 'race':
                finish()
                emit(dict(id=f['id'], error=dict(code=-32600,message='no active turn to steer')))
            else:
                emit(dict(id=f['id'], result=dict(turnId=turn)))
        elif method == 'turn/interrupt':
            emit(dict(id=f['id'],result={}))
            if turn:
                finish('interrupted')
        elif 'result' in f:
            finish()
