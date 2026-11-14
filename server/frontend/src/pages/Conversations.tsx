import { Markdown } from '@/components/Markdown';
import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ChevronRight, CircleCheck, FolderOpen, LoaderCircle, MessagesSquare, RotateCcw, Trash2, UserRound, X } from 'lucide-react';
import { MentionComposer } from '@/components/MentionComposer';
import { FileManager } from '@/components/FileManager';
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
import type { Message, Question, Conversation, ConversationDetail as Detail, ConversationEvent, ConversationStatus, ConversationRound, Run, Delivery, User, Attachment } from '@/lib/types';
import { cn, fmtTime } from '@/lib/utils';
import { t, useT } from '@/lib/i18n';

export const STATUS_TONE: Record<ConversationStatus, 'ok' | 'warn' | 'run' | 'muted'> = { idle: 'ok', pending: 'warn', running: 'run', waiting: 'warn', resolved: 'muted' };
export const statusLabel = (s: ConversationStatus) => t(`conversation.status.${s}`);
export const useConversations = () => useQuery({ queryKey: ['conversations'], queryFn: () => api<Conversation[]>('/api/conversations') });
export const useChildren = (parent: string, enabled=true) => useQuery({
  queryKey: ['conversations', 'children', parent], queryFn: () => api<Conversation[]>(`/api/conversations?parent_conversation_id=${encodeURIComponent(parent)}`), enabled,
});
const refresh = () => queryClient.invalidateQueries({ queryKey: ['conversations'] });
// progress 回报只有 {text} 时直接显示文字，否则显示 JSON。
const evText = (p: any) => (p && typeof p === 'object' && Object.keys(p).length === 1 && 'text' in p ? String(p.text) : JSON.stringify(p ?? ''));

/** 新会话：选 agent,写第一句话。 */
export function NewConversation({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
  const actAs = useActAs();
  const sender = actAs || queryClient.getQueryData<User>(['me'])?.id;

  const [mode,setMode]=useState<Conversation['mode']>('single');
  const fileDraft = useMessageFiles();
  const draft = useRef<string | null>(null);
  const [draftId, setDraftId] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: async ({prompt, targets, users}: {prompt:string;targets:string[];users:string[]}) => {
      if (!draft.current) {
        const r = await api<{conversation: Conversation}>('/api/conversations', {method: 'POST', body: {mode}});
        draft.current = r.conversation.id; setDraftId(r.conversation.id); void refresh();
      }
      const id = draft.current;
      const attachment_ids = await fileDraft.upload(id);
      const body={prompt,to_agent_ids:targets,to_user_ids:users,attachment_ids};
      await api(`/api/conversations/${id}/messages`, {method: 'POST', body});
      return {conversation: {id} as Conversation};
    },
    onSuccess: r => { void refresh(); onCreated(r.conversation.id); },
  });
  return <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-4 py-10">
    <div className="grid gap-1 text-center">
      <h1 className="text-2xl font-semibold">{t('conversation.ask')}</h1>
      <p className="text-sm text-muted-foreground">{t('mention.shared')}</p>
    </div>
    <fieldset className="grid gap-2 rounded-xl border p-3" disabled={add.isPending||!!draftId}>
      <legend className="px-1 text-xs text-muted-foreground">{t('nest.mode')}</legend>
      {(['single','collaborative'] as const).map(value=><label key={value} className="flex cursor-pointer items-start gap-3 rounded-md p-2 hover:bg-muted/60"><input className="mt-1" type="radio" name="conversation-mode" value={value} checked={mode===value} onChange={()=>setMode(value)}/><span className="grid gap-1"><span className="text-sm font-medium">{t(`nest.${value}`)}</span><span className="text-xs text-muted-foreground">{t(`nest.${value}Hint`)}</span></span></label>)}
    </fieldset>
    <MentionComposer showAgentPicker agents={agents.data??[]} users={sender ? [sender] : []} files={fileDraft.files} onFiles={fileDraft.addFiles} onRemoveFile={fileDraft.remove} placeholder={t('conversation.taskPlaceholder')} busy={add.isPending} onSend={(prompt,targets,users)=>add.mutateAsync({prompt,targets,users})} />
    {draftId && <div className="grid gap-1 text-center text-xs"><p>{t('attachment.created')}</p><a className="underline" href={`#/conversations/${draftId}`}>{t('attachment.open')}</a></div>}
    {add.isError && <div className="text-center"><ErrorState error={add.error} /></div>}
    {agents.isError && <ErrorState error={agents.error} />}
  </div>;
}

