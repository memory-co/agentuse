import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { actAsHeaders } from '@/lib/actas';
import { queryClient } from '@/lib/query';
import { useT } from '@/lib/i18n';
import type { Skill, SkillVersion, User } from '@/lib/types';

export function Skills({me:_me}:{me:User}) {
 const t=useT();const label=(zh:string,en:string)=>t('nav.language')==='English'?zh:en;
 const skills=useQuery({queryKey:['skills'],queryFn:()=>api<Skill[]>('/api/skills')});
 const [uploadKey,setUploadKey]=useState(0);
 const [selected,setSelected]=useState<Skill|null>(null);const [file,setFile]=useState<File|null>(null);const [historyID,setHistoryID]=useState('');
 const versions=useQuery({queryKey:['skill-versions',historyID],enabled:!!historyID,queryFn:()=>api<SkillVersion[]>(`/api/skills/${historyID}/versions`)});
 const refresh=()=>{void queryClient.invalidateQueries({queryKey:['skills']});void queryClient.invalidateQueries({queryKey:['skill-versions']});};
 const upload=useMutation({mutationFn:()=>{if(!file)throw new Error('Select a ZIP');const body=new FormData();body.append('file',file);if(selected)body.append('base_version_id',selected.current_version_id);return api(selected?`/api/skills/${selected.id}/versions`:'/api/skills',{method:'POST',body});},onSuccess:()=>{setFile(null);setSelected(null);setUploadKey(k=>k+1);refresh();}});
 const del=useMutation({mutationFn:(id:string)=>api(`/api/skills/${id}`,{method:'DELETE'}),onSuccess:refresh});
 const download=useMutation({mutationFn:async(v:SkillVersion)=>{const r=await fetch(v.download_path,{headers:actAsHeaders(v.download_path)});if(!r.ok)throw new Error(`Download: ${r.status}`);const url=URL.createObjectURL(await r.blob());const a=document.createElement('a');a.href=url;a.download=`${v.name}-v${v.version}.zip`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}});
 return <div className="grid gap-4"><Card><CardHeader><CardTitle>{selected?`${selected.current_version.name} · ${label('发布新版本','Publish version')}`:label('新建 skill','New skill')}</CardTitle><CardDescription>{label('上传根目录包含 SKILL.md 的 ZIP。已发布版本不可修改；更新 skill 不会自动改变 agent。','Upload a ZIP with SKILL.md at its root. Published versions are immutable; skill updates do not change agent versions.')}</CardDescription></CardHeader><CardContent><form className="flex flex-wrap gap-2" onSubmit={e=>{e.preventDefault();upload.mutate();}}><Input key={`${selected?.id??'new'}-${uploadKey}`} aria-label="Skill ZIP" type="file" accept=".zip,application/zip" onChange={e=>setFile(e.target.files?.[0]??null)} required/><Button disabled={!file||upload.isPending}>{selected?label('发布新版本','Publish version'):t('common.create')}</Button>{selected&&<Button type="button" variant="outline" onClick={()=>{setSelected(null);setFile(null);}}>{t('common.cancel')}</Button>}</form>{upload.isError&&<ErrorState error={upload.error}/>}</CardContent></Card>
 {del.isError&&<ErrorState error={del.error}/>} {download.isError&&<ErrorState error={download.error}/>}
 {skills.isPending?<Loading/>:skills.isError?<ErrorState error={skills.error}/>:skills.data.map(s=><Card key={s.id}><CardHeader><CardTitle>{s.current_version.name} · v{s.current_version.version}</CardTitle><CardDescription>{s.current_version.description}</CardDescription></CardHeader><CardContent><p className="mb-3 break-all font-mono text-xs text-muted-foreground">{s.id}</p><div className="flex gap-2"><Button variant="outline" onClick={()=>{setSelected(s);setFile(null);}}>{label('上传新版本','Upload version')}</Button><Button variant="outline" onClick={()=>setHistoryID(s.id)}>{label('历史版本','History')}</Button><Confirm title={t('common.delete')} description={label('已发布的包和 agent 引用会保留。','Published packages and agent references will be retained.')} action={t('common.delete')} onConfirm={()=>del.mutate(s.id)}><Button variant="ghost">{t('common.delete')}</Button></Confirm></div></CardContent></Card>)}
 {historyID&&<Card><CardHeader><CardTitle>{label('历史版本','History')}</CardTitle></CardHeader><CardContent>{versions.isPending?<Loading/>:versions.isError?<ErrorState error={versions.error}/>:versions.data.map(v=><div key={v.id} className="border-b py-3"><p>{v.name} · v{v.version} · {v.size_bytes} bytes</p><p className="break-all font-mono text-xs text-muted-foreground">SHA256 {v.sha256}</p><Button variant="ghost" onClick={()=>download.mutate(v)}>{label('下载 ZIP','Download ZIP')}</Button></div>)}<Button variant="ghost" onClick={()=>setHistoryID('')}>{t('common.close')}</Button></CardContent></Card>}
 </div>;
}
