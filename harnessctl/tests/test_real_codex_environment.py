"""HARNESSCTL_REAL_LLM=1 python3 -m unittest discover -s harnessctl/tests -p test_real_codex_environment.py -v"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest
import uuid

import test_real_resources

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(os.environ.get('HARNESSCTL_REAL_LLM') == '1', 'set HARNESSCTL_REAL_LLM=1 to call real models')
class RealCodexEnvironmentTests(unittest.TestCase):
    def test_session_command_and_binding_survive_native_resume(self):
        if not shutil.which('codex'):
            self.skipTest('codex is not installed')
        with tempfile.TemporaryDirectory(prefix='harnessctl-shell-') as directory:
            root = Path(directory)
            workspace = root/'workspace'; workspace.mkdir()
            binary = root/'session-bin'; binary.mkdir()
            witness = root/'calls.jsonl'
            binding = 'binding-'+uuid.uuid4().hex
            entry = binary/'context'
            entry.write_text('#!/usr/bin/env python3\nimport json, os, sys\n'
                'record = {"binding": os.environ["AGENTUSE_CONTEXT"], "args": sys.argv[1:]}\n'
                'with open('+repr(str(witness))+', "a") as f: f.write(json.dumps(record)+"\\n")\n'
                'print(json.dumps(record))\n')
            entry.chmod(0o700)
            env = dict(os.environ, PATH=str(binary)+os.pathsep+os.environ['PATH'], AGENTUSE_CONTEXT=binding)
            events = root/'events.jsonl'
            ctl = ROOT/'harness/codex/harnessctl'
            initialized = subprocess.run([str(ctl), 'init', str(events), '--cwd', str(workspace)],
                                         env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(initialized.returncode, 0, initialized.stderr)
            native = None
            try:
                for cycle in range(2):
                    sent = subprocess.run([str(ctl), 'send', str(events)], env=env,
                        input='Execute the shell command `context conversation agents` exactly once. '
                              'Use the command name from PATH, without searching for or substituting an absolute path. '
                              'Report its actual output. Do not modify files.',
                        text=True, capture_output=True, timeout=30)
                    self.assertEqual(sent.returncode, 0, sent.stderr)
                    deadline = time.monotonic()+120
                    while time.monotonic() < deadline:
                        rows = [json.loads(line) for line in events.read_text().splitlines()]
                        results = [r for r in rows if r['type']=='result']
                        if len(results) >= cycle+1:
                            break
                        time.sleep(.2)
                    self.assertEqual(len(results), cycle+1, 'model did not finish')
                    self.assertEqual(results[-1]['status'], 'completed', results[-1])
                    calls = [json.loads(line) for line in witness.read_text().splitlines()] if witness.exists() else []
                    self.assertEqual(calls, [{'binding': binding, 'args': ['conversation', 'agents']}]*(cycle+1))
                    self.assertIn(binding, results[-1]['output'])
                    state = json.loads(Path(str(events)+'.d/native.json').read_text())
                    if native is None:
                        native = state
                    else:
                        self.assertEqual(state, native, 'resume created a different native thread')
                    test_real_resources.RealResourceTests.stop(events)
            finally:
                test_real_resources.RealResourceTests.stop(events)
