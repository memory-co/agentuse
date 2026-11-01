"""Only each independent adapter directory is made available to the CLI."""
import concurrent.futures
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tarfile
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
ADAPTERS = ('codex', 'claudecode')
from test_events import load_contract
CONTRACTS = {adapter:load_contract(adapter) for adapter in ADAPTERS}


class ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='harnessctl-test-')
        self.root = Path(self.temp.name)
        self.dist = self.root/'distribution'
        for name in ADAPTERS:
            shutil.copytree(ROOT/'harness'/name, self.dist/name,
                            ignore=shutil.ignore_patterns('__pycache__'))
        binary = self.root/'bin'
        binary.mkdir()
        for name in ('codex', 'claude'):
            target = binary/name
            shutil.copy(ROOT/'tests/fake_backend.py', target)
            target.chmod(0o700)
        self.log = self.root/'native.jsonl'
        self.env = dict(os.environ, PATH=str(binary)+':'+os.environ['PATH'],
                        HARNESSCTL_TEST_LOG=str(self.log))
        self.sessions = {}

    def call(self, adapter, verb, event=None, *args, stdin=None):
        argv = [str(self.dist/adapter/'harnessctl'), verb]
        if event is not None:
            argv.append(str(event))
        return subprocess.run(argv+list(args), cwd=self.root, env=self.env,
                              input=stdin, capture_output=True, text=True, timeout=10)

    def events(self, event):
        items = [json.loads(line) for line in event.read_text().splitlines()] if event.exists() else []
        for item in items:
            for contract in CONTRACTS.values():
                contract.validate_event(item)
        return items

    def wait(self, event, event_type, **fields):
        deadline = time.monotonic()+8
        while time.monotonic()<deadline:
            for item in self.events(event):
                if item['type']==event_type and all(item.get(k)==v for k,v in fields.items()):
                    return item
            time.sleep(.02)
        self.fail(f'missing {event_type} {fields}: {self.events(event)}')

    def send(self, adapter, prompt='hold', *args, event=None, stdin=None):
        event = event or self.root/(adapter+'.jsonl')
        self.sessions[event] = adapter
        argv = list(args)
        if prompt is not None:
            argv.append(prompt)
        result = self.call(adapter, 'send', event, *argv, stdin=stdin)
        self.assertEqual((result.returncode,result.stdout,result.stderr),(0,'',''))
        return event

    def status(self, adapter, event):
        result = self.call(adapter,'status',event)
        self.assertEqual(result.returncode,0,result.stderr)
        return json.loads(result.stdout)

    def stop(self, event, abrupt=False):
        process = Path(str(event)+'.d/process.json')
        if not process.exists():
            return
        data = json.loads(process.read_text())
        # Private test cleanup simulates adapter-owned shutdown; no public close.
        try:
            os.kill(data['pid'],signal.SIGKILL if abrupt else signal.SIGTERM)
        except ProcessLookupError:
            return
        if abrupt:
            try: os.killpg(data['backend_pid'],signal.SIGTERM)
            except ProcessLookupError: pass
        lockpath = Path(str(event)+'.d/runtime.lock')
        import fcntl
        deadline = time.monotonic()+4
        while time.monotonic()<deadline:
            with lockpath.open('a') as lock:
                try:
                    fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                    return
                except BlockingIOError:
                    pass
            time.sleep(.02)
        self.fail('runtime did not stop')

    def tearDown(self):
        for event in self.sessions:
            self.stop(event)
        self.temp.cleanup()

    def test_four_commands_only(self):
        for adapter in ADAPTERS:
            result=self.call(adapter,'--help')
            self.assertEqual(result.returncode,0)
            self.assertIn('{send,cancel,status,reply}',result.stdout)
            for verb in ('start','doctor','close'):
                self.assertEqual(self.call(adapter,verb,self.root/'absent').returncode,2)
            for flag in ('--delivery','--if-idle','--resume','--id','--backend-arg'):
                self.assertEqual(self.call(adapter,'send',self.root/'absent',flag,'x','prompt').returncode,2)

    def test_first_send_stdin_continuation_and_binding(self):
        for adapter in ADAPTERS:
            text='literal $HOME `whoami` \\ "quote"\n中文\n'
            event=self.send(adapter,None,'--cwd',str(self.root),'--model','test-model',stdin=text)
            self.wait(event,'message',text=text)
            self.wait(event,'turn.finished',status='completed')
            state=self.status(adapter,event)
            self.assertEqual(state['state'],'idle')
            self.assertEqual(state['cwd'],str(self.root))
            self.assertEqual(state['model'],'test-model')
            self.assertNotIn('native_session',state)
            self.assertNotIn('pid',state)
            self.send(adapter,'second',event=event)
            self.wait(event,'message',text='second')
            other=self.root/'other';other.mkdir(exist_ok=True)
            for args in (('--cwd',str(other)),('--model','different')):
                self.assertEqual(self.call(adapter,'send',event,*args,'no').returncode,2)
            self.assertEqual(event.stat().st_mode&0o777,0o600)

    def test_automatic_busy_delivery_and_race(self):
        event=self.send('codex')
        self.wait(event,'input.delivered',outcome='started_turn')
        self.send('codex','supplement',event=event)
        self.wait(event,'input.delivered',outcome='injected')
        self.send('codex','race',event=event)
        self.wait(event,'message',text='race')
        other=self.send('claudecode')
        self.wait(other,'input.observed')
        self.send('claudecode','more',event=other)
        self.wait(other,'message',text='more')
        self.wait(other,'input.delivered',outcome='unknown')
        self.assertFalse(any(x.get('outcome')=='injected' for x in self.events(other)))

    def test_cancel_drops_waiting_input_and_can_send_again(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'question')
            self.wait(event,'request.opened')
            self.send(adapter,'must-not-run',event=event)
            result=self.call(adapter,'cancel',event)
            self.assertEqual((result.returncode,result.stdout),(0,''))
            self.wait(event,'turn.finished',status='interrupted')
            self.wait(event,'input.dropped',reason='cancelled')
            self.assertFalse(any(x.get('text')=='must-not-run' for x in self.events(event)))
            self.send(adapter,'after-cancel',event=event)
            self.wait(event,'message',text='after-cancel')

    def test_replies_normalize_permissions_and_questions(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'question')
            request=self.wait(event,'request.opened',kind='permission')
            key=request['request_id']
            self.assertEqual(self.status(adapter,event)['requests'][0]['request_id'],key)
            self.assertEqual(self.call(adapter,'reply',event,key,'bad').returncode,2)
            result=self.call(adapter,'reply',event,key,stdin='deny\n')
            self.assertEqual(result.returncode,0,result.stderr)
            self.wait(event,'request.replied',request_id=key)
            self.wait(event,'turn.finished',status='completed')
            self.assertEqual(self.call(adapter,'reply',event,key,'allow').returncode,4)
            self.send(adapter,'ask',event=event)
            question=self.wait(event,'request.opened',kind='question')
            self.assertEqual(question['questions'][0]['id'],'q1')
            self.assertEqual(question['questions'][0]['text'],'Which language?')
            key=question['request_id']
            self.assertEqual(self.call(adapter,'reply',event,key,'{"wrong":"Go"}').returncode,2)
            result=self.call(adapter,'reply',event,key,stdin='{"q1":"Go"}\n')
            self.assertEqual(result.returncode,0,result.stderr)
            self.wait(event,'request.replied',request_id=key)
            # Fixture asserts the exact, different native answer formats.
            self.wait(event,'message',text='answered') if adapter=='claudecode' else self.wait(event,'request.replied',request_id=key)

    def test_cancel_expires_reply(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'ask')
            key=self.wait(event,'request.opened')['request_id']
            self.assertEqual(self.call(adapter,'cancel',event).returncode,0)
            self.wait(event,'request.expired',request_id=key)
            self.assertEqual(self.call(adapter,'reply',event,key,'{"q1":"Go"}').returncode,4)

    def test_concurrent_first_sends_have_one_runtime(self):
        event=self.root/'concurrent.jsonl'
        self.sessions[event]='codex'
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results=list(pool.map(lambda _:self.call('codex','send',event,'hold'),range(8)))
        self.assertTrue(all(x.returncode==0 for x in results),[x.stderr for x in results])
        deadline=time.monotonic()+5
        while time.monotonic()<deadline:
            if len([x for x in self.events(event) if x['type']=='input.accepted'])==8:break
            time.sleep(.02)
        self.assertEqual(len([x for x in self.events(event) if x['type']=='input.accepted']),8)
        self.assertEqual(len([x for x in self.events(event) if x['type']=='session.ready']),1)

    def test_restart_resumes_without_public_native_ids(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'first')
            self.wait(event,'turn.finished',status='completed')
            self.stop(event)
            self.assertEqual(self.status(adapter,event)['state'],'idle')
            self.send(adapter,'resumed',event=event)
            self.wait(event,'message',text='resumed')
        frames=[json.loads(x) for x in self.log.read_text().splitlines()]
        self.assertTrue(any(x.get('frame',{}).get('method')=='thread/resume' for x in frames))
        self.assertTrue(any('--resume' in x.get('argv',[]) for x in frames))

    def test_loss_is_reported_once_and_next_send_resumes(self):
        event=self.send('codex')
        self.wait(event,'session.ready')
        self.stop(event,abrupt=True)
        for _ in range(2):self.assertEqual(self.status('codex',event)['state'],'failed')
        self.assertEqual(len([x for x in self.events(event) if x['type']=='runtime.stopped' and x['reason']=='lost']),1)
        self.send('codex','recovered',event=event)
        self.wait(event,'message',text='recovered')

    def test_orphan_backend_is_not_resumed_concurrently(self):
        self.env['HARNESSCTL_TEST_KEEP_ORPHAN']='1'
        event=self.send('codex')
        self.wait(event,'session.ready')
        process=json.loads(Path(str(event)+'.d/process.json').read_text())
        os.kill(process['pid'],signal.SIGKILL)
        try:
            self.assertEqual(self.status('codex',event)['state'],'failed')
            result=self.call('codex','send',event,'do-not-duplicate')
            self.assertEqual(result.returncode,1,result.stderr)
            self.assertIn('still running',result.stderr)
            self.assertEqual(self.call('codex','cancel',event).returncode,1)
        finally:
            try:os.killpg(process['backend_pid'],signal.SIGTERM)
            except ProcessLookupError:pass

    def test_missing_recovery_data_never_silently_starts_new_context(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'original')
            self.wait(event,'turn.finished',status='completed')
            self.stop(event)
            Path(str(event)+'.d/native.json').unlink()
            before=len(self.events(event))
            result=self.call(adapter,'send',event,'must-not-start')
            self.assertEqual(result.returncode,1,result.stderr)
            self.assertIn('unavailable',result.stderr)
            self.assertEqual(len(self.events(event)),before)

    def test_invalid_utf8_stdin_rejected(self):
        for adapter in ADAPTERS:
            result=subprocess.run([str(self.dist/adapter/'harnessctl'),'send',str(self.root/'bad-utf8')],
                                  input=b'\xff',capture_output=True,env=self.env,cwd=self.root,timeout=5)
            self.assertEqual(result.returncode,2,result.stderr)
            self.assertIn(b'UTF-8',result.stderr)

    def test_stdin_limits_wrong_adapter_and_existing_files(self):
        for adapter in ADAPTERS:
            for content in ('','   ','x'*(256*1024+1)):
                self.assertEqual(self.call(adapter,'send',self.root/'invalid',stdin=content).returncode,2)
        event=self.send('codex')
        self.assertEqual(self.call('claudecode','status',event).returncode,2)
        existing=self.root/'foreign.jsonl';existing.write_text('preserve\n')
        self.assertEqual(self.call('codex','send',existing,'no').returncode,2)
        self.assertEqual(existing.read_text(),'preserve\n')
        self.assertEqual(self.call('codex','status',self.root/'absent').returncode,4)

    def test_cancel_while_native_start_pending(self):
        gate=self.root/'native-start-gate'
        self.env['HARNESSCTL_TEST_STARTING_GATE']=str(gate)
        event=self.send('codex','starting_hold')
        deadline=time.monotonic()+3
        while not Path(str(gate)+'.started').exists() and time.monotonic()<deadline:time.sleep(.01)
        self.assertTrue(Path(str(gate)+'.started').exists())
        self.assertEqual(self.call('codex','cancel',event).returncode,0)
        gate.touch()
        self.wait(event,'turn.finished',status='interrupted')

    def test_idle_runtime_is_reclaimed_and_resumed(self):
        # Speed up the private policy in the isolated test copy, not a user knob.
        for adapter in ADAPTERS:
            source=self.dist/adapter/'lib/runtime.py'
            source.write_text(source.read_text().replace('IDLE_SECONDS = 60','IDLE_SECONDS = 0.3'))
            event=self.send(adapter,'idle-test')
            self.wait(event,'runtime.stopped',reason='idle')
            self.assertEqual(self.status(adapter,event)['state'],'idle')
            self.send(adapter,'again',event=event)
            self.wait(event,'message',text='again')

    def test_normalized_native_events_follow_the_contract(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'events')
            self.wait(event,'turn.finished',status='completed')
            self.wait(event,'tool.started',status='running')
            self.wait(event,'tool.finished',status='completed')
            self.wait(event,'message.delta',text='delta')
            self.wait(event,'usage')
            if adapter=='codex':
                self.wait(event,'plan',steps=[{'text':'Check','status':'in_progress'}])

    def test_bad_native_event_becomes_valid_error_not_invalid_jsonl(self):
        for adapter in ADAPTERS:
            event=self.send(adapter,'invalid-event')
            self.wait(event,'runtime.stopped',reason='error')
            self.wait(event,'error')
            self.assertFalse(any(x['type']=='message' for x in self.events(event)))

    def test_independent_reproducible_archives(self):
        spec=importlib.util.spec_from_file_location('package_tool',ROOT/'package.py')
        package=importlib.util.module_from_spec(spec);spec.loader.exec_module(package)
        unpacked=self.root/'unpacked';unpacked.mkdir()
        for adapter in ADAPTERS:
            archive=package.build(self.root/'build1',adapter)
            self.assertEqual(archive.read_bytes(),package.build(self.root/'build2',adapter).read_bytes())
            with tarfile.open(archive) as source:
                self.assertTrue(all(x.name.startswith(adapter+'/') for x in source.getmembers()))
                self.assertTrue({adapter+'/EVENT.md',adapter+'/event.schema.json',adapter+'/lib/events.py'}.issubset(source.getnames()))
                source.extractall(unpacked)
        shutil.rmtree(self.dist);self.dist=unpacked
        for adapter in ADAPTERS:
            sibling=self.dist/('claudecode' if adapter=='codex' else 'codex');hidden=self.root/'hidden'
            sibling.rename(hidden)
            try:
                event=self.send(adapter,'packaged')
                self.wait(event,'message',text='packaged')
                self.stop(event)
            finally:hidden.rename(sibling)


if __name__=='__main__':
    unittest.main()
