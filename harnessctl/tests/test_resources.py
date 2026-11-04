"""Deterministic initialization, immutable resources and recovery coverage."""
import concurrent.futures
import json
from pathlib import Path
import shutil
import subprocess
import time
import unittest

import test_protocol


class ResourceTests(unittest.TestCase):
    def setUp(self):
        self.h = test_protocol.ProtocolTests()
        self.h.setUp()
        self.root = self.h.root
        self.cwd = self.root/'workspace'; self.cwd.mkdir()
        self.agents = self.root/'AGENTS.md'; self.agents.write_text('fixed instructions\n')
        self.skills = self.root/'source-skills'
        self.skill = self.skills/'review'; (self.skill/'scripts').mkdir(parents=True)
        (self.skill/'SKILL.md').write_text('---\nname: review\ndescription: >-\n  Review changes\n  and tests.\n---\nUse scripts/tool.sh.\n')
        script = self.skill/'scripts/tool.sh';script.write_text('#!/bin/sh\necho frozen\n');script.chmod(0o755)

    def tearDown(self):
        self.h.tearDown()

    def initialize(self, adapter, event=None, *args):
        event = event or self.root/(adapter+'.jsonl')
        self.h.sessions[event] = adapter
        result = self.h.call(adapter,'init',event,'--cwd',str(self.cwd),*args)
        self.assertEqual(result.returncode,0,result.stderr)
        return event

    def test_init_is_required_and_does_not_start_backend(self):
        for adapter in test_protocol.ADAPTERS:
            event=self.root/(adapter+'.jsonl')
            self.assertEqual(self.h.call(adapter,'send',event,'hello').returncode,4)
            self.assertFalse(event.exists())
            self.initialize(adapter,event,'--model','test-model')
            self.assertEqual(event.read_text(),'')
            self.assertFalse(self.h.log.exists())
            state=self.h.status(adapter,event)
            self.assertEqual((state['state'],state['run_id'],state['requests']),('idle',None,[]))
            self.assertEqual(state['model'],'test-model')
            self.assertEqual(self.h.call(adapter,'send',event,'--cwd',str(self.cwd),'hello').returncode,2)
            self.assertEqual(self.h.call(adapter,'init',event,'--model','other').returncode,2)

    def test_concurrent_initialization_and_send_commit_one_binding(self):
        event=self.root/'concurrent.jsonl'
        def init(_):return self.h.call('codex','init',event,'--cwd',str(self.cwd))
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results=list(pool.map(init,range(8)))
        self.h.sessions[event]='codex'
        self.assertTrue(all(r.returncode==0 for r in results),[r.stderr for r in results])
        self.assertEqual(event.read_text(),'')
        config=json.loads(Path(str(event)+'.d/config.json').read_text())
        self.assertEqual(config['cwd'],str(self.cwd))
        self.h.send('codex','hello',event=event)
        self.h.wait(event,'result',status='completed')
        before=event.read_text()
        self.assertEqual(init(1).returncode,0)
        self.assertEqual(event.read_text(),before)


    def test_send_waits_for_complete_initialization(self):
        adapter='codex';event=self.root/'during-init.jsonl';self.h.sessions[event]=adapter
        gate=self.root/'init-gate'
        self.h.env['HARNESSCTL_TEST_INIT_GATE']=str(gate)
        module=self.h.dist/adapter/'lib/resources.py'
        code=module.read_text().replace('def prepare(args, destination, cwd, events):',
            "def prepare(args, destination, cwd, events):\n    import time\n    gate=Path(os.environ['HARNESSCTL_TEST_INIT_GATE'])\n    gate.with_suffix('.started').touch()\n    while not gate.exists(): time.sleep(.01)")
        module.write_text(code)
        command=str(self.h.dist/adapter/'harnessctl')
        initial=subprocess.Popen([command,'init',str(event),'--cwd',str(self.cwd)],env=self.h.env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        pending=None
        try:
            deadline=time.monotonic()+5
            while not gate.with_suffix('.started').exists() and time.monotonic()<deadline:time.sleep(.01)
            self.assertTrue(gate.with_suffix('.started').exists())
            pending=subprocess.Popen([command,'send',str(event),'hello'],env=self.h.env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            time.sleep(.1)
            self.assertIsNone(pending.poll())
            self.assertFalse(event.exists())
            gate.touch()
            _,error=initial.communicate(timeout=5);self.assertEqual(initial.returncode,0,error)
            _,error=pending.communicate(timeout=10);self.assertEqual(pending.returncode,0,error)
            self.h.wait(event,'result',status='completed')
        finally:
            gate.touch()
            initial.communicate(timeout=5)
            if pending:pending.communicate(timeout=10)

    def test_interrupted_initialization_recovers_only_owned_empty_history(self):
        for adapter in test_protocol.ADAPTERS:
            event=self.root/(adapter+'.jsonl');directory=Path(str(event)+'.d');directory.mkdir()
            marker=directory/'initializing.json';marker.write_text(json.dumps(dict(stage='.init-interrupted')))
            (directory/'.init-interrupted').mkdir();(directory/'resources').mkdir();event.touch()
            self.initialize(adapter,event)
            self.assertFalse(marker.exists());self.assertFalse((directory/'.init-interrupted').exists())
            foreign=self.root/(adapter+'-foreign.jsonl');foreign.touch()
            result=self.h.call(adapter,'init',foreign,'--cwd',str(self.cwd))
            self.assertEqual(result.returncode,2,result.stderr)
            self.assertTrue(foreign.exists())

    def test_snapshot_survives_sources_deleted_and_native_resume(self):
        for adapter in test_protocol.ADAPTERS:
            # Both adapters share the same workspace and the same external sources.
            event=self.initialize(adapter,None,'--agents-md',str(self.agents),'--skills-dir',str(self.skills))
            self.assertEqual(list(self.cwd.iterdir()),[])
            frozen=Path(str(event)+'.d/resources')
            self.assertEqual((frozen/'agents.md').read_text(),'fixed instructions\n')
            self.assertEqual((frozen/'skills/review/scripts/tool.sh').stat().st_mode&0o777,0o700)
            self.assertEqual(self.h.call(adapter,'init',event,'--agents-md',str(self.agents),'--skills-dir',str(self.skills)).returncode,0)
        self.agents.unlink();shutil.rmtree(self.skills)
        for adapter in test_protocol.ADAPTERS:
            event=self.root/(adapter+'.jsonl')
            self.assertEqual(self.h.call(adapter,'init',event).returncode,0)
            self.h.send(adapter,'first',event=event);self.h.wait(event,'result',status='completed')
            self.h.stop(event)
            self.h.send(adapter,'second',event=event);self.h.wait(event,'message',text='second')
            self.assertEqual(list(self.cwd.iterdir()),[])
        frames=[json.loads(line) for line in self.h.log.read_text().splitlines()]
        codex=[f['frame'] for f in frames if f.get('frame',{}).get('method') in ('thread/start','thread/resume')]
        self.assertEqual(len(codex),2)
        self.assertTrue(all(f['params']['developerInstructions']=='fixed instructions\n' for f in codex))
        claude=[f['argv'] for f in frames if 'argv' in f and Path(f['argv'][0]).name=='claude']
        self.assertEqual(len(claude),2)
        self.assertTrue(all('--append-system-prompt-file' in argv and '--plugin-dir' in argv for argv in claude))

    def test_invalid_resources_roll_back_and_retry(self):
        for adapter in test_protocol.ADAPTERS:
            event=self.root/(adapter+'.jsonl')
            bad=self.h.call(adapter,'init',event,'--cwd',str(self.cwd),'--agents-md',str(self.root/'missing'))
            self.assertEqual(bad.returncode,2,bad.stderr)
            self.assertFalse(event.exists())
            self.assertFalse(Path(str(event)+'.d/config.json').exists())
            self.initialize(adapter,event,'--agents-md',str(self.agents))
            self.agents.write_text('changed')
            conflict=self.h.call(adapter,'init',event,'--agents-md',str(self.agents))
            self.assertEqual(conflict.returncode,2,conflict.stderr)
            self.assertEqual(Path(str(event)+'.d/resources/agents.md').read_text(),'fixed instructions\n')
            self.agents.write_text('fixed instructions\n')

    def test_duplicate_skills_symlink_escape_and_cwd_resources_rejected(self):
        duplicate=self.root/'other-skills';shutil.copytree(self.skills,duplicate)
        for adapter in test_protocol.ADAPTERS:
            event=self.root/(adapter+'.jsonl')
            for flags in [('--skills-dir',str(self.skills),'--skills-dir',str(duplicate))]:
                result=self.h.call(adapter,'init',event,'--cwd',str(self.cwd),*flags)
                self.assertEqual(result.returncode,2,result.stderr)
                self.assertFalse(event.exists())
            (self.skill/'escape').symlink_to(self.agents)
            result=self.h.call(adapter,'init',event,'--cwd',str(self.cwd),'--skills-dir',str(self.skills))
            self.assertEqual(result.returncode,2,result.stderr)
            (self.skill/'escape').unlink()
            result=self.h.call(adapter,'init',self.cwd/'bad.jsonl','--cwd',str(self.cwd),'--agents-md',str(self.agents))
            self.assertEqual(result.returncode,2,result.stderr)

    def test_corrupt_snapshot_is_not_silently_reloaded(self):
        for adapter in test_protocol.ADAPTERS:
            event=self.initialize(adapter,None,'--agents-md',str(self.agents))
            Path(str(event)+'.d/resources/agents.md').write_text('tampered')
            result=self.h.call(adapter,'send',event,'hello')
            self.assertNotEqual(result.returncode,0)
            self.assertEqual(event.read_text(),'')
            self.assertFalse(self.h.log.exists())

    def test_corrupt_resources_do_not_prevent_cancel_or_status(self):
        for adapter in test_protocol.ADAPTERS:
            event=self.initialize(adapter,None,'--agents-md',str(self.agents))
            self.h.send(adapter,'hold',event=event)
            self.h.wait(event,'accepted')
            Path(str(event)+'.d/resources/agents.md').write_text('changed externally')
            self.assertEqual(self.h.status(adapter,event)['state'],'running')
            cancelled=self.h.call(adapter,'cancel',event)
            self.assertEqual(cancelled.returncode,0,cancelled.stderr)
            self.h.wait(event,'result',status='cancelled')
