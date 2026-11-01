"""Executable checks for EVENT.md, Schema, and independent writer contracts."""
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

ROOT = Path(__file__).resolve().parents[1]
ADAPTERS = ('codex','claudecode')
SCHEMA = json.loads((ROOT/'event.schema.json').read_text())
SAMPLES = [json.loads(text) for text in re.findall(r'```json\n(.*?)\n```', (ROOT/'EVENT.md').read_text(), re.S)]


def load_contract(adapter):
    path=ROOT/'harness'/adapter/'lib/events.py'
    spec=importlib.util.spec_from_file_location('contract_'+adapter,path)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module


class EventContractTests(unittest.TestCase):
    def test_documentation_and_schema_match_every_emitted_type(self):
        variants=[SCHEMA['$defs'][item['$ref'].split('/')[-1]] for item in SCHEMA['oneOf']]
        documented={item['properties']['type']['const'] for item in variants}
        self.assertEqual(len(documented),21)
        self.assertEqual({sample['type'] for sample in SAMPLES},documented)
        self.assertEqual({sample['kind'] for sample in SAMPLES if sample['type']=='request.opened'}, {'permission','question'})
        emitted=set()
        for adapter in ADAPTERS:
            for source in (ROOT/'harness'/adapter/'lib').glob('*.py'):
                tree=ast.parse(source.read_text())
                for node in ast.walk(tree):
                    if not isinstance(node,ast.Call):continue
                    if isinstance(node.func,ast.Attribute) and node.func.attr=='emit':
                        tag=node.args[0]
                    elif isinstance(node.func,ast.Name) and node.func.id=='append_event':
                        tag=node.args[1]
                    else:continue
                    if isinstance(tag,ast.Constant) and isinstance(tag.value,str):emitted.add(tag.value)
                    elif isinstance(tag,ast.IfExp):
                        emitted.update(child.value for child in (tag.body,tag.orelse) if isinstance(child,ast.Constant))
                    else:self.assertIsInstance(tag,ast.Name)  # append_event forwarding its event_type
        self.assertEqual(emitted,documented)
        for adapter in ADAPTERS:
            contract=load_contract(adapter)
            for sample in SAMPLES:
                with self.subTest(adapter=adapter,event=sample['type']):contract.validate_event(sample)

    def test_independent_packages_include_identical_contracts(self):
        for adapter in ADAPTERS:
            package=ROOT/'harness'/adapter
            for name in ('EVENT.md','event.schema.json'):
                self.assertEqual((ROOT/name).read_bytes(),(package/name).read_bytes())
            self.assertEqual((package/'lib/events.py').read_bytes(),(ROOT/'harness/codex/lib/events.py').read_bytes())
            manifest=json.loads((package/'manifest.json').read_text())
            self.assertEqual(manifest['events'],{'schema':'event.schema.json','documentation':'EVENT.md'})

    def test_invalid_events_are_rejected(self):
        valid=dict(type='message',ts='2026-11-01T12:00:00Z',text='ok')
        invalid=[
            dict(valid,type='undocumented'), dict(valid,ts='yesterday'),
            dict(valid,ts='2026-02-30T12:00:00Z'),dict(valid,ts='2026-11-01T12:00:00+01:00'),
            dict(valid,text=12),dict(valid,text=None),dict(valid,native=[]),
            dict(valid,turn='private-id'),dict(valid,session_id='extra-session'),
            dict(type='message',ts=valid['ts']),dict(valid,native={'value':float('nan')}),
            dict(type='turn.finished',ts=valid['ts'],status='success'),
            dict(type='usage',ts=valid['ts'],usage={},cost_usd=-1),
            dict(type='usage',ts=valid['ts'],usage={},cost_usd=True),
            dict(type='plan',ts=valid['ts'],steps=[{'text':'a','status':'inProgress'}]),
            dict(type='tool.started',ts=valid['ts'],tool='x',title='Bash',status='inProgress'),
            dict(type='request.opened',ts=valid['ts'],request_id='x',kind='question',message='q',questions=[]),
        ]
        question=copy.deepcopy(next(x for x in SAMPLES if x['type']=='request.opened' and x['kind']=='question'))
        question['questions'][0]['multiple']='yes';invalid.append(question)
        delivered=copy.deepcopy(next(x for x in SAMPLES if x['type']=='input.delivered'))
        delivered.pop('evidence');invalid.append(delivered)
        delivered=dict(type='input.delivered',ts=valid['ts'],input='in-1',outcome='injected',evidence='user_replay')
        invalid.append(delivered)
        for adapter in ADAPTERS:
            contract=load_contract(adapter)
            for event in invalid:
                with self.subTest(adapter=adapter,event=event):
                    with self.assertRaises(ValueError):contract.validate_event(event)

    def test_schema_errors_do_not_touch_the_jsonl_file(self):
        script='''
import pathlib,sys
sys.path.insert(0,sys.argv[1])
from runtime import append_event
path=pathlib.Path(sys.argv[2]);before=path.read_bytes()
for kind,fields in [('message',{'text':42}),('unknown',{}),('turn.finished',{'status':'success'})]:
    try:append_event(path,kind,**fields)
    except ValueError:pass
    else:raise AssertionError('invalid event written')
    assert path.read_bytes()==before
append_event(path,'message',text='valid')
'''
        for adapter in ADAPTERS:
            with tempfile.TemporaryDirectory() as temp:
                event=Path(temp)/'test.jsonl';Path(str(event)+'.d').mkdir()
                event.write_text(json.dumps(SAMPLES[0])+'\n')
                result=subprocess.run([sys.executable,'-c',script,str(ROOT/'harness'/adapter/'lib'),str(event)],
                                      capture_output=True,text=True,timeout=5)
                self.assertEqual(result.returncode,0,result.stderr)
                items=[json.loads(line) for line in event.read_text().splitlines()]
                self.assertEqual(len(items),2)
                self.assertEqual(items[-1]['text'],'valid')


if __name__=='__main__':unittest.main()
