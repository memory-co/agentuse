"""Real Claude CLI and a local Messages API: no model calls or credentials."""
import http.server
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(os.environ.get('HARNESSCTL_REAL_CLAUDE') == '1' and shutil.which('claude'),
                     'set HARNESSCTL_REAL_CLAUDE=1 to exercise the installed Claude CLI')
class RealClaudeSteerTests(unittest.TestCase):
    def test_busy_input_is_consumed_before_one_native_result(self):
        self.exercise(False)

    def test_cancel_remains_available_while_send_waits_for_consumption(self):
        self.exercise(True)

    def exercise(self, cancel):
        with tempfile.TemporaryDirectory(prefix='claude-steer-') as tmp:
            root = Path(tmp)
            gate = root/'release'
            calls = []

            class Handler(http.server.BaseHTTPRequestHandler):
                def log_message(self, *args):
                    pass

                def do_POST(self):
                    body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                    self.send_response(200)
                    if '/messages' not in self.path or 'count_tokens' in self.path:
                        self.end_headers()
                        self.wfile.write(b'{"input_tokens":10}')
                        return
                    calls.append(body)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.end_headers()
                    tool = len(calls) == 1
                    seen = 'STEER_MARKER' in json.dumps(body.get('messages', []))
                    block = dict(type='tool_use', id='tool_probe', name='Bash', input={}) if tool else dict(type='text', text='')
                    delta = dict(type='input_json_delta', partial_json=json.dumps(dict(command=f'while [ ! -f {gate} ]; do sleep .05; done', description='Wait for test gate'))) if tool else dict(type='text_delta', text='STEER_SEEN' if seen else 'NOT_SEEN')
                    events = [
                        ('message_start', dict(message=dict(id='msg_'+str(len(calls)), type='message', role='assistant', model='claude-sonnet-4-5', content=[], stop_reason=None, stop_sequence=None, usage=dict(input_tokens=10, output_tokens=0)))),
                        ('content_block_start', dict(index=0, content_block=block)),
                        ('content_block_delta', dict(index=0, delta=delta)),
                        ('content_block_stop', dict(index=0)),
                        ('message_delta', dict(delta=dict(stop_reason='tool_use' if tool else 'end_turn', stop_sequence=None), usage=dict(output_tokens=20))),
                        ('message_stop', {}),
                    ]
                    for typ, data in events:
                        data['type'] = typ
                        self.wfile.write(('event: '+typ+'\ndata: '+json.dumps(data)+'\n\n').encode())
                        self.wfile.flush()

            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            binary = root/'bin'
            binary.mkdir()
            wrapper = binary/'claude'
            wrapper.write_text('#!/bin/sh\nexec "'+shutil.which('claude')+'" --setting-sources "" "$@"\n')
            wrapper.chmod(0o700)
            env = dict(os.environ, PATH=str(binary)+':'+os.environ['PATH'], ANTHROPIC_BASE_URL=f'http://127.0.0.1:{server.server_port}', ANTHROPIC_API_KEY='local-test-only', ANTHROPIC_MODEL='claude-sonnet-4-5', CLAUDE_CONFIG_DIR=str(root/'config'), CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1')
            adapter = ROOT/'harness/claudecode/harnessctl'
            event = root/'events.jsonl'

            def command(verb, *args):
                return subprocess.run([str(adapter), verb, str(event), *args], env=env, cwd=root, capture_output=True, text=True, timeout=30)

            def read():
                return [json.loads(line) for line in event.read_text().splitlines()] if event.exists() else []

            def wait(predicate):
                deadline = time.monotonic()+20
                while time.monotonic() < deadline:
                    rows = read()
                    if predicate(rows):
                        return rows
                    time.sleep(.03)
                self.fail('missing native evidence: '+str(read()))

            pending = None
            try:
                initialized = command('init', '--cwd', str(root))
                self.assertEqual(initialized.returncode, 0, initialized.stderr)
                initial = command('send', 'Run the tool, then answer.')
                self.assertEqual(initial.returncode, 0, initial.stderr)
                wait(lambda rows: any(e['type']=='log' and e['topic']=='tool.started' for e in rows))
                pending = subprocess.Popen([str(adapter), 'send', str(event), 'STEER_MARKER focus on the additional instruction'], env=env, cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                first_id = next(e['input_id'] for e in read() if e['type']=='accepted')
                wait(lambda rows: any(e['type']=='log' and e['topic']=='input.lifecycle' and e['data']['state']=='queued' and e['data']['input_id']!=first_id for e in rows))
                # Public status must remain available while send awaits consumption.
                status = command('status')
                self.assertEqual(status.returncode, 0, status.stderr)
                self.assertEqual(json.loads(status.stdout)['state'], 'running')
                self.assertIsNone(pending.poll())
                if cancel:
                    cancelled = command('cancel')
                    self.assertEqual(cancelled.returncode, 0, cancelled.stderr)
                    pending.communicate(timeout=10)
                    self.assertNotEqual(pending.returncode, 0)
                    rows = wait(lambda rows: any(e['type']=='result' and e['status']=='cancelled' for e in rows))
                    self.assertEqual(len([e for e in rows if e['type']=='accepted']), 1)
                    self.assertEqual(len([e for e in rows if e['type']=='result']), 1)
                    self.assertEqual(len(calls), 1)
                    return
                gate.touch()
                _, stderr = pending.communicate(timeout=20)
                self.assertEqual(pending.returncode, 0, stderr)
                rows = wait(lambda rows: any(e['type']=='result' for e in rows))
                accepted = [e for e in rows if e['type']=='accepted']
                results = [e for e in rows if e['type']=='result']
                self.assertEqual(len(accepted), 2)
                self.assertEqual(len(results), 1)
                result = results[0]
                self.assertEqual({e['run_id'] for e in accepted}, {result['run_id']})
                self.assertEqual(set(result['input_ids']), {e['input_id'] for e in accepted})
                self.assertIn('STEER_SEEN', result['output'])
                self.assertEqual(len(calls), 2)
                self.assertIn('STEER_MARKER', json.dumps(calls[1]['messages']))
            finally:
                gate.touch()
                if pending and pending.poll() is None:
                    pending.terminate()
                    pending.wait(timeout=5)
                process = Path(str(event)+'.d/process.json')
                if process.exists():
                    try:
                        os.kill(json.loads(process.read_text())['pid'], signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                server.shutdown()
                server.server_close()