/** Conversation messages, nested conversations and files. */
export function ConversationView({ id, onDeleted }: { id: string; onDeleted: () => void }) {
  const t = useT();
  const actAs = useActAs();
  const sender = actAs || queryClient.getQueryData<User>(['me'])?.id;
  const agents = useAgents();
  const fileDraft = useMessageFiles();
  const [selectedFile, setSelectedFile] = useState<Attachment | null>(null);
  const [filesOpen, setFilesOpen] = useState(false);
  const [childrenOpen, setChildrenOpen] = useState(false);
  const fileReturnFocus = useRef<HTMLElement | null>(null);
  const composer = useRef<HTMLDivElement>(null);
  const attachments = useQuery({ queryKey: ['attachments', id], queryFn: () => listAttachments(id), enabled: filesOpen });
  const openFiles = (file?: Attachment) => {
    fileReturnFocus.current = document.activeElement as HTMLElement | null;
    if (file) setSelectedFile(file);
    setFilesOpen(true);
  };
  const attachFile = (file: Attachment) => {
    fileDraft.addExisting(file);
    fileReturnFocus.current = composer.current?.querySelector('textarea') ?? fileReturnFocus.current;
    setFilesOpen(false);
  };

  const roundParam = new URLSearchParams(location.hash.split('?')[1] ?? '').get('round');
  const selectedRound = roundParam === null ? undefined : Number(roundParam);
  const invalidRound = selectedRound !== undefined && (!Number.isSafeInteger(selectedRound) || selectedRound <= 0);
  const q = useQuery({ queryKey: selectedRound === undefined ? ['conversations', id] : ['conversations', id, 'round', selectedRound], queryFn: () => api<Detail>(`/api/conversations/${id}${selectedRound === undefined ? '' : `?round=${selectedRound}`}`), enabled: !invalidRound });
  const children=useChildren(id,q.data?.conversation.mode==='collaborative');
  const reply = useMutation({
    mutationFn: async ({prompt,targets,users}: {prompt:string;targets:string[];users:string[]}) => {const attachment_ids=await fileDraft.upload(id);const body={prompt,to_agent_ids:targets,to_user_ids:users,attachment_ids};return api(`/api/conversations/${id}/messages`, { method:'POST', body });},
    onSuccess: () => { fileDraft.clear(); void refresh(); },
  });
  const del = useMutation({ mutationFn: () => api(`/api/conversations/${id}${q.data?.conversation.mode==='collaborative'?'?recursive=true':''}`, { method: 'DELETE' }), onSuccess: () => { void refresh(); onDeleted(); } });
  const resolve = useMutation({ mutationFn: (done: boolean) => api(`/api/conversations/${id}/${done ? 'resolve' : 'reopen'}`, { method: 'POST' }), onSuccess: () => { void refresh(); } });
  const bottom = useRef<HTMLDivElement>(null);
  const count = (q.data?.messages.length ?? 0) + (q.data?.events.length ?? 0);
  useEffect(() => { if(selectedRound === undefined) bottom.current?.scrollIntoView({block:'end'}); }, [count,selectedRound]);
  if (invalidRound) return <ErrorState error={t('nest.invalidRound')} />;
  if (q.isPending) return <div className="px-6"><Loading /></div>;
  if (q.isError) return <div className="px-6 py-6"><ErrorState error={q.error} /></div>;
  const { conversation: x, messages, events } = q.data;
  const lastSent = [...messages].reverse().find(message => message.author_role === 'user' && message.author_user_id === sender);
  const questions = q.data.questions ?? [];
  const meta = t(`nest.${x.mode}`);
  const settled = x.status === 'idle' || x.status === 'resolved';
  return <div className="flex min-h-0 flex-1 overflow-hidden">
   <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid min-w-0 w-full max-w-3xl grid-cols-1 gap-6 px-4 py-6">
        <div className="flex items-start gap-2 border-b pb-3">
          <p className="min-w-0 flex-1 break-all font-mono text-xs text-muted-foreground">{meta}</p>
          <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" aria-label={t('attachment.files')} aria-haspopup="dialog" onClick={() => openFiles()}><FolderOpen />{t('attachment.files')}</Button>
          {x.mode === 'collaborative' && <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs xl:hidden" aria-label={t('nest.children')} aria-haspopup="dialog" onClick={() => setChildrenOpen(true)}><MessagesSquare />{t('nest.children')}<span className="text-muted-foreground">{children.data?.length ?? 0}</span></Button>}
          {selectedRound === undefined && settled && <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" disabled={resolve.isPending} onClick={() => resolve.mutate(x.status === 'idle')}>
            {x.status === 'idle' ? <><CircleCheck />{t('conversation.resolve')}</> : <><RotateCcw />{t('conversation.reopen')}</>}
          </Button>}
          {selectedRound === undefined && settled && <Confirm title={t('conversation.deleteTitle')} description={t(x.mode==='collaborative'?'nest.deleteTree':'conversation.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
            <Button variant="ghost" size="icon" className="size-7 text-destructive" aria-label={t('conversation.delete')}><Trash2 /></Button>
          </Confirm>}
        </div>

        {selectedRound !== undefined && <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-muted/30 p-3 text-xs"><span>{t('nest.round', {n:selectedRound})} · {t(q.data.rounds[0]?.finished_at !== undefined ? 'nest.roundEnded' : 'nest.roundActive')}</span><a className="underline" href={`#/conversations/${id}`}>{t('nest.fullConversation')}</a></div>}
        {(del.isError||resolve.isError) && <ErrorState error={del.error||resolve.error}/>}

        {messages.map(m=><div key={m.id} className="grid min-w-0 grid-cols-1 gap-2">{x.parent_conversation_id && q.data.rounds.filter(round => round.start_message_seq === m.seq).map(round => <RoundStatus key={round.seq} round={round} />)}<MessageView m={m} onOpenFile={openFiles}/>
        {m.to_agent_ids.length>0&&<div className="flex flex-wrap gap-2">{(q.data.deliveries??[]).filter(d=>d.message_id===m.id && !(q.data.runs??[]).some(run=>run.id===d.run_id && run.first_message_id===m.id)).map(d=><span key={`${d.message_id}:${d.agent_id}`} className="rounded-md border px-2 py-1 text-xs" title={d.error}>@{d.agent_id} · {d.status}</span>)}</div>}
        {(q.data.runs??[]).filter(r=>r.first_message_id===m.id).map(run=><RunView key={run.id} run={run} events={events} questions={questions} messages={messages} deliveries={q.data.deliveries ?? []} conversationId={id}/>)}
        </div>)}
        <div ref={bottom} />
      </div>
    </div>
    {selectedRound === undefined && <div ref={composer} className="mx-auto w-full max-w-3xl px-4 pb-4">
      {reply.isError && <div className="pb-2"><ErrorState error={reply.error} /></div>}
      <MentionComposer files={fileDraft.files} onFiles={fileDraft.addFiles} onRemoveFile={fileDraft.remove} key={`${id}:${sender}`} initialTargets={lastSent?.to_agent_ids ?? []} agents={agents.data??[]} users={[...new Set([x.user_id, x.created_by, ...messages.flatMap(m => [m.author_user_id, ...m.to_user_ids])].filter((value): value is string => !!value))]} placeholder={t('conversation.replyPlaceholder')} busy={reply.isPending} onSend={(prompt,targets,users)=>reply.mutateAsync({prompt,targets,users})}/>

    </div>}
   </div>
   {x.mode === 'collaborative' && <>
     <aside className="hidden w-80 shrink-0 flex-col border-l xl:flex 2xl:w-96" aria-label={t('nest.children')} data-conversation-children={id}>
       <ChildConversations conversations={children.data ?? []} pending={children.isPending} error={children.error} />
     </aside>
     <Sheet open={childrenOpen} onOpenChange={setChildrenOpen}>
       <SheetContent className="w-[90vw] p-0 sm:max-w-sm [&>button]:hidden">
         <SheetTitle className="sr-only">{t('nest.children')}</SheetTitle>
         <SheetDescription className="sr-only">{t('nest.childrenDescription')}</SheetDescription>
         <ChildConversations conversations={children.data ?? []} pending={children.isPending} error={children.error} onClose={() => setChildrenOpen(false)} />
       </SheetContent>
     </Sheet>
   </>}
   <FileManager open={filesOpen} onOpenChange={setFilesOpen} conversation={id} files={attachments.data ?? []} pending={attachments.isPending} error={attachments.error}
     selected={selectedFile} onSelect={setSelectedFile} onAttach={attachFile} busy={reply.isPending} readOnly={selectedRound !== undefined}
     onRestoreFocus={() => { if (fileReturnFocus.current?.isConnected) fileReturnFocus.current.focus({ preventScroll: true }); }}
     referenced={new Set(messages.flatMap(message => message.attachments.map(file => file.id)))} />
  </div>;
}

function ChildConversations({ conversations, pending, error, onClose }: {
  conversations: Conversation[]; pending: boolean; error: unknown; onClose?: () => void;
}) {
  const t = useT();
  return <section className="flex h-full min-h-0 flex-col bg-background">
    <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
      <MessagesSquare className="size-4 text-muted-foreground" />
      <h2 className="text-sm font-semibold">{t('nest.children')}</h2>
      <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{conversations.length}</span>
      {onClose && <Button variant="ghost" size="icon" className="ml-auto size-8" aria-label={t('common.close')} onClick={onClose}><X /></Button>}
    </header>
    <div className="min-h-0 flex-1 overflow-y-auto p-3">
      {pending ? <Loading /> : error ? <ErrorState error={error} /> : conversations.length ? <div className="grid gap-2">
        {conversations.map(child => <a key={child.id} className="group grid gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" href={`#/conversations/${child.id}`} onClick={onClose}>
          <span className="line-clamp-3 break-words text-sm leading-6">{child.title || child.id}</span>
          <span className="flex items-center justify-between gap-2"><StatusBadge tone={STATUS_TONE[child.status]}>{statusLabel(child.status)}</StatusBadge><ChevronRight className="size-3.5 text-muted-foreground transition-transform group-hover:translate-x-0.5" /></span>
        </a>)}
      </div> : <div className="flex h-full min-h-40 flex-col items-center justify-center gap-3 px-4 text-center text-muted-foreground"><MessagesSquare className="size-8 opacity-40" /><p className="text-xs leading-6">{t('nest.noChildren')}</p></div>}
    </div>
  </section>;
}

function RoundStatus({round}:{round:ConversationRound}) {
 const t=useT();
 return <div className="mt-2 flex flex-wrap items-center gap-2 border-t pt-3 text-xs text-muted-foreground" data-conversation-round={round.seq}>
  <a className="font-medium hover:underline" href={`#/conversations/${round.conversation_id}?round=${round.seq}`}>{t('nest.round',{n:round.seq})}</a>
  <span>{t(round.finished_at === undefined ? 'nest.roundActive' : 'nest.roundEnded')}</span>
  {round.parent_delivery && <span className={cn(round.parent_delivery==='failed'&&'text-destructive')} title={round.parent_delivery_error}>{t(`nest.delivery.${round.parent_delivery}`)}{round.parent_delivery_error&&` · ${round.parent_delivery_error}`}</span>}
 </div>;
}

function MessageView({ m, onOpenFile }: { m: Message; onOpenFile: (file: Attachment) => void }) {
 const t=useT();
 const targetLabel = <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground" data-message-targets={m.id}>
   {m.to_agent_ids.map(agent => <span key={`agent:${agent}`} className="break-all font-medium">@{agent}</span>)}
   {m.to_user_ids.map(user => <span key={`user:${user}`} className="inline-flex items-center gap-1 break-all font-medium"><UserRound className="size-3 shrink-0" />{user}</span>)}
   {m.author_role==='user' && !m.to_agent_ids.length && !m.to_user_ids.length && <span>{t('mention.noteOnly')}</span>}
 </div>;
 if(m.author_role==='child_conversation') {
  return <div className="grid gap-3 rounded-lg border bg-muted/20 p-4" data-child-notification={m.id}>
   <a className="flex items-start gap-2 text-sm font-medium hover:underline" href={`#/conversations/${m.author_conversation_id}?round=${m.author_round_seq}`}><MessagesSquare className="mt-0.5 size-4 shrink-0"/><span className="break-words">{t('nest.childAuthor')} · {m.author_name || t('nest.deletedChild')}</span></a>
   <p className="text-sm text-muted-foreground">{t('nest.roundNotice',{n:m.author_round_seq ?? 0})}</p>
   <a className="w-fit text-xs underline" href={`#/conversations/${m.author_conversation_id}?round=${m.author_round_seq}`}>{t('nest.readRound')}</a>
   {targetLabel}
  </div>;
 }
 if(m.author_role==='user') {
  return <div className="ml-auto grid min-w-0 max-w-[85%] gap-1.5 rounded-2xl bg-muted px-4 py-2.5 text-sm" title={fmtTime(m.created_at)} data-message-id={m.id}>
    {targetLabel}<div className="whitespace-pre-wrap break-words">{m.content}</div>
    <MessageFiles files={m.attachments} onOpen={onOpenFile} />
  </div>;
 }
 return <div className="grid min-w-0 grid-cols-1 gap-1.5" title={fmtTime(m.created_at)} data-message-id={m.id}>
  {targetLabel}
  {m.author_agent_id && <div className="text-xs font-medium text-muted-foreground">@{m.author_agent_id}{m.author_name && ` · ${m.author_name}`}</div>}

  <Markdown>{m.content}</Markdown><MessageFiles files={m.attachments} onOpen={onOpenFile}/>
 </div>;
}

function QuestionView({ q, conversationId }: { q: Question; conversationId: string }) {
 const [answers,setAnswers]=useState<Record<string,string>>({});
 const p=q.payload??{};
 const reply=useMutation({mutationFn:(answer:unknown)=>api(`/api/conversations/${conversationId}/runs/${q.run_id}/requests/${encodeURIComponent(q.request_id)}/reply`,{method:'POST',body:{answer}}),onSuccess:()=>void refresh()});
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

function RunView({run,events,questions,conversationId}:{run:Run;events:ConversationEvent[];questions:Question[];messages:Message[];deliveries:Delivery[];conversationId:string}){
 const cancel=useMutation({mutationFn:()=>api(`/api/conversations/${conversationId}/runs/${run.id}/cancel`,{method:'POST'}),onSuccess:()=>void refresh()});
 const mine=events.filter(e=>e.run_id===run.id);
 const active=['running','waiting'].includes(run.status);
 return <section data-run-id={run.id} aria-label={`run @${run.agent_id}`}><Dialog.Root>
  <Dialog.Trigger asChild><button className="inline-flex max-w-full items-center gap-2 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted">{active&&<LoaderCircle className="size-3.5 animate-spin"/>}<span>@{run.agent_id}</span><span>{run.status}</span><ChevronRight className="size-3.5"/></button></Dialog.Trigger>
  <Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-50 bg-black/50"/><Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[85dvh] w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border bg-background p-4 shadow-lg" data-run-detail={run.id}>
   <div className="flex items-start justify-between"><div><Dialog.Title className="font-semibold">@{run.agent_id} · Run details</Dialog.Title><Dialog.Description className="mt-1 text-xs text-muted-foreground">{run.status} · {run.harness} · {run.harness_revision}</Dialog.Description></div><Dialog.Close asChild><Button size="icon" variant="ghost" aria-label="关闭"><X className="size-4"/></Button></Dialog.Close></div>
   <dl className="my-4 grid grid-cols-[auto_minmax(0,1fr)] gap-2 text-xs"><dt>Run</dt><dd className="break-all">{run.id}</dd><dt>Client session</dt><dd className="break-all">{run.session_id}</dd><dt>Device</dt><dd className="break-all">{run.device_id}</dd></dl>
   {run.output&&<Markdown>{run.output}</Markdown>}
   {run.error&&<p className="text-sm text-destructive">{run.error}</p>}
   <div className="my-4 grid gap-3">{questions.filter(q=>q.run_id===run.id).map(q=><QuestionView key={q.request_id} q={q} conversationId={conversationId}/>)}</div>
   {active&&<Button size="sm" variant="outline" disabled={cancel.isPending} onClick={()=>cancel.mutate()}>取消此 run</Button>}
   {cancel.isSuccess&&active&&<p className="mt-2 text-xs text-muted-foreground">取消请求已发送，等待 client 上报结果</p>}
   {cancel.isError&&<ErrorState error={cancel.error}/>}
   <div className="mt-4 grid gap-3">{mine.map(e=><div key={e.id} className="rounded border p-3 text-xs"><span className="text-muted-foreground">{e.kind}</span><pre className="mt-2 whitespace-pre-wrap break-words">{JSON.stringify(e.payload,null,2)}</pre></div>)}</div>
  </Dialog.Content></Dialog.Portal>
 </Dialog.Root></section>;
}
