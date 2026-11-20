"""Test ACP permission and cancellation over the standalone adapter entrypoint."""
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

FAKE_AGY = '''#!/usr/bin/env python3
import json
import sys

def send(**fields):
    print(json.dumps(dict(jsonrpc='2.0', **fields)), flush=True)

for line in sys.stdin:
    frame = json.loads(line)
    assert frame['jsonrpc'] == '2.0'
    method = frame.get('method')
    key = frame.get('id')
    if method == 'initialize':
        send(id=key, result=dict(protocolVersion=1))
    elif method == 'session/new':
        send(id=key, result=dict(sessionId='native-agy'))
    elif method == 'session/prompt':
        prompt = key
        send(id=777, method='session/request_permission', params=dict(
            sessionId='native-agy', toolCall=dict(title='Fixture permission'),
            options=[dict(optionId='once', kind='allow_once')]))
    elif key == 777:
        outcome = frame['result']['outcome']
        if outcome.get('outcome') == 'selected':
            assert outcome['optionId'] == 'once'
            send(method='session/update', params=dict(sessionId='native-agy',
                update=dict(sessionUpdate='agent_message_chunk', content=dict(type='text', text='approved'))))
            send(id=prompt, result=dict(stopReason='end_turn'))
    elif method == 'session/cancel':
        send(id=prompt, result=dict(stopReason='cancelled'))
'''


class AgyTests(unittest.TestCase):
    def test_permission_queue_and_cancel(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            pkg = directory/'adapter'
            shutil.copytree(ROOT/'harness/agy', pkg)
            fake = directory/'agy'
            fake.write_text(FAKE_AGY)
            fake.chmod(0o700)
            env = dict(os.environ, PATH=str(directory)+os.pathsep+os.environ['PATH'])
            event = directory/'events.jsonl'

            def call(verb, *args):
                return subprocess.run([pkg/'harnessctl', verb, event, *args], env=env,
                                      check=True, capture_output=True, text=True, timeout=8).stdout

            def wait(predicate):
                for _ in range(200):
                    rows = [json.loads(line) for line in event.read_text().splitlines() if line.endswith('}')]
                    matches = [row for row in rows if predicate(row)]
                    if matches:
                        return matches[-1]
                    time.sleep(.025)
                self.fail(f'Expected event not found: {rows}')

            call('init', '--cwd', temp)
            try:
                call('send', 'first')
                first = wait(lambda row: row['type']=='request' and row['state']=='pending')
                call('send', 'second')
                self.assertEqual(json.loads(call('status'))['queued'], 1)
                call('reply', first['request_id'], 'allow')
                done = wait(lambda row: row['type']=='result' and row['run_id']==first['run_id'])
                self.assertEqual((done['status'], done['output']), ('completed', 'approved'))
                second = wait(lambda row: row['type']=='request' and row['run_id']!=first['run_id'])
                call('send', 'queued for cancellation')
                queued = wait(lambda row: row['type']=='accepted' and row['text']=='queued for cancellation')
                call('cancel')
                for run_id in (second['run_id'], queued['run_id']):
                    row = wait(lambda row: row['type']=='result' and row['run_id']==run_id)
                    self.assertEqual(row['status'], 'cancelled')
            finally:
                test_real_resources.RealResourceTests.stop(event)
