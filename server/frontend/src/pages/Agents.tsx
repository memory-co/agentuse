import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Command, CommandInput, CommandList, CommandItem, CommandEmpty } from '@/components/ui/command';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Agent, AgentVersion, Skill, SkillVersion, User, Device } from '@/lib/types';
import { t, useT } from '@/lib/i18n';
import { cn } from '@/lib/utils';

export const useAgents = () => useQuery({ queryKey: ['agents'], queryFn: () => api<Agent[]>('/api/agents'), staleTime: 30_000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['agents'] });

// 运行时 = 某台 device + 上面可用并已开启的某个 harness,下拉框的值写成 "device_id/harness"。
type Runtime = { device_id: string; harness: string };
const rtKey = (r: Runtime) => `${r.device_id}/${r.harness}`;
const parseRt = (k: string): Runtime => { const i = k.indexOf('/'); return { device_id: k.slice(0, i), harness: k.slice(i + 1) }; };
const runtimesOf = (devices: Device[] | undefined, userId: string) =>
  (devices || []).filter(d => d.user_id === userId).flatMap(d => d.harnesses.filter(h => h.available && h.enabled).map(h => ({
    key: rtKey({ device_id: d.id, harness: h.harness }),
    label: `${d.name} · ${h.harness}${d.status === 'online' ? '' : t('agents.offline')}`,
  })));

function RuntimeSelect({ value, onChange, devices, userId, className }: { value: string; onChange: (v: string) => void; devices?: Device[]; userId: string; className?: string }) {
  const t = useT();
  const options = runtimesOf(devices, userId);
  // 当前选的运行时已经不能用了(harness 被关掉或本机没了):照样显示出来，标成不可用，不能再选
  const stale = value && devices && !options.some(o => o.key === value) ? (() => {
    const rt = parseRt(value);
    return { key: value, label: `${devices.find(d => d.id === rt.device_id)?.name ?? rt.device_id} · ${rt.harness}${t('agents.unavailable')}` };
  })() : null;
  return <Select value={value} onValueChange={onChange}>
    <SelectTrigger className={cn(className, stale && 'text-destructive')} title={stale ? t('agents.unavailableHint') : undefined}><SelectValue placeholder={t('agents.pickRuntime')} /></SelectTrigger>
    <SelectContent>
      {stale && <SelectItem value={stale.key} disabled>{stale.label}</SelectItem>}
      {options.map(o => <SelectItem key={o.key} value={o.key}>{o.label}</SelectItem>)}
      {!options.length && <p className="px-2 py-1.5 text-sm text-muted-foreground">{t('agents.noRuntimes')}</p>}
    </SelectContent>
  </Select>;
}


