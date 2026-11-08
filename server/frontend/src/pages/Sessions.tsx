import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Check, ChevronRight, Circle, CircleCheck, FolderOpen, LoaderCircle, RotateCcw, Trash2, UserRound, X } from 'lucide-react';
import { MentionComposer } from '@/components/MentionComposer';
import { Attachments } from '@/components/Attachments';
import { listAttachments } from '@/lib/attachments';
import { useMessageFiles } from '@/hooks/use-message-files';
import { MessageFiles } from '@/components/MessageFiles';
import { Sheet, SheetContent, SheetTitle, SheetDescription } from '@/components/ui/sheet';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { useActAs } from '@/lib/actas';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Message, Question, Session, SessionDetail as Detail, SessionEvent, SessionStatus, Run, Delivery, User, Attachment } from '@/lib/types';
import { cn, fmtTime } from '@/lib/utils';
import { t, useT } from '@/lib/i18n';

export const STATUS_TONE: Record<SessionStatus, 'ok' | 'warn' | 'run' | 'muted'> = { idle: 'ok', pending: 'warn', running: 'run', waiting: 'warn', resolved: 'muted' };
export const statusLabel = (s: SessionStatus) => t(`session.status.${s}`);
/** 侧栏「会话」tab:不在工作里的会话。 */
export const useSessions = () => useQuery({ queryKey: ['sessions'], queryFn: () => api<Session[]>('/api/sessions'), refetchInterval: 3000 });
/** 某个工作的线程(不含协调者)。 */
export const useWorkSessions = (workId: string, enabled = true) => useQuery({
  queryKey: ['sessions', 'work', workId], queryFn: () => api<Session[]>(`/api/sessions?work_id=${encodeURIComponent(workId)}`), refetchInterval: 3000, enabled,
});
const refresh = () => Promise.all([queryClient.invalidateQueries({ queryKey: ['sessions'] }), queryClient.invalidateQueries({ queryKey: ['works'] })]);
// progress 回报只有 {text} 时直接显示文字，否则显示 JSON。
const evText = (p: any) => (p && typeof p === 'object' && Object.keys(p).length === 1 && 'text' in p ? String(p.text) : JSON.stringify(p ?? ''));

/** 新会话：选 agent,写第一句话。 */
export function NewSession({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
  const actAs = useActAs();
  const sender = actAs || queryClient.getQueryData<User>(['me'])?.id;

  const fileDraft = useMessageFiles();
  const draft = useRef<string | null>(null);
  const [draftId, setDraftId] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: async ({prompt, targets, users}: {prompt:string;targets:string[];users:string[]}) => {
      if (!draft.current) {
        const r = await api<{session: Session}>('/api/sessions', {method: 'POST', body: {mode:"multi_agent"}});
        draft.current = r.session.id; setDraftId(r.session.id); void refresh();
      }
      const id = draft.current;
      const attachment_ids = await fileDraft.upload(id);
      const body={prompt,to_agent_ids:targets,to_user_ids:users,attachment_ids};
      await api(`/api/sessions/${id}/messages`, {method: 'POST', body});
      return {session: {id} as Session};
    },
    onSuccess: r => { void refresh(); onCreated(r.session.id); },
  });
  return <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-4 py-10">
    <div className="grid gap-1 text-center">
      <h1 className="text-2xl font-semibold">{t('session.ask')}</h1>
      <p className="text-sm text-muted-foreground">{t('mention.shared')}</p>
    </div>
    <MentionComposer showAgentPicker agents={agents.data??[]} users={sender ? [sender] : []} files={fileDraft.files} onFiles={fileDraft.addFiles} onRemoveFile={fileDraft.remove} placeholder={t('session.taskPlaceholder')} busy={add.isPending} onSend={(prompt,targets,users)=>add.mutateAsync({prompt,targets,users})} />
    {draftId && <div className="grid gap-1 text-center text-xs"><p>{t('attachment.created')}</p><a className="underline" href={`#/s/${draftId}`}>{t('attachment.open')}</a></div>}
    {add.isError && <div className="text-center"><ErrorState error={add.error} /></div>}
    {agents.isError && <ErrorState error={agents.error} />}
  </div>;
}

