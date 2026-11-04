"""Opt-in real model calls through the installed, authenticated native CLIs.

HARNESSCTL_REAL_LLM=1 python3 -m unittest discover -s harnessctl/tests -p test_real_resources.py -v
Uses native authentication/provider configuration unchanged. Never prints credentials.
"""
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(os.environ.get('HARNESSCTL_REAL_LLM') == '1', 'set HARNESSCTL_REAL_LLM=1 to call real models')
class RealResourceTests(unittest.TestCase):
    def test_codex_resources_and_native_resume(self):
        self.exercise('codex', 'codex')

    def test_claude_resources_and_native_resume(self):
        self.exercise('claudecode', 'claude')

    def exercise(self, adapter, binary):
        if not shutil.which(binary):
            self.skipTest(binary+' is not installed')
        root = Path(tempfile.mkdtemp(prefix='harnessctl-live-'+adapter+'-'))
        workspace = root/'workspace'; workspace.mkdir()
        (root/'sessions').mkdir()
        event = root/'sessions/events.jsonl'
        executable = ROOT/'harness'/adapter/'harnessctl'
        self.addCleanup(self.stop, event)
        witness = root/'executed.txt'
        nonce = uuid.uuid4().hex
        header = 'HEADER_'+nonce
        agents = root/'AGENTS.md'
        agents.write_text('For every final response in this session, begin with '+header+'.\n'
                          'When a verification skill is requested, execute its Python script; do not guess its output.\n'
                          'Do not create or edit files in the working directory.\n')
        skills, tokens = [], []
        for suffix in ('alpha', 'beta'):
            collection = root/('source-'+suffix)
            skill = collection/('hctl-probe-'+suffix)
            (skill/'scripts').mkdir(parents=True)
            token = suffix.upper()+'_'+nonce; tokens.append(token)
            (skill/'SKILL.md').write_text('---\nname: hctl-probe-'+suffix+'\n'
                'description: Run the external '+suffix+' verification script and report its token.\n---\n'
                'Execute `python3 scripts/probe.py`, resolving scripts/probe.py relative to THIS SKILL.md directory, '
                'not the current working directory. Report the script output in your final answer.\n')
            (skill/'scripts/probe.py').write_text('from pathlib import Path\n'
                'with Path('+repr(str(witness))+').open("a") as f: f.write('+repr(token+'\n')+')\n'
                'print('+repr(token)+')\n')
            skills.append(collection)
        args = ['init', str(event), '--cwd', str(workspace), '--agents-md', str(agents)]
        for directory in skills:
            args += ['--skills-dir', str(directory)]
        initialized = subprocess.run([str(executable)]+args, capture_output=True, text=True, timeout=30)
        self.assertEqual(initialized.returncode, 0, initialized.stderr)
        self.assertEqual(event.read_text(), '')
        self.assertFalse((Path(str(event)+'.d')/'process.json').exists())
        # Delete originals BEFORE first send: execution must consume only frozen resources.
        agents.unlink()
        for collection in skills:
            shutil.rmtree(collection)
        names = ['$hctl-probe-alpha', '$hctl-probe-beta'] if adapter == 'codex' else ['harnessctl:hctl-probe-alpha', 'harnessctl:hctl-probe-beta']
        prompt = ('Use both '+', '.join(names)+' skills. Execute the scripts each skill specifies. '
                  'Return the two actual script outputs, preceded by the header required by your standing instructions. '
                  'Do not modify or create files in the current working directory.')
        for cycle in range(2):
            result = subprocess.run([str(executable), 'send', str(event)], input=prompt,
                                    capture_output=True, text=True, timeout=180)
            deadline = time.monotonic()+180
            completed = []
            while time.monotonic() < deadline:
                rows = [json.loads(line) for line in event.read_text().splitlines()]
                completed = [row for row in rows if row['type'] == 'result']
                if len(completed) >= cycle+1:
                    break
                time.sleep(.2)
            self.assertEqual(len(completed), cycle+1, 'model did not finish; diagnostics: '+str(root)+'; send: '+result.stderr)
            terminal = completed[-1]
            self.assertEqual(terminal['status'], 'completed', terminal.get('error'))
            for expected in [header]+tokens:
                self.assertIn(expected, terminal['output'])
            executed = witness.read_text().splitlines()
            for token in tokens:
                self.assertGreaterEqual(executed.count(token), cycle+1, 'skill script was not executed again on resume')
            self.assertEqual(list(workspace.iterdir()), [], 'resources or tools polluted the shared cwd')
            native = json.loads((Path(str(event)+'.d')/'native.json').read_text())['resume']
            if cycle == 0:
                original_native = native
                self.stop(event)
            else:
                self.assertEqual(native, original_native, 'adapter started a new native conversation')
        report = dict(adapter=adapter, native_resume_verified=True, cycles=2,
                      runs=[r['run_id'] for r in completed], source_resources_deleted=True,
                      workspace_clean=True, script_executions=len(witness.read_text().splitlines()))
        (root/'verification.json').write_text(json.dumps(report, indent=2)+'\n')
        print('\nReal resource verification: '+str(root/'verification.json'), flush=True)

    @staticmethod
    def stop(event):
        directory = Path(str(event)+'.d')
        path = directory/'process.json'
        if not path.exists():
            return
        process = json.loads(path.read_text())
        try:
            os.kill(process['pid'], signal.SIGTERM)
        except ProcessLookupError:
            return
        import fcntl
        deadline = time.monotonic()+10
        while time.monotonic()<deadline:
            with (directory/'runtime.lock').open('a') as lock:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    return
                except BlockingIOError:
                    pass
            time.sleep(.05)
        raise RuntimeError('native runtime did not stop: '+str(event))