export function Agents({ me }: { me: User }) {
 const t = useT();
 const agents = useAgents();
 const devices = useQuery({queryKey:['devices'],queryFn:()=>api<Device[]>('/api/devices')});
 const skills = useQuery({queryKey:['skills'],queryFn:()=>api<Skill[]>('/api/skills')});
 const versions = useQuery({queryKey:['skill-versions',skills.data?.map(s=>s.current_version_id)],enabled:!!skills.data,queryFn:async()=> (await Promise.all((skills.data||[]).map(s=>api<SkillVersion[]>(`/api/skills/${s.id}/versions`)))).flat()});
 const empty = {id:'',name:'',description:'',runtime:'',model:'',instructions:'',skills:[] as string[]};
 const [form,setForm] = useState(empty);
 const [editing,setEditing] = useState<Agent|null>(null);
 const [historyID,setHistoryID] = useState('');
 const history = useQuery({queryKey:['agent-versions',historyID],enabled:!!historyID,queryFn:()=>api<AgentVersion[]>(`/api/agents/${historyID}/versions`)});
 const label = (zh:string,en:string)=> t('nav.language')==='English'?zh:en;
 function load(a:Agent,v?:AgentVersion){setEditing(a);setForm({id:a.id,name:v?.name??a.name,description:v?.description??a.description,runtime:rtKey({device_id:v?.device_id??a.device_id??'',harness:v?.harness??a.harness}),model:v?.model??a.model??'',instructions:v?.instructions??a.instructions??'',skills:(v?.skills??a.skills??[]).map(s=>s.skill_version_id)});}
 const save = useMutation({mutationFn:()=>{
  const selected=form.skills.map(id=>{const v=versions.data?.find(v=>v.id===id);if(!v) {const old=editing?.skills.find(s=>s.skill_version_id===id);if(old)return old;throw new Error('Unknown skill version');}return {skill_id:v.skill_id,skill_version_id:v.id};});
  const body={name:form.name.trim(),description:form.description,...parseRt(form.runtime),model:form.model.trim(),instructions:form.instructions,skills:selected};
  return editing?api(`/api/agents/${editing.id}/versions`,{method:'POST',body:{base_version_id:editing.current_version_id,...body}}):api('/api/agents',{method:'POST',body:{id:form.id.trim(),...body}});
 },onSuccess:()=>{setForm(empty);setEditing(null);void refresh();void queryClient.invalidateQueries({queryKey:['agent-versions']});}});
 const del=useMutation({mutationFn:(id:string)=>api(`/api/agents/${id}`,{method:'DELETE'}),onSuccess:refresh});
 return <div className="grid gap-4">
  <Card><CardHeader><CardTitle>{editing?label('发布 agent 新版本','Publish agent version'):t('agents.newTitle')}</CardTitle><CardDescription>{t('agents.newText')}</CardDescription></CardHeader><CardContent>
  <form className="grid gap-3" onSubmit={e=>{e.preventDefault();save.mutate();}}>
   <div className="flex flex-wrap gap-2"><Input className="w-40" aria-label="Agent ID" placeholder={t('agents.idPlaceholder')} value={form.id} disabled={!!editing} onChange={e=>setForm({...form,id:e.target.value})}/><Input className="w-48" aria-label={t('common.name')} placeholder={t('agents.namePlaceholder')} required value={form.name} onChange={e=>setForm({...form,name:e.target.value})}/><RuntimeSelect className="w-64" value={form.runtime} onChange={runtime=>setForm({...form,runtime})} devices={devices.data} userId={editing?.user_id??me.id}/></div>
   <Input aria-label={t('common.description')} placeholder={t('common.description')} value={form.description} onChange={e=>setForm({...form,description:e.target.value})}/>
   <Input aria-label="Model" placeholder={label('Model（可选）','Model (optional)')} value={form.model} onChange={e=>setForm({...form,model:e.target.value})}/>
   <Textarea aria-label="AGENTS.md" placeholder="AGENTS.md" rows={6} value={form.instructions} onChange={e=>setForm({...form,instructions:e.target.value})}/>
   <Popover><PopoverTrigger asChild><Button type="button" variant="outline" className="justify-start">Skills · {form.skills.length}</Button></PopoverTrigger><PopoverContent className="w-96"><Command><CommandInput placeholder={label('搜索 skill 和版本','Search skills and versions')}/><CommandList><CommandEmpty>{label('没有可用 skill','No skills available')}</CommandEmpty>{versions.data?.filter(v=>skills.data?.find(s=>s.id===v.skill_id)?.user_id===(editing?.user_id??me.id)).map(v=><CommandItem key={v.id} value={`${v.name} v${v.version} ${v.id}`} onSelect={()=>setForm(f=>({...f,skills:f.skills.includes(v.id)?f.skills.filter(id=>id!==v.id):[...f.skills.filter(id=>versions.data?.find(x=>x.id===id)?.skill_id!==v.skill_id),v.id]}))}><span className="mr-2">{form.skills.includes(v.id)?'✓':'○'}</span>{v.name} · v{v.version} <span className="ml-2 text-xs text-muted-foreground">{v.skill_id.slice(0,8)}</span></CommandItem>)}</CommandList></Command></PopoverContent></Popover>
   {form.skills.map(id=><div key={id} className="text-sm">{versions.data?.find(v=>v.id===id)?.name??id} · v{versions.data?.find(v=>v.id===id)?.version??'?'} <Button variant="ghost" type="button" onClick={()=>setForm({...form,skills:form.skills.filter(x=>x!==id)})}>×</Button></div>)}
   {versions.isError&&<ErrorState error={versions.error}/>}
   {save.isError&&<ErrorState error={save.error}/>}
   <div className="flex gap-2"><Button disabled={!form.runtime||save.isPending||versions.isPending}>{editing?label('发布新版本','Publish version'):t('common.create')}</Button>{editing&&<Button type="button" variant="outline" onClick={()=>{setEditing(null);setForm(empty);}}>{t('common.cancel')}</Button>}</div>
  </form></CardContent></Card>
  <Card><CardHeader><CardTitle>{t('agents.listTitle')}</CardTitle></CardHeader><CardContent>
   {del.isError&&<ErrorState error={del.error}/>}
   {agents.isPending?<Loading/>:agents.isError?<ErrorState error={agents.error}/>:<Table><TableHeader><TableRow><TableHead>ID</TableHead><TableHead>{t('common.name')}</TableHead><TableHead>{t('agents.runtime')}</TableHead><TableHead>{label('版本','Version')}</TableHead><TableHead/></TableRow></TableHeader><TableBody>{agents.data.map(a=><TableRow key={a.id}><TableCell>{a.id}</TableCell><TableCell>{a.name}</TableCell><TableCell>{devices.data?.find(d=>d.id===a.device_id)?.name??a.device_id} · {a.harness}</TableCell><TableCell>v{a.version}</TableCell><TableCell><Button variant="ghost" onClick={()=>load(a)}>{label('编辑','Edit')}</Button><Button variant="ghost" onClick={()=>setHistoryID(a.id)}>{label('历史版本','History')}</Button><Confirm title={t('agents.deleteTitle',{name:a.name})} description={t('agents.deleteText')} action={t('common.delete')} onConfirm={()=>del.mutate(a.id)}><Button variant="ghost" size="icon" aria-label={t('common.delete')}><Trash2/></Button></Confirm></TableCell></TableRow>)}</TableBody></Table>}
  </CardContent></Card>
  {historyID&&<Card><CardHeader><CardTitle>{historyID} · {label('历史版本','History')}</CardTitle></CardHeader><CardContent>{history.isPending?<Loading/>:history.isError?<ErrorState error={history.error}/>:history.data.map(v=><details key={v.id} className="border-b py-3"><summary className="cursor-pointer">v{v.version} · {v.name} · {new Date(v.created_at*1000).toLocaleString()}</summary><pre className="my-3 overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(v,null,2)}</pre><Button variant="outline" onClick={()=>{const a=agents.data?.find(a=>a.id===historyID);if(a)load(a,v);}}>{label('以此配置发布新版本','Use this configuration')}</Button></details>)}<Button variant="ghost" onClick={()=>setHistoryID('')}>{t('common.close')}</Button></CardContent></Card>}
 </div>;
}