/** 会话页：上面是消息流，下面是回复框。工作页里协调者的对话也用它(placeholder 换掉，不能删、不能标记完成)。 */
export function Conversation({ id, onDeleted, placeholder,defaultAgent }: { id: string; onDeleted: () => void; placeholder?: string; defaultAgent?:string }) {
  const t = useT();
 const agents=useAgents();
 const fileDraft=useMessageFiles();
 const [selectedFile,setSelectedFile]=useState<Attachment|null>(null);
 const [filesOpen,setFilesOpen]=useState(true);
 const [wide,setWide]=useState(()=>window.matchMedia('(min-width:1280px)').matches);
 useEffect(()=>{const media=window.matchMedia('(min-width:1280px)');const change=()=>setWide(media.matches);media.addEventListener('change',change);return()=>media.removeEventListener('change',change)},[]);
 const [drawerOpen,setDrawerOpen]=useState(false);
 const attachments=useQuery({queryKey:['attachments',id],queryFn:()=>listAttachments(id),refetchInterval:3000});
 const openFile=(file:Attachment)=>{setSelectedFile(file);if(wide)setFilesOpen(true);else setDrawerOpen(true)};


  const q = useQuery({ queryKey: ['sessions', id], queryFn: () => api<Detail>(`/api/sessions/${id}`), refetchInterval: 3000 });
  const reply = useMutation({
    mutationFn: async ({prompt,targets,users}: {prompt:string;targets:string[];users:string[]}) => {const attachment_ids=await fileDraft.upload(id);const body={prompt,to_agent_ids:targets,to_user_ids:users,attachment_ids};return api(`/api/sessions/${id}/messages`, { method:'POST', body });},
    onSuccess: () => { fileDraft.clear(); void refresh(); },
  });
  const del = useMutation({ mutationFn: () => api(`/api/sessions/${id}`, { method: 'DELETE' }), onSuccess: () => { void refresh(); onDeleted(); } });
  const resolve = useMutation({ mutationFn: (done: boolean) => api(`/api/sessions/${id}/${done ? 'resolve' : 'reopen'}`, { method: 'POST' }), onSuccess: () => { void refresh(); } });
  const bottom = useRef<HTMLDivElement>(null);
  const count = (q.data?.messages.length ?? 0) + (q.data?.events.length ?? 0);
  useEffect(() => { bottom.current?.scrollIntoView({ block: 'end' }); }, [count]);
  if (q.isPending) return <div className="px-6"><Loading /></div>;
  if (q.isError) return <div className="px-6 py-6"><ErrorState error={q.error} /></div>;
  const { session: x, messages, events } = q.data;
  const questions = q.data.questions ?? [];
  const initialTargets = defaultAgent ? [defaultAgent] : [];
  const meta = t("mention.shared");
  const coordinator = x.work_role === 'coordinator';
  const settled = x.status === 'idle' || x.status === 'resolved';
  return <div className="flex min-h-0 flex-1 overflow-hidden">
   <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid min-w-0 w-full max-w-3xl grid-cols-1 gap-6 px-4 py-6">
        <div className="flex items-start gap-2 border-b pb-3">
          <p className="min-w-0 flex-1 break-all font-mono text-xs text-muted-foreground">{meta}</p>
          <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" aria-label={t('attachment.files')} onClick={()=>wide?setFilesOpen(value=>!value):setDrawerOpen(true)}><FolderOpen />{t('attachment.files')}</Button>
          {!coordinator && settled && <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" disabled={resolve.isPending} onClick={() => resolve.mutate(x.status === 'idle')}>
            {x.status === 'idle' ? <><CircleCheck />{t('session.resolve')}</> : <><RotateCcw />{t('session.reopen')}</>}
          </Button>}
          {!coordinator && settled && <Confirm title={t('session.deleteTitle')} description={t('session.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
            <Button variant="ghost" size="icon" className="size-7 text-destructive" aria-label={t('session.delete')}><Trash2 /></Button>
          </Confirm>}
        </div>

        {messages.map(m=><div key={m.id} className="grid min-w-0 grid-cols-1 gap-2"><MessageView m={m} onOpenFile={openFile}/>
        {m.role==='user'&&<div className="flex flex-wrap gap-2">{(q.data.deliveries??[]).filter(d=>d.message_id===m.id && !(q.data.runs??[]).some(run=>run.id===d.run_id && run.first_message_id===m.id)).map(d=><span key={`${d.message_id}:${d.agent_id}`} className="rounded-md border px-2 py-1 text-xs" title={d.error}>@{d.agent_id} · {d.status}</span>)}</div>}
        {(q.data.runs??[]).filter(r=>r.first_message_id===m.id).map(run=><RunView key={run.id} run={run} events={events} questions={questions} messages={messages} deliveries={q.data.deliveries ?? []} sessionId={id}/>)}
        </div>)}
        <div ref={bottom} />
      </div>
    </div>
    <div className="mx-auto w-full max-w-3xl px-4 pb-4">
      {reply.isError && <div className="pb-2"><ErrorState error={reply.error} /></div>}
      <MentionComposer files={fileDraft.files} onFiles={fileDraft.addFiles} onRemoveFile={fileDraft.remove} key={id} agents={agents.data??[]} users={[...new Set([x.user_id, x.created_by, ...messages.flatMap(m => [m.author_user_id, ...m.to_user_ids])].filter((value): value is string => !!value))]} initialTargets={initialTargets} placeholder={placeholder??t('session.replyPlaceholder')} busy={reply.isPending} onSend={(prompt,targets,users)=>reply.mutateAsync({prompt,targets,users})}/>

    </div>
   </div>
   {wide ? filesOpen && <aside className="w-80 shrink-0 border-l 2xl:w-96" data-conversation-files={id}><Attachments session={id} files={attachments.data??[]} pending={attachments.isPending} error={attachments.error} selected={selectedFile} onSelect={setSelectedFile} onClose={()=>setFilesOpen(false)} onAttach={fileDraft.addExisting} busy={reply.isPending} referenced={new Set(messages.flatMap(message=>message.attachments.map(file=>file.id)))} /></aside> : <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}><SheetContent className="w-[90vw] p-0 sm:max-w-sm [&>button]:hidden"><SheetTitle className="sr-only">{t('attachment.files')}</SheetTitle><SheetDescription className="sr-only">{t('attachment.preview')}</SheetDescription><Attachments session={id} files={attachments.data??[]} pending={attachments.isPending} error={attachments.error} selected={selectedFile} onSelect={setSelectedFile} onClose={()=>setDrawerOpen(false)} onAttach={file=>{fileDraft.addExisting(file);setDrawerOpen(false)}} busy={reply.isPending} referenced={new Set(messages.flatMap(message=>message.attachments.map(file=>file.id)))} /></SheetContent></Sheet>}
  </div>;
}

function MessageView({ m, onOpenFile }: { m: Message; onOpenFile: (file: Attachment) => void }) {
 const t=useT();const c=m.content||{};
 const targetLabel = <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground" data-message-targets={m.id}>
   {m.to_agent_ids.map(agent => <span key={`agent:${agent}`} className="break-all font-medium">@{agent}</span>)}
   {m.to_user_ids.map(user => <span key={`user:${user}`} className="inline-flex items-center gap-1 break-all font-medium"><UserRound className="size-3 shrink-0" />{user}</span>)}
   {!m.to_agent_ids.length && !m.to_user_ids.length && <span>{t('mention.noteOnly')}</span>}
 </div>;
 if(m.role==='user') {
  const text=typeof c.prompt==='string'?c.prompt:JSON.stringify(c,null,2);
  return typeof c.from_session === 'string'
    ? <div className="grid min-w-0 gap-1.5" data-message-id={m.id}><ThreadNote m={m}/>{targetLabel}</div>
    : <div className="ml-auto grid min-w-0 max-w-[85%] gap-1.5 rounded-2xl bg-muted px-4 py-2.5 text-sm" title={fmtTime(m.created_at)} data-message-id={m.id}>
        {targetLabel}
        <div className="whitespace-pre-wrap break-words">{text}</div>
        <MessageFiles files={m.attachments} onOpen={onOpenFile} />
      </div>;
 }
  const out = c.ok ? (typeof c.output === 'string' ? c.output : JSON.stringify(c, null, 2)) : (c.error || t('session.failed')) + (c.output ? '\n' + c.output : '');
  return <div className="grid min-w-0 grid-cols-1 gap-1.5" title={fmtTime(m.created_at)}>
    {targetLabel}
    {m.agent_key&&<div className="text-xs font-medium text-muted-foreground">@{m.agent_key}{m.agent_name&&` · ${m.agent_name}`}</div>}
    {!c.ok && <div><StatusBadge tone="bad">{t('session.failed')}</StatusBadge></div>}
    <div className={cn('min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-sm leading-7', !c.ok && 'text-destructive')}>{out}</div>
    <MessageFiles files={m.attachments} onOpen={onOpenFile} />
  </div>;
}

/** 线程回完后发给协调者的消息：显示成一张小卡片，点了进线程(designs/v2/designs/work.md)。 */
function ThreadNote({ m }: { m: Message }) {
  const t = useT();
  const prompt = String(m.content.prompt ?? '');
  const nl = prompt.indexOf('\n');
  const title = /「(.*)」/.exec(nl < 0 ? prompt : prompt.slice(0, nl))?.[1] || m.content.from_session;
  return <a href={`#/s/${encodeURIComponent(m.content.from_session)}`} title={fmtTime(m.created_at)}
    className="mr-auto grid max-w-[85%] gap-1 rounded-lg border bg-muted/40 px-3 py-2 text-sm hover:bg-muted">
    <span className="text-xs font-medium text-muted-foreground">{t('session.threadReplied', { title })}</span>
    <span className="line-clamp-3 whitespace-pre-wrap break-words">{nl < 0 ? '' : prompt.slice(nl + 1)}</span>
  </a>;
}

function QuestionView({ q, sessionId }: { q: Question; sessionId: string }) {
 const [answers,setAnswers]=useState<Record<string,string>>({});
 const p=q.payload??{};
 const reply=useMutation({mutationFn:(answer:unknown)=>api(`/api/sessions/${sessionId}/runs/${q.run_id}/requests/${encodeURIComponent(q.request_id)}/reply`,{method:'POST',body:{answer}}),onSuccess:()=>void refresh()});
 const busy=reply.isPending||(reply.isSuccess&&!q.delivery_error);
 return <div className="grid gap-2 rounded-lg border p-3 text-sm">
  <p className="whitespace-pre-wrap break-words font-medium">{p.message}</p>
  {q.state!=='pending' ? <p className="text-xs text-muted-foreground">{q.state}{p.answer!==undefined&&` · ${JSON.stringify(p.answer)}`}</p> : p.kind==='permission' ? <div className="flex gap-2"><Button size="sm" disabled={busy} onClick={()=>reply.mutate('allow')}>allow</Button><Button size="sm" variant="outline" disabled={busy} onClick={()=>reply.mutate('deny')}>deny</Button></div> : <form className="grid gap-3" onSubmit={e=>{e.preventDefault();const value=Object.fromEntries((p.questions??[]).map((item:any)=>[item.id,item.multiple?(answers[item.id]??'').split(',').map(x=>x.trim()).filter(Boolean):answers[item.id]]));reply.mutate(value)}}>
   {(p.questions??[]).map((item:any)=><label key={item.id} className="grid gap-1"><span>{item.text}</span>{item.options?.length>0&&<span className="text-xs text-muted-foreground">{item.options.map((o:any)=>o.label).join(' / ')}</span>}<Input required value={answers[item.id]??''} onChange={e=>setAnswers({...answers,[item.id]:e.target.value})} placeholder={item.multiple?'多个答案用逗号分隔':''}/></label>)}
   <Button size="sm" disabled={busy}>回复</Button>
  </form>}
  {busy&&reply.isSuccess&&<p className="text-xs text-muted-foreground">已发送，等待 client 确认</p>}
  {q.delivery_error&&<p className="text-xs text-destructive">{q.delivery_error}</p>}
  {reply.isError&&<ErrorState error={reply.error}/>}
 </div>;
}

function RunView({run,events,questions,sessionId}:{run:Run;events:SessionEvent[];questions:Question[];messages:Message[];deliveries:Delivery[];sessionId:string}){
 const cancel=useMutation({mutationFn:()=>api(`/api/sessions/${sessionId}/runs/${run.id}/cancel`,{method:'POST'}),onSuccess:()=>void refresh()});
 const mine=events.filter(e=>e.run_id===run.id);
 const active=['running','waiting'].includes(run.status);
 return <section data-run-id={run.id} aria-label={`run @${run.agent_id}`}><Dialog.Root>
  <Dialog.Trigger asChild><button className="inline-flex max-w-full items-center gap-2 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted">{active&&<LoaderCircle className="size-3.5 animate-spin"/>}<span>@{run.agent_id}</span><span>{run.status}</span><ChevronRight className="size-3.5"/></button></Dialog.Trigger>
  <Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-50 bg-black/50"/><Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[85dvh] w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border bg-background p-4 shadow-lg" data-run-detail={run.id}>
   <div className="flex items-start justify-between"><div><Dialog.Title className="font-semibold">@{run.agent_id} · Run details</Dialog.Title><Dialog.Description className="mt-1 text-xs text-muted-foreground">{run.status} · {run.harness} · {run.harness_revision}</Dialog.Description></div><Dialog.Close asChild><Button size="icon" variant="ghost" aria-label="关闭"><X className="size-4"/></Button></Dialog.Close></div>
   <dl className="my-4 grid grid-cols-[auto_minmax(0,1fr)] gap-2 text-xs"><dt>Run</dt><dd className="break-all">{run.id}</dd><dt>Client session</dt><dd className="break-all">{run.client_session_id}</dd><dt>Device</dt><dd className="break-all">{run.daemon_id}</dd></dl>
   {run.output&&<p className="whitespace-pre-wrap break-words text-sm">{run.output}</p>}
   {run.error&&<p className="text-sm text-destructive">{run.error}</p>}
   <div className="my-4 grid gap-3">{questions.filter(q=>q.run_id===run.id).map(q=><QuestionView key={q.request_id} q={q} sessionId={sessionId}/>)}</div>
   {active&&<Button size="sm" variant="outline" disabled={cancel.isPending} onClick={()=>cancel.mutate()}>取消此 client session</Button>}
   {cancel.isSuccess&&active&&<p className="mt-2 text-xs text-muted-foreground">取消请求已发送，等待 client 上报结果</p>}
   {cancel.isError&&<ErrorState error={cancel.error}/>}
   <div className="mt-4 grid gap-3">{mine.map(e=><div key={e.id} className="rounded border p-3 text-xs"><span className="text-muted-foreground">{e.kind}</span><pre className="mt-2 whitespace-pre-wrap break-words">{JSON.stringify(e.payload,null,2)}</pre></div>)}</div>
  </Dialog.Content></Dialog.Portal>
 </Dialog.Root></section>;
}
