#!/usr/bin/env python3
"""Deterministic native protocol fixture, never calls a model."""
import json
import os
from pathlib import Path
import sys
import time
import threading

if '--version' in sys.argv:
    print('fake 1.0.0')
    raise SystemExit
log = os.environ.get('HARNESSCTL_TEST_LOG')
if log:
    with open(log, 'a') as output:
        output.write(json.dumps(dict(argv=sys.argv))+'\n')
claude = os.path.basename(sys.argv[0]) == 'claude'
turn = None
count = 0
initialized = False
last_output = ""


def emit(value):
    global last_output
    if value.get('type')=='assistant':
        texts=[b.get('text') for b in value.get('message',{}).get('content',[]) if b.get('type')=='text' and isinstance(b.get('text'),str)]
        if texts:last_output=''.join(texts)
    print(json.dumps(value), flush=True)


def finish(status='completed',output=None):
    global turn,last_output
    if claude:
        emit(dict(type='result', subtype='success', is_error=False, result=output if output is not None else last_output or 'OK', usage={'input_tokens': 2}))
    else:
        emit(dict(method='turn/completed', params=dict(threadId='native-session', turn=dict(id=turn, status=status))))
    turn = None
    last_output = ""


for line in sys.stdin:
    f = json.loads(line)
    if log:
        with open(log, 'a') as output:
            output.write(json.dumps(dict(frame=f))+'\n')
    if claude:
        if f.get('type') == 'control_request':
            emit(dict(type='control_response', response=dict(subtype='success', request_id=f['request_id'], response={})))
            if f['request']['subtype'] == 'interrupt' and turn:
                finish('interrupted')
        elif f.get('type') == 'control_response':
            if f.get('response',{}).get('response',{}).get('updatedInput',{}).get('answers') is not None:
                assert f['response']['response']['updatedInput']['answers'] == {'Which language?': 'Go'}
            emit(dict(type='assistant', message=dict(content=[dict(type='text', text='answered')])))
            finish()
        elif f.get('type') == 'user':
            if not initialized:
                emit(dict(type='system', subtype='init', session_id='native-session'))
                initialized = True
            emit(f)  # replay confirms receipt, not consumption
            text = f['message']['content']
            turn = turn or 'active'
            if text == 'gated':
                def gated():
                    gate=Path(os.environ['HARNESSCTL_TEST_RELEASE'])
                    while not gate.exists():time.sleep(.01)
                    emit(dict(type='assistant',message=dict(id='gated-message',content=[dict(type='text',text='gated done')])))
                    finish()
                threading.Thread(target=gated,daemon=True).start()
            elif text == 'ask':
                emit(dict(type='control_request', request_id='question-1', request=dict(subtype='can_use_tool', tool_name='AskUserQuestion', input=dict(questions=[dict(header='Language',question='Which language?',options=[dict(label='Go',description='Go language')],multiSelect=False)]))))
            elif text == 'invalid-event':
                emit(dict(type='assistant',message=dict(content=[dict(type='text',text=42)])))
            elif text == 'multi-text':
                emit(dict(type='assistant',message=dict(id='multi-1',content=[dict(type='text',text='first '),dict(type='text',text='second')])) )
                emit(dict(type='result',subtype='success',is_error=False,usage={'input_tokens':1}))
                turn=None
            elif text == 'stream-only':
                for part in ('stream',' complete'):
                    emit(dict(type='stream_event',event=dict(type='content_block_delta',delta=dict(type='text_delta',text=part))))
                finish(output='stream complete')
            elif text == 'events':
                emit(dict(type='stream_event',event=dict(type='content_block_delta',delta=dict(type='text_delta',text='delta'))))
                emit(dict(type='assistant',message=dict(content=[dict(type='tool_use',id='tool-1',name='Bash')])))
                emit(dict(type='user',message=dict(content=[dict(type='tool_result',tool_use_id='tool-1',is_error=False)])))
                finish(output='delta')
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
                gate = os.environ.get('HARNESSCTL_TEST_STARTING_GATE')
                if gate:
                    Path(gate + '.started').touch()
                    deadline = time.monotonic() + 5
                    while not Path(gate).exists() and time.monotonic() < deadline:
                        time.sleep(.01)
                else:
                    time.sleep(.4)
            count += 1
            turn = 'turn-' + str(count)
            emit(dict(id=f['id'], result=dict(turn=dict(id=turn))))
            text = f['params']['input'][0]['text']
            if text == 'ask':
                emit(dict(id='question-1',method='item/tool/requestUserInput',params=dict(threadId='native-session',turnId=turn,questions=[dict(id='q1',header='Language',question='Which language?',options=[dict(label='Go',description='Go language')])])))
            elif text == 'invalid-event':
                emit(dict(method='item/completed',params=dict(threadId='native-session',turnId=turn,item=dict(type='agentMessage',text=42))))
            elif text == 'stream-only':
                for part in ('stream',' complete'):
                    emit(dict(method='item/agentMessage/delta',params=dict(threadId='native-session',itemId='stream-1',delta=part)))
                finish()
            elif text == 'events':
                emit(dict(method='item/agentMessage/delta',params=dict(threadId='native-session',delta='delta')))
                emit(dict(method='item/started',params=dict(threadId='native-session',item=dict(type='commandExecution',id='tool-1',command='echo OK',status='inProgress'))))
                emit(dict(method='item/completed',params=dict(threadId='native-session',item=dict(type='commandExecution',id='tool-1',command='echo OK',status='completed'))))
                emit(dict(method='turn/plan/updated',params=dict(threadId='native-session',plan=[dict(step='Check',status='inProgress')])))
                emit(dict(method='thread/tokenUsage/updated',params=dict(threadId='native-session',tokenUsage={'total':{'inputTokens':2}})))
                finish()
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
            if 'answers' in f['result']:
                assert f['result']['answers'] == {'q1': {'answers':['Go']}}
            finish()

# Simulate a backend/tool that survives its owner's stdin closing.
if os.environ.get('HARNESSCTL_TEST_KEEP_ORPHAN'):
    while True:
        time.sleep(1)
