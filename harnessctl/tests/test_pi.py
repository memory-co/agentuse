"""Exercise native run boundaries and dialogue replies through the standalone Pi adapter."""
from contextlib import ExitStack
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

import test_real_resources

ROOT = Path(__file__).resolve().parents[1]

FAKE_PI = '''#!/usr/bin/env python3
import json
from pathlib import Path
import sys

def emit(kind, **fields):
    print(json.dumps(dict(type=kind, **fields)), flush=True)

def respond(frame, **data):
    emit('response', id=frame['id'], command=frame['type'], success=True, data=data)

def assistant(text, stop='stop'):
    message = dict(role='assistant', model='actual-pi-model',
                   content=[dict(type='text', text=text)], stopReason=stop,
                   usage=dict(input=10, output=2, cacheRead=5, cacheWrite=1))
    emit('message_start', message=message)
    emit('message_update', assistantMessageEvent=dict(type='text_delta', delta=text))
    emit('message_end', message=message)

if sys.argv[1:] == ['--version']:
    print('1.6.0')
    sys.exit(0)

args = sys.argv[1:]
assert args[args.index('--mode')+1] == 'rpc'
directory = Path(args[args.index('--session-dir')+1])
session = directory/'fixture.jsonl'
if '--session' in args:
    assert Path(args[args.index('--session')+1]) == session
    assert session.read_text() == 'fixture context'

for line in sys.stdin:
    frame = json.loads(line)
    kind = frame['type']
    if kind == 'get_state':
        respond(frame, sessionId='pi-fixture', sessionFile=str(session), model={'id':'default'})
    elif kind == 'prompt':
        if frame['message'] == 'handled':
            respond(frame, disposition='handled')
            continue
        directory.mkdir(exist_ok=True)
        session.write_text('fixture context')
        respond(frame, disposition='started')
        if frame['message'] == 'first':
            assistant('intermediate', 'toolUse')
            emit('agent_end', messages=[], willRetry=True)
            emit('extension_ui_request', id='select-1', method='select',
                 title='Choose a value', options=['choice A','choice B'])
        elif frame['message'] == 'second':
            emit('extension_ui_request', id='editor-2', method='editor', title='Enter text')
        elif frame['message'] == 'resumed':
            assert '--session' in args
            assistant('native context restored')
            emit('agent_settled', aborted=False)
    elif kind == 'extension_ui_response' and frame['id'] == 'select-1':
        assert frame['value'] == 'choice B'
        assistant('selected choice B')
        emit('agent_settled', aborted=False)
    elif kind == 'abort':
        command = frame
    elif kind == 'extension_ui_response' and frame['id'] == 'editor-2':
        assert frame['cancelled']
        emit('agent_settled', aborted=True)
        respond(command)
'''


class PiTests(unittest.TestCase):
    def test_settled_queue_reply_cancel_and_resume(self):
        with tempfile.TemporaryDirectory() as temp, ExitStack() as cleanup:
            directory = Path(temp)
            package = directory/'adapter'
            shutil.copytree(ROOT/'harness/pi', package)
            binary = directory/'pi'
            binary.write_text(FAKE_PI)
            binary.chmod(0o700)
            event = directory/'events.jsonl'
            env = dict(os.environ, PATH=str(directory)+os.pathsep+os.environ['PATH'])
            cleanup.callback(test_real_resources.RealResourceTests.stop, event)

            def call(verb, *args):
                result = subprocess.run([package/'harnessctl', verb, event, *args], env=env,
                                        capture_output=True, text=True, timeout=8)
                self.assertEqual(result.returncode, 0, result.stderr)
                return result.stdout

            def read():
                return [json.loads(line) for line in event.read_text().splitlines() if line.endswith('}')]

            def wait(event_type, **fields):
                for _ in range(200):
                    rows = read()
                    for row in rows:
                        if row['type'] == event_type and all(row.get(key) == value for key, value in fields.items()):
                            return row
                    time.sleep(.025)
                self.fail(f'Missing {event_type} event for {fields}: {rows}')

            call('init', '--cwd', temp)
            call('send', 'first')
            first = wait('accepted', text='first')
            request = wait('request', kind='question', state='pending', run_id=first['run_id'])
            call('send', 'second')
            second = wait('accepted', text='second')
            self.assertNotEqual(first['run_id'], second['run_id'])
            self.assertEqual(json.loads(call('status'))['queued'], 1)
            self.assertFalse(any(row['type'] == 'result' for row in read()))

            call('reply', request['request_id'], '{"value":"choice B"}')
            wait('request', state='replied', run_id=first['run_id'])
            result = wait('result', run_id=first['run_id'])
            self.assertEqual(result['status'], 'completed')
            self.assertEqual(result['output'], 'intermediate\n\nselected choice B')
            self.assertEqual(result['model'], 'actual-pi-model')
            self.assertEqual(result['usage'], dict(input_tokens=32, output_tokens=4,
                                                   cache_read_tokens=10, cache_write_tokens=2))
            wait('request', state='pending', run_id=second['run_id'])
            call('cancel')
            self.assertEqual(wait('result', run_id=second['run_id'])['status'], 'cancelled')

            test_real_resources.RealResourceTests.stop(event)
            call('send', 'resumed')
            result = wait('result', run_id=wait('accepted', text='resumed')['run_id'])
            self.assertEqual((result['status'], result['output']), ('completed', 'native context restored'))
            call('send', 'handled')
            result = wait('result', run_id=wait('accepted', text='handled')['run_id'])
            self.assertEqual(result['status'], 'completed')
