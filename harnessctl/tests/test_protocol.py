"""Run from any directory: python3 -m unittest discover -s harnessctl/tests -v."""
import concurrent.futures
import json
import importlib.util
import signal
import tarfile
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='harnessctl-test-')
        self.root = Path(self.temp.name)
        # Copy only individual adapter directories, with no shared parent lib,
        # protocol source tree, package builder, or AgentUse modules available.
        self.dist = self.root / 'distribution'
        for adapter in ('codex', 'claudecode'):
            shutil.copytree(ROOT / 'harness' / adapter, self.dist / adapter,
                            ignore=shutil.ignore_patterns('__pycache__'))
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        for name in ('codex', 'claude'):
            target = self.bin / name
            shutil.copy(ROOT / 'tests/fake_backend.py', target)
            target.chmod(0o700)
        self.env = dict(os.environ, PATH=str(self.bin) + ':' + os.environ['PATH'])
        self.sessions = []

    def tearDown(self):
        for adapter, event in self.sessions:
            self.call(adapter, 'close', event)
        for _, event in self.sessions:
            try:
                self.wait(event, 'session.exited')
            except AssertionError:
                pass
        self.temp.cleanup()

    def call(self, adapter, verb, event=None, *args):
        argv = [str(self.dist / adapter / 'harnessctl'), verb]
        if event is not None:
            argv.append(str(event))
        return subprocess.run(argv + list(args), cwd=self.root, env=self.env,
                              capture_output=True, text=True, timeout=10)

    def events(self, event):
        return [json.loads(line) for line in event.read_text().splitlines()] if event.exists() else []

    def wait(self, event, event_type, **fields):
        end = time.monotonic() + 8
        while time.monotonic() < end:
            for e in self.events(event):
                if e['type'] == event_type and all(e.get(k) == v for k, v in fields.items()):
                    return e
            time.sleep(.025)
        self.fail(f'missing {event_type} {fields}: {self.events(event)}')

    def start(self, adapter, prompt='hold'):
        event = self.root / (adapter + '.jsonl')
        result = self.call(adapter, 'start', event, '--cwd', str(self.root), '--id', 'initial', prompt)
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, '', ''))
        self.sessions.append((adapter, event))
        self.wait(event, 'session.started')
        self.wait(event, 'turn.started')
        return event

    def test_both_adapters_lifecycle_and_independent_distribution(self):
        for adapter in ('codex','claudecode'):
            with self.subTest(adapter=adapter):
                doctor = self.call(adapter, 'doctor')
                self.assertEqual(doctor.returncode, 0)
                self.assertTrue(json.loads(doctor.stdout)['ok'])
                event = self.start(adapter)
                result = self.call(adapter, 'send', event, '--delivery', 'queue', '--id', 'queued', 'next')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.wait(event, 'input.queued', input='queued')
                self.assertEqual(json.loads(self.call(adapter,'status',event).stdout)['queued'], 1)
                result = self.call(adapter, 'cancel', event)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.wait(event,'turn.finished',status='interrupted')
                self.wait(event,'input.delivered',input='queued',outcome='started_turn')
                self.wait(event,'turn.finished',status='completed')
                self.assertEqual(self.call(adapter,'send',event,'--if-idle','fail','no').returncode,5)
                self.assertEqual(self.call(adapter,'close',event).stdout,'')
                self.wait(event,'session.exited',reason='closed')
                self.assertEqual(json.loads(self.call(adapter,'status',event).stdout)['state'],'exited')
                self.assertEqual(self.call(adapter,'send',event,'late').returncode,4)
                self.assertEqual(event.stat().st_mode & 0o777,0o600)

    def test_codex_steer_receipt_dedup_and_race(self):
        event=self.start('codex')
        for _ in range(2):
            p=self.call('codex','send',event,'--id','steer-1','supplement')
            self.assertEqual(p.returncode,0,p.stderr)
        self.wait(event,'input.delivered',input='steer-1',outcome='injected')
        self.assertEqual(len([e for e in self.events(event) if e['type']=='input.accepted' and e['input']=='steer-1']),1)
        self.assertEqual(self.call('codex','send',event,'--id','steer-1','different').returncode,2)
        self.assertEqual(self.call('codex','send',event,'--id','race','race').returncode,0)
        self.wait(event,'input.delivered',input='race',outcome='started_turn')

    def test_claude_replay_does_not_claim_injection(self):
        event=self.start('claudecode')
        self.assertEqual(self.call('claudecode','send',event,'--id','steer-1','next').returncode,0)
        self.wait(event,'input.observed',input='steer-1')
        self.wait(event,'input.delivered',input='steer-1',outcome='unknown')
        self.assertFalse(any(e.get('outcome')=='injected' for e in self.events(event)))

    def test_approval_reply_and_drop_queue(self):
        for adapter in ('codex','claudecode'):
            event=self.start(adapter,'question')
            self.wait(event,'request.opened',request='approval-1',kind='approval')
            p=self.call(adapter,'reply',event,'approval-1','invalid')
            self.assertEqual(p.returncode,2,p.stderr)
            p=self.call(adapter,'reply',event,'approval-1','deny')
            self.assertEqual(p.returncode,0,p.stderr)
            self.wait(event,'request.replied',request='approval-1')
            self.wait(event,'turn.finished',status='completed')
            self.assertEqual(self.call(adapter,'reply',event,'approval-1','allow').returncode,4)
            self.assertEqual(self.call(adapter,'send',event,'hold').returncode,0)
            self.assertEqual(self.call(adapter,'send',event,'--delivery','queue','--id','drop','next').returncode,0)
            self.assertEqual(self.call(adapter,'cancel',event,'--drop-queued').returncode,0)
            self.wait(event,'input.dropped',input='drop',reason='cancelled')

    def test_concurrent_sends_and_wrong_adapter(self):
        event=self.start('codex')
        self.assertEqual(self.call('claudecode','status',event).returncode,2)
        self.assertEqual(self.call('codex','start',event,'oops').returncode,2)
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results=list(pool.map(lambda i:self.call('codex','send',event,'--id',f'in-{i}','more'),range(12)))
        self.assertTrue(all(p.returncode==0 for p in results))
        for i in range(12):
            self.wait(event,'input.delivered',input=f'in-{i}',outcome='injected')
        self.assertTrue(all('ts' in e for e in self.events(event)))

    def test_interrupt_starts_new_turn_and_resume_uses_new_address(self):
        for adapter in ('codex', 'claudecode'):
            event=self.start(adapter)
            p=self.call(adapter,'send',event,'--delivery','interrupt','--id','urgent','urgent')
            self.assertEqual(p.returncode,0,p.stderr)
            self.wait(event,'turn.finished',status='interrupted')
            self.wait(event,'input.delivered',input='urgent',outcome='started_turn')
            self.wait(event,'turn.finished',status='completed')
            self.call(adapter,'close',event)
            self.wait(event,'session.exited')
            resumed=self.root/(adapter+'-resumed.jsonl')
            p=self.call(adapter,'start',resumed,'--resume','native-session','continue')
            self.assertEqual(p.returncode,0,p.stderr)
            self.sessions.append((adapter,resumed))
            self.wait(resumed,'session.started',native_session='native-session')
            self.wait(resumed,'turn.finished',status='completed')

    def test_cancel_while_codex_start_response_is_pending(self):
        event=self.root/'starting.jsonl'
        gate=self.root/'native-start-gate'
        self.env['HARNESSCTL_TEST_STARTING_GATE']=str(gate)
        p=self.call('codex','start',event,'starting_hold')
        self.assertEqual(p.returncode,0,p.stderr)
        self.sessions.append(('codex',event))
        deadline=time.monotonic()+3
        while not Path(str(gate)+'.started').exists() and time.monotonic()<deadline:
            time.sleep(.01)
        self.assertTrue(Path(str(gate)+'.started').exists())
        self.assertEqual(self.call('codex','cancel',event).returncode,0)
        gate.touch()
        self.wait(event,'turn.finished',status='interrupted')
        self.wait(event,'control.response',operation='interrupt')

    def test_native_questions_and_expiration(self):
        for adapter in ('codex','claudecode'):
            event=self.start(adapter,'ask')
            self.wait(event,'request.opened',request='question-1',kind='question')
            self.assertEqual(self.call(adapter,'reply',event,'question-1','not-json').returncode,2)
            answer='{"answers":{"q1":{"answers":["yes"]}}}' if adapter=='codex' else '{"behavior":"allow","updatedInput":{"answers":{}}}'
            self.assertEqual(self.call(adapter,'reply',event,'question-1',answer).returncode,0)
            self.wait(event,'turn.finished',status='completed')
            # A new native request in the next turn may reuse the same native ID.
            self.assertEqual(self.call(adapter,'send',event,'question').returncode,0)
            self.wait(event,'request.opened',request='approval-1')
            self.assertEqual(self.call(adapter,'cancel',event).returncode,0)
            self.wait(event,'request.expired',request='approval-1')
            self.assertEqual(self.call(adapter,'reply',event,'approval-1','allow').returncode,4)

    def test_lost_runtime_recorded_once(self):
        event=self.start('codex')
        process=json.loads(Path(str(event)+'.d/process.json').read_text())
        os.kill(process['pid'],signal.SIGKILL)
        os.killpg(process['backend_pid'],signal.SIGTERM)
        end=time.monotonic()+3
        while time.monotonic()<end:
            result=self.call('codex','status',event)
            if result.returncode==0 and json.loads(result.stdout)['state']=='exited':break
            time.sleep(.025)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['state'],'exited')
        self.call('codex','status',event)
        self.assertEqual(len([e for e in self.events(event) if e['type']=='session.exited']),1)

    def test_reproducible_archives_are_independent(self):
        spec=importlib.util.spec_from_file_location('package_tool', ROOT/'package.py')
        package=importlib.util.module_from_spec(spec);spec.loader.exec_module(package)
        unpacked=self.root/'unpacked';unpacked.mkdir()
        for adapter in ('codex','claudecode'):
            archive=package.build(self.root/'build1', adapter)
            other=package.build(self.root/'build2', adapter)
            self.assertEqual(archive.read_bytes(),other.read_bytes())
            with tarfile.open(archive) as source:
                self.assertTrue(all(p.name.startswith(adapter+'/') for p in source.getmembers()))
                self.assertNotIn(adapter+'/lib/'+('claudecode' if adapter=='codex' else 'codex')+'.py',source.getnames())
                source.extractall(unpacked)
        shutil.rmtree(self.dist)
        self.dist=unpacked
        for adapter in ('codex','claudecode'):
            # Hide the other package: no sibling can supply runtime dependencies.
            sibling=self.dist/('claudecode' if adapter=='codex' else 'codex')
            hidden=self.root/'hidden-adapter'
            sibling.rename(hidden)
            try:
                event=self.start(adapter,'packaged')
                self.wait(event,'turn.finished',status='completed')
                self.call(adapter,'close',event)
                self.wait(event,'session.exited')
            finally:
                hidden.rename(sibling)


if __name__=='__main__':
    unittest.main()
