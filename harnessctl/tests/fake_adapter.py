#!/usr/bin/env python3
"""Protocol fixture: each input is a queued interaction executed by a legacy test worker.
Only for tests; production packages have their own runtime and strict event writer.
"""
import fcntl, json, os, pathlib, signal, subprocess, sys, time, uuid

def records(path):
    try: return [json.loads(x) for x in pathlib.Path(path).read_text().splitlines()]
    except FileNotFoundError: return []

def append(path, event):
    with open(path, 'a') as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        f.write(json.dumps(event, ensure_ascii=False)+'\n'); f.flush(); os.fsync(f.fileno())

def main():
    backend, verb, path, *args = sys.argv[1:]
    root = pathlib.Path(path+'.fixture'); root.mkdir(parents=True, exist_ok=True)
    pathlib.Path(path).parent.mkdir(parents=True, exist_ok=True)
    def event(kind, run, **fields): append(path, dict(type=kind, run_id=run, **fields))
    if verb == 'init':
        cwd = args[args.index('--cwd')+1] if '--cwd' in args else os.getcwd()
        (root/'config.json').write_text(json.dumps(dict(cwd=cwd)))
        pathlib.Path(path).touch(exist_ok=True)
        return
    if verb == 'send':
        prompt = sys.stdin.read(); run = str(uuid.uuid4()); iid = str(uuid.uuid4())
        cwd = json.loads((root/'config.json').read_text())['cwd']
        event('accepted', run, input_id=iid, text=prompt)
        p = subprocess.Popen([sys.executable, __file__, backend, '_worker', path, run, iid, cwd], stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        (root/(run+'.pid')).write_text(str(p.pid))
        p.stdin.write(prompt.encode()); p.stdin.close()
        print('{}'); return
    if verb == '_worker':
        run, iid, cwd = args; prompt = sys.stdin.read(); os.environ['AGENTUSE_RUN']=run
        lock = open(root/'queue.lock','w'); fcntl.flock(lock, fcntl.LOCK_EX)
        instruction, sep, task = prompt.partition('\n\n# 本次任务\n'); task = task if sep else prompt
        import re
        match = re.search(r'先读取 (.+?history.json)。',instruction)
        saved = root/'history-path'
        if match: saved.write_text(match[1])
        resources = os.environ.get('AGENTUSE_RESOURCES', '')
        history_path = pathlib.Path(match[1]) if match else pathlib.Path(saved.read_text()) if saved.exists() else pathlib.Path(resources, 'history.json')
        history = []
        if resources or match or saved.exists():
            try:
                data = json.loads(history_path.read_text())
                history = data.get('messages', []) if isinstance(data, dict) else data
            except FileNotFoundError: pass
        context_marker='共享对话历史（JSON 数据，按当前消息处理）：\n'
        if context_marker in instruction:
            history=json.loads(instruction.split(context_marker,1)[1].strip())
        attachments = []
        marker = '本轮输入附件（JSON 数据，本地 path 相对当前目录）：\n'
        if marker in instruction:
            attachments = json.loads(instruction.split(marker)[1].splitlines()[0])
        import re
        found = re.search('用户消息 id 为 (\\[[^\\n]*?\\])', instruction)
        envelope = dict(endpoint_id=os.environ.get('AGENTUSE_ENDPOINT',''),conversation_id=os.environ.get('AGENTUSE_CONVERSATION',''),input=dict(prompt=task), run_id=run, agent_id=os.environ.get('AGENTUSE_AGENT',''), session_id=os.environ.get('AGENTUSE_SESSION',''), resume='', session_messages=history, task_message_ids=json.loads(found[1]) if found else [], attachments=attachments, instructions=instruction)
        if task == 'multi:hold': event('message',run,message_id=str(uuid.uuid4()),text='working on '+task)
        proc = subprocess.Popen([backend], cwd=cwd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        stdout, stderr = proc.communicate(json.dumps(envelope)+'\n')
        result = None
        for line in stdout.splitlines():
            try: row = json.loads(line)
            except ValueError: continue
            if row.get('type') == 'result': result = row
            elif row.get('type') == 'progress': event('message',run,message_id=str(uuid.uuid4()),text=row.get('text','working'))
        ok = proc.returncode == 0 and result and result.get('ok')
        event('result',run,input_ids=[iid],status='completed' if ok else 'failed',output=(result or {}).get('output',''),usage=(result or {}).get('usage'),cost_usd=(result or {}).get('cost_usd'),error=None if ok else stderr or 'fixture failed')
        (root/(run+'.pid')).unlink(missing_ok=True)
        return
    rows = records(path); accepted = {r['run_id']:r for r in rows if r['type']=='accepted'}; done = {r['run_id'] for r in rows if r['type']=='result'}
    if verb == 'cancel':
        for run in accepted.keys()-done:
            try: os.killpg(int((root/(run+'.pid')).read_text()),signal.SIGKILL)
            except (FileNotFoundError,ProcessLookupError): pass
            event('result',run,input_ids=[accepted[run]['input_id']],status='cancelled',output='',usage=None,cost_usd=None,error=None)
        print('{}')
    elif verb == 'status': print(json.dumps(dict(state='running' if accepted.keys()-done else 'idle')))
    else: raise SystemExit('unsupported fixture command')
if __name__=='__main__': main()
