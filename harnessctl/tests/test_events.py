"""Five public event types, their durable semantics and independent packages."""
import ast
import copy
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[1]
ADAPTERS=('codex','claudecode')
TYPES={'accepted','message','request','result','log'}
SCHEMA=json.loads((ROOT/'event.schema.json').read_text())


def load_contract(adapter):
    spec=importlib.util.spec_from_file_location('contract_'+adapter,ROOT/'harness'/adapter/'lib/events.py')
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module


class EventContractTests(unittest.TestCase):
    def samples(self):
        return [json.loads(text) for text in re.findall(r'```json\n(.*?)\n```',(ROOT/'EVENT.md').read_text(),re.S)]

    def test_only_five_types_and_all_documentation_examples_validate(self):
        variants=[SCHEMA['$defs'][item['$ref'].split('/')[-1]] for item in SCHEMA['oneOf']]
        self.assertEqual({item['properties']['type']['const'] for item in variants},TYPES)
        self.assertEqual({sample['type'] for sample in self.samples()},TYPES)
        for adapter in ADAPTERS:
            contract=load_contract(adapter)
            for sample in self.samples():contract.validate_event(sample)
            for source in (ROOT/'harness'/adapter/'lib').glob('*.py'):
                for node in ast.walk(ast.parse(source.read_text())):
                    if isinstance(node,ast.Call) and isinstance(node.func,ast.Attribute) and node.func.attr=='emit':
                        if isinstance(node.args[0],ast.Constant):self.assertIn(node.args[0].value,TYPES)

    def test_independent_packages_include_identical_contracts(self):
        for adapter in ADAPTERS:
            package=ROOT/'harness'/adapter
            for name in ('EVENT.md','event.schema.json'):
                self.assertEqual((ROOT/name).read_bytes(),(package/name).read_bytes())
            self.assertEqual((package/'lib/events.py').read_bytes(),(ROOT/'harness/codex/lib/events.py').read_bytes())
            manifest=json.loads((package/'manifest.json').read_text())
            self.assertEqual(set(manifest['events']['types']),TYPES)

    def test_invalid_critical_events_are_rejected(self):
        good=dict(type='message',ts='2026-11-01T12:00:00Z',run_id='550e8400-e29b-41d4-a716-446655440000',message_id='m1',text='ok')
        result=dict(type='result',ts=good['ts'],run_id=good['run_id'],input_ids=['in1'],status='completed',output='ok',usage=None,cost_usd=None,error=None)
        invalid=[dict(good,type='message.delta'),dict(good,ts='yesterday'),dict(good,text=42),
                 dict(good,ts='2026-02-30T12:00:00Z'),dict(good,ts='2026-11-01T12:00:00+01:00'),
                 dict(good,native={}),dict(good,session_id='s1'),dict(good,message_id=''),
                 dict(result,input_ids=[]),dict(result,input_ids=['in1','in1']),
                 dict(result,status='failed'),dict(result,cost_usd=-1),dict(result,cost_usd=True),
                 dict(result,usage={'tokens':float('nan')}),dict(result,status='interrupted'),
                 dict(result,model=''),dict(result,model=42),dict(result,model=['model-a'])]
        no_id=copy.deepcopy(good);no_id.pop('message_id');invalid.append(no_id)
        for adapter in ADAPTERS:
            contract = load_contract(adapter)
            for model in (None, 'resolved-model'):
                contract.validate_event(dict(result, model=model))
            for event in invalid:
                with self.subTest(adapter=adapter,event=event):
                    with self.assertRaises(ValueError):load_contract(adapter).validate_event(event)

    def test_invalid_events_never_touch_the_file(self):
        script='''
import pathlib,sys
sys.path.insert(0,sys.argv[1])
from runtime import append_event
p=pathlib.Path(sys.argv[2]);before=p.read_bytes()
for kind,fields in [('message',{'message_id':'m1','text':42}),('error',{}),('result',{'status':'success'})]:
    try:append_event(p,kind,**fields)
    except ValueError:pass
    else:raise AssertionError('invalid event written')
    assert p.read_bytes()==before
append_event(p,'message',run_id='550e8400-e29b-41d4-a716-446655440000',message_id='m1',text='valid')
'''
        for adapter in ADAPTERS:
            with tempfile.TemporaryDirectory() as temp:
                event=Path(temp)/'test.jsonl';Path(str(event)+'.d').mkdir();event.write_text('')
                p=subprocess.run([sys.executable,'-c',script,str(ROOT/'harness'/adapter/'lib'),str(event)],capture_output=True,text=True,timeout=5)
                self.assertEqual(p.returncode,0,p.stderr)
                self.assertEqual(json.loads(event.read_text())['text'],'valid')

    def test_cancelled_unconfirmed_steer_cannot_create_a_new_run(self):
        script = """
import io,json,pathlib,sys,time
sys.path.insert(0,sys.argv[1])
import codex
from runtime import Runtime,identity
p=pathlib.Path(sys.argv[2]);pathlib.Path(str(p)+'.d').mkdir();p.touch()
r=Runtime(p,codex,{'cwd':str(p.parent),'model':None})
r.thread='native-thread';r.turn='native-turn';r.active_run_id=identity()
first={'id':identity(),'text':'initial'};late={'id':identity(),'text':'late'}
r.inputs={first['id']:first,late['id']:late};r.confirm_input(first,r.active_run_id);r.work_inputs=[first]
r.proc=type('Process',(),{'stdin':io.StringIO()})()
r.pending['late']=('turn/steer',late,time.monotonic())
r.command({'verb':'cancel'})
codex.frame(r,{'id':'late','error':{'message':'no active turn to steer'}})
r.finished('interrupted',None)
events=[json.loads(line) for line in p.read_text().splitlines()]
assert len([e for e in events if e['type']=='accepted'])==1
assert not r.queued
result=next(e for e in events if e['type']=='result')
assert result['status']=='cancelled' and result['input_ids']==[first['id']]
"""
        with tempfile.TemporaryDirectory() as temp:
            p=subprocess.run([sys.executable,'-c',script,str(ROOT/'harness/codex/lib'),str(Path(temp)/'events.jsonl')],capture_output=True,text=True,timeout=5)
            self.assertEqual(p.returncode,0,p.stderr)

    def test_claude_consumption_receipts_and_completion_races(self):
        script = """
import io,json,pathlib,sys
sys.path.insert(0,sys.argv[1])
import claudecode
from runtime import Runtime,identity
root=pathlib.Path(sys.argv[2])
for scenario in ('same','race','cancel','reject'):
    p=root/(scenario+'.jsonl');pathlib.Path(str(p)+'.d').mkdir();p.touch()
    r=Runtime(p,claudecode,{'cwd':str(root),'model':None})
    r.thread='native';r.turn='turn1';r.active_run_id=identity()
    r.claude_writes={};r.claude_interrupt=False
    r.proc=type('Process',(),{'stdin':io.StringIO()})()
    first={'id':identity(),'text':'initial','started_turn':r.turn}
    r.inputs[first['id']]=first;r.confirm_input(first,r.active_run_id);r.work_inputs=[first]
    r.claude_writes['first']=first;r.delivered(first,'started_turn')
    late=r.accept({'prompt':'supplement'})
    native=next(k for k,v in r.claude_writes.items() if v is late)
    claudecode.frame(r,{'type':'user','uuid':native,'message':{'content':'supplement'}})
    assert not late.get('accepted'), 'replay is not consumption'
    if scenario=='cancel':
        r.command({'verb':'cancel'})
        claudecode.frame(r,{'type':'command_lifecycle','command_uuid':native,'state':'started'})
        claudecode.frame(r,{'type':'result','user_message_uuids':['first',native],'result':'stopped'})
    elif scenario=='reject':
        claudecode.frame(r,{'type':'command_lifecycle','command_uuid':native,'state':'refused'})
        claudecode.frame(r,{'type':'result','user_message_uuids':['first'],'result':'initial only'})
    elif scenario=='race':
        claudecode.frame(r,{'type':'result','user_message_uuids':['first'],'result':'initial only'})
        assert not late.get('accepted')
        claudecode.frame(r,{'type':'command_lifecycle','command_uuid':native,'state':'started'})
        claudecode.frame(r,{'type':'result','user_message_uuids':[native],'result':'next interaction'})
    else:
        # A result's UUID list alone also proves consumption without replay or lifecycle.
        claudecode.frame(r,{'type':'result','user_message_uuids':['first',native],'result':'combined'})
    rows=[json.loads(line) for line in p.read_text().splitlines()]
    accepted=[e for e in rows if e['type']=='accepted'];results=[e for e in rows if e['type']=='result']
    if scenario=='same':
        assert len(accepted)==2 and len(results)==1
        assert accepted[0]['run_id']==accepted[1]['run_id']==results[0]['run_id']
        assert set(results[0]['input_ids'])=={first['id'],late['id']}
    elif scenario=='race':
        assert len(accepted)==2 and len(results)==2
        assert accepted[0]['run_id']!=accepted[1]['run_id']
        assert results[1]['input_ids']==[late['id']]
    else:
        assert len(accepted)==1 and len(results)==1
        assert results[0]['input_ids']==[first['id']]
        assert late['resolved']
"""
        with tempfile.TemporaryDirectory() as temp:
            p=subprocess.run([sys.executable,'-c',script,str(ROOT/'harness/claudecode/lib'),temp],capture_output=True,text=True,timeout=5)
            self.assertEqual(p.returncode,0,p.stderr)

    def test_claude_model_sources_do_not_confuse_child_or_synthetic_messages(self):
        script = """
import json,pathlib,sys
sys.path.insert(0,sys.argv[1])
import claudecode
from runtime import Runtime,identity
root=pathlib.Path(sys.argv[2])
for source,expected in [('stream','stream-model'),('usage','usage-model'),
                        ('ambiguous','session-model'),('synthetic','session-model'),('child','session-model')]:
    p=root/(source+'.jsonl');pathlib.Path(str(p)+'.d').mkdir();p.touch()
    r=Runtime(p,claudecode,{'cwd':str(root),'model':'requested-alias'})
    r.thread='native';r.turn='turn';r.active_run_id=identity()
    r.claude_writes={};r.claude_interrupt=False
    first={'id':identity(),'text':'initial'}
    r.inputs[first['id']]=first;r.confirm_input(first,r.active_run_id);r.work_inputs=[first]
    r.model('session-model',session=True)
    result={'type':'result','result':'done'}
    if source=='stream':
        claudecode.frame(r,{'type':'stream_event','event':{'type':'message_start','message':{'model':'stream-model'}}})
    elif source in ('usage','ambiguous'):
        result['modelUsage']={'usage-model':{}}
        if source=='ambiguous':result['modelUsage']['another-model']={}
    else:
        frame={'type':'assistant','message':{'model':'<synthetic>' if source=='synthetic' else 'child-model','content':[]}}
        if source=='child':frame['parent_tool_use_id']='child-tool'
        claudecode.frame(r,frame)
    claudecode.frame(r,result)
    rows=[json.loads(line) for line in p.read_text().splitlines()]
    assert next(e for e in rows if e['type']=='result')['model']==expected, source
"""
        with tempfile.TemporaryDirectory() as temp:
            p=subprocess.run([sys.executable,'-c',script,str(ROOT/'harness/claudecode/lib'),temp],capture_output=True,text=True,timeout=5)
            self.assertEqual(p.returncode,0,p.stderr)


if __name__=='__main__':unittest.main()
