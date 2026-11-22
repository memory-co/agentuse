import { Markdown } from '@/components/Markdown';
import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Bot, ChevronRight, CircleCheck, FolderOpen, LoaderCircle, MessagesSquare, RotateCcw, Trash2, UserRound, X } from 'lucide-react';
import { MentionComposer } from '@/components/MentionComposer';
import { FileManager } from '@/components/FileManager';
import { listAttachments } from '@/lib/attachments';
import { useMessageFiles } from '@/hooks/use-message-files';
import { MessageFiles } from '@/components/MessageFiles';
import { RunEvents } from '@/components/RunEvents';
import { RunTokenUsage } from '@/components/RunTokenUsage';
import { Sheet, SheetContent, SheetTitle, SheetDescription } from '@/components/ui/sheet';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { useActAs } from '@/lib/actas';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Message, Question, Conversation, ConversationDetail as Detail, ConversationEvent, ConversationStatus, ConversationRound, Run, RunInput, User, Attachment } from '@/lib/types';
import { cn, fmtTime } from '@/lib/utils';
import { t, useT } from '@/lib/i18n';

export const STATUS_TONE: Record<ConversationStatus, 'ok' | 'warn' | 'run' | 'muted'> = { idle: 'ok', pending: 'warn', running: 'run', waiting: 'warn', resolved: 'muted' };
export const statusLabel = (s: ConversationStatus) => t(`conversation.status.${s}`);
export const useConversations = () => useQuery({ queryKey: ['conversations'], queryFn: () => api<Conversation[]>('/api/conversations') });
export const useChildren = (parent: string, enabled=true) => useQuery({
  queryKey: ['conversations', 'children', parent], queryFn: () => api<Conversation[]>(`/api/conversations?parent_conversation_id=${encodeURIComponent(parent)}`), enabled,
});
const refresh = () => queryClient.invalidateQueries({ queryKey: ['conversations'] });

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
  return <div className="conversation-column flex flex-1 flex-col justify-center gap-7 py-10">
    <div className="grid gap-2">
      <h1 className="text-[1.75rem] font-semibold tracking-tight sm:text-4xl">{t('conversation.ask')}</h1>
      <p className="text-sm leading-6 text-muted-foreground sm:text-base">{t('mention.shared')}</p>
    </div>
    <fieldset className="min-w-0" disabled={add.isPending||!!draftId}>
      <legend className="mb-2 text-xs font-medium text-muted-foreground">{t('nest.mode')}</legend>
      <div className="grid gap-2 sm:grid-cols-2">
        {(['single','collaborative'] as const).map(value=><label key={value} className={cn('flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors', mode === value ? 'border-primary/40 bg-accent/60' : 'bg-panel hover:bg-muted')}>
          <input className="mt-1 accent-primary" type="radio" name="conversation-mode" value={value} checked={mode===value} onChange={()=>setMode(value)}/>
          <span className="grid gap-1"><span className="text-sm font-semibold">{t(`nest.${value}`)}</span><span className="text-xs leading-5 text-muted-foreground">{t(`nest.${value}Hint`)}</span></span>
        </label>)}
      </div>
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
  const recentMessages = [...messages].reverse();
  const lastSent = recentMessages.find(message => message.author_role === 'user' && message.author_user_id === sender);
  const initialTargets = lastSent?.to_agent_ids ?? (x.parent_conversation_id
    ? recentMessages.find(message => message.author_role === 'agent' && message.to_agent_ids.length > 0)?.to_agent_ids
    : undefined);
  const questions = q.data.questions ?? [];
  const meta = t(`nest.${x.mode}`);
  const settled = x.status === 'idle' || x.status === 'resolved';
  return <div className="flex min-h-0 flex-1 overflow-hidden">
   <div className="flex min-h-0 min-w-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="conversation-column grid grid-cols-1 gap-7 py-5 sm:gap-8 sm:py-7">
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-b pb-3">
          <p className="text-xs font-medium text-muted-foreground">{meta}</p>
          <div className="ml-auto flex max-w-full flex-wrap items-center justify-end gap-1">
            <Button variant="ghost" size="sm" className="gap-1.5 px-2" aria-label={t('attachment.files')} aria-haspopup="dialog" onClick={() => openFiles()}><FolderOpen />{t('attachment.files')}</Button>
            {x.mode === 'collaborative' && <Button variant="ghost" size="sm" className="gap-1.5 px-2 xl:hidden" aria-label={t('nest.children')} aria-haspopup="dialog" onClick={() => setChildrenOpen(true)}><MessagesSquare />{t('nest.children')}<span className="text-muted-foreground">{children.data?.length ?? 0}</span></Button>}
            {selectedRound === undefined && settled && <Button variant="ghost" size="sm" className="gap-1.5 px-2" disabled={resolve.isPending} onClick={() => resolve.mutate(x.status === 'idle')}>
              {x.status === 'idle' ? <><CircleCheck />{t('conversation.resolve')}</> : <><RotateCcw />{t('conversation.reopen')}</>}
            </Button>}
            {selectedRound === undefined && settled && <Confirm title={t('conversation.deleteTitle')} description={t(x.mode==='collaborative'?'nest.deleteTree':'conversation.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
              <Button variant="ghost" size="icon" className="size-8 text-destructive" aria-label={t('conversation.delete')}><Trash2 /></Button>
            </Confirm>}
          </div>
        </div>

        {selectedRound !== undefined && <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-muted/30 p-3 text-xs"><span>{t('nest.round', {n:selectedRound})} · {t(q.data.rounds[0]?.finished_at !== undefined ? 'nest.roundEnded' : 'nest.roundActive')}</span><a className="underline" href={`#/conversations/${id}`}>{t('nest.fullConversation')}</a></div>}
        {(del.isError||resolve.isError) && <ErrorState error={del.error||resolve.error}/>}

        {messages.map(m=><div key={m.id} className="grid min-w-0 grid-cols-1 gap-2">{x.parent_conversation_id && q.data.rounds.filter(round => round.start_message_seq === m.seq).map(round => <RoundStatus key={round.seq} round={round} />)}<MessageView m={m} onOpenFile={openFiles}/>
        {(q.data.runs??[]).filter(r=>r.first_message_id===m.id).map(run=><RunView key={run.id} run={run} events={events} questions={questions} messages={messages} inputs={q.data.run_inputs.filter(input=>input.run_id===run.id)} onOpenFile={openFiles} conversationId={id}/>)}
        </div>)}
        <div ref={bottom} />
      </div>
    </div>
    {selectedRound === undefined && <div ref={composer} className="conversation-column pb-4 pt-2 sm:pb-6">
      {reply.isError && <div className="pb-2"><ErrorState error={reply.error} /></div>}
      <MentionComposer files={fileDraft.files} onFiles={fileDraft.addFiles} onRemoveFile={fileDraft.remove} key={`${id}:${sender}`} initialTargets={initialTargets} agents={agents.data??[]} users={[...new Set([x.user_id, x.created_by, ...messages.flatMap(m => [m.author_user_id, ...m.to_user_ids])].filter((value): value is string => !!value))]} placeholder={t('conversation.replyPlaceholder')} busy={reply.isPending} onSend={(prompt,targets,users)=>reply.mutateAsync({prompt,targets,users})}/>

    </div>}
   </div>
   {x.mode === 'collaborative' && <>
     <aside className="hidden w-[19rem] shrink-0 flex-col border-l xl:flex 2xl:w-[21rem]" aria-label={t('nest.children')} data-conversation-children={id}>
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
  return <section className="flex h-full min-h-0 flex-col bg-panel">
    <header className="flex h-16 shrink-0 items-center gap-2 border-b px-5">
      <MessagesSquare className="size-4 text-muted-foreground" />
      <h2 className="text-sm font-semibold tracking-tight">{t('nest.children')}</h2>
      <span className="text-xs tabular-nums text-muted-foreground">{conversations.length}</span>
      {onClose && <Button variant="ghost" size="icon" className="ml-auto size-8" aria-label={t('common.close')} onClick={onClose}><X /></Button>}
    </header>
    <div className="min-h-0 flex-1 overflow-y-auto px-4">
      {pending ? <Loading /> : error ? <ErrorState error={error} /> : conversations.length ? <div className="divide-y">
        {conversations.map(child => <a key={child.id} className="group grid min-h-14 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 px-1 py-3 transition-colors hover:bg-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring" href={`#/conversations/${child.id}`} onClick={onClose}>
          <span className="line-clamp-3 break-words text-sm font-medium leading-5">{child.title || child.id}</span>
          <StatusBadge tone={STATUS_TONE[child.status]}>{statusLabel(child.status)}</StatusBadge>
          <ChevronRight className="size-3.5 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
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
 const targetLabel = (m.author_role === 'user' || m.to_agent_ids.length > 0 || m.to_user_ids.length > 0) && <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground" data-message-targets={m.id}>
   {m.to_agent_ids.map(agent => <span key={`agent:${agent}`} className="break-all font-medium">@{agent}</span>)}
   {m.to_user_ids.map(user => <span key={`user:${user}`} className="inline-flex items-center gap-1 break-all font-medium"><UserRound className="size-3 shrink-0" />{user}</span>)}
   {m.author_role==='user' && !m.to_agent_ids.length && !m.to_user_ids.length && <span>{t('mention.noteOnly')}</span>}
 </div>;
 if(m.author_role==='child_conversation') {
  return <details className="group min-w-0 rounded-lg border bg-panel" data-child-notification={m.id}>
   <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 rounded-lg px-3 py-2 text-xs text-muted-foreground hover:bg-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
    <ChevronRight className="size-3.5 shrink-0 transition-transform group-open:rotate-90" />
    <MessagesSquare className="size-3.5 shrink-0" />
    <span className="min-w-0 flex-1 truncate" title={m.author_name || t('nest.deletedChild')}>{t('nest.childAuthor')} · {m.author_name || t('nest.deletedChild')}</span>
    <span className="shrink-0">{t('nest.round',{n:m.author_round_seq ?? 0})} · {t('nest.roundEnded')}</span>
   </summary>
   <div className="grid gap-3 border-t px-3 py-3">
    <p className="text-sm text-muted-foreground">{t('nest.roundNotice',{n:m.author_round_seq ?? 0})}</p>
    <a className="w-fit text-xs font-medium text-primary underline underline-offset-4" href={`#/conversations/${m.author_conversation_id}?round=${m.author_round_seq}`}>{t('nest.readRound')}</a>
    {targetLabel}<MessageErrors message={m}/>
   </div>
  </details>;
 }
 if(m.author_role==='user') {
  return <div className="ml-auto grid min-w-0 max-w-[90%] gap-1.5 rounded-2xl rounded-br-md border bg-muted px-4 py-3 text-sm leading-6 sm:max-w-[85%]" title={fmtTime(m.created_at)} data-message-id={m.id}>
    {targetLabel}<div className="whitespace-pre-wrap break-words">{m.content}</div>
    <MessageFiles files={m.attachments} onOpen={onOpenFile} /><MessageErrors message={m}/>
  </div>;
 }
 return <div className="grid min-w-0 grid-cols-1 gap-3" title={fmtTime(m.created_at)} data-message-id={m.id}>
  {m.author_agent_id && <div className="flex min-w-0 items-center gap-2.5 text-sm">
    <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-accent text-accent-foreground"><Bot className="size-4" aria-hidden="true" /></span>
    <span className="min-w-0 break-words [overflow-wrap:anywhere]"><span className="font-semibold">@{m.author_agent_id}</span>{m.author_name && <span className="ml-2 text-xs text-muted-foreground">{m.author_name}</span>}</span>
  </div>}
  {targetLabel}

  <Markdown>{m.content}</Markdown><MessageFiles files={m.attachments} onOpen={onOpenFile}/><MessageErrors message={m}/>
 </div>;
}

function MessageErrors({message}:{message:Message}) {
 const t=useT();
 return Object.entries(message.agent_errors ?? {}).map(([agent,receipt])=><p key={agent} className={cn('break-words text-xs',receipt.uncertain?'text-muted-foreground':'text-destructive')} data-message-error={agent}>@{agent} · {t(receipt.uncertain?'run.unconfirmed':'run.rejected')}：{receipt.error}</p>);
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

function RunView({run,events,questions,messages,inputs,conversationId,onOpenFile}:{run:Run;events:ConversationEvent[];questions:Question[];messages:Message[];inputs:RunInput[];conversationId:string;onOpenFile:(file:Attachment)=>void}){
 const t=useT();
 const [open,setOpen]=useState(false);
 const cancel=useMutation({mutationFn:()=>api(`/api/conversations/${conversationId}/runs/${run.id}/cancel`,{method:'POST'}),onSuccess:()=>void refresh()});
 const mine=events.filter(e=>e.run_id===run.id);
 const active=['running','waiting'].includes(run.status);
 return <section data-run-id={run.id} aria-label={`${t('run.label')} @${run.agent_id}`}><Dialog.Root open={open} onOpenChange={setOpen}>
  <Dialog.Trigger asChild><button className="flex max-w-full flex-wrap items-center gap-x-3 gap-y-1.5 rounded-md bg-panel px-3 py-2 text-left text-xs text-muted-foreground transition-colors hover:bg-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring" data-run-summary>
   <span className="inline-flex shrink-0 items-center gap-2">{active&&<LoaderCircle className="size-3.5 animate-spin"/>}<span>{t('run.label')} · @{run.agent_id}</span><span>{t(`run.status.${run.status}`)}</span><ChevronRight className="size-3.5"/></span>
   <span className="inline-flex min-w-0 max-w-full flex-wrap gap-x-3 gap-y-1">
    <span>{t('run.harness')} <span className="font-medium text-foreground/80" data-run-summary-harness>{run.harness}</span></span>
    <span className="min-w-0 break-words [overflow-wrap:anywhere]">{t('run.model')} <span className="font-medium text-foreground/80" data-run-summary-model>{run.model ?? t('run.modelUnknown')}</span></span>
   </span>
   <RunTokenUsage usage={run.usage} compact />
  </button></Dialog.Trigger>
  <Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-50 bg-black/50"/><Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[85dvh] w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 overflow-x-hidden overflow-y-auto rounded-xl border bg-background p-4 shadow-lg [overflow-wrap:anywhere]" data-run-detail={run.id}>
   <div className="flex items-start justify-between gap-3"><div className="min-w-0"><Dialog.Title className="font-semibold">@{run.agent_id} · {t('run.details')}</Dialog.Title><Dialog.Description className="mt-1 text-xs text-muted-foreground">{t(`run.status.${run.status}`)} · {run.harness} · {run.harness_revision}</Dialog.Description></div><Dialog.Close asChild><Button size="icon" variant="ghost" className="shrink-0" aria-label={t('common.close')}><X className="size-4"/></Button></Dialog.Close></div>
   <RunTokenUsage usage={run.usage} />
   <dl className="my-4 grid grid-cols-[auto_minmax(0,1fr)] gap-2 text-xs"><dt>{t('run.id')}</dt><dd className="break-all">{run.id}</dd><dt>{t('run.model')}</dt><dd className="break-all" data-run-model>{run.model ?? t('run.modelUnknown')}</dd><dt>{t('run.clientSession')}</dt><dd className="break-all">{run.session_id}</dd><dt>{t('run.device')}</dt><dd className="break-all">{run.device_id}</dd></dl>
   <section className="my-4 grid grid-cols-1 gap-2" aria-label={t('run.inputs')}>
    <h3 className="text-sm font-medium">{t('run.inputs')} · {inputs.length}</h3>
    {inputs.map(input=>{
     const message=messages.find(m=>m.id===input.message_id);
     return <details key={input.input_id} className="group/input rounded-lg border" data-run-input={input.input_id}>
      <summary className="flex cursor-pointer list-none items-center gap-2 rounded-lg p-3 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
       <ChevronRight className="size-3.5 shrink-0 transition-transform group-open/input:rotate-90" />
       <span className="line-clamp-2 min-w-0 flex-1">{message?.author_role==='child_conversation' ? `${t('nest.childAuthor')} · ${message.author_name || t('nest.deletedChild')}` : message?.content || input.message_id}</span>
       <span className="hidden shrink-0 text-muted-foreground sm:inline">{fmtTime(input.created_at)}</span>
      </summary>
      <div className="grid grid-cols-1 gap-3 border-t p-3 text-sm">
       <p className="break-all text-xs text-muted-foreground">{t('run.inputId')} · {input.input_id}</p>
       <p className="text-xs text-muted-foreground sm:hidden">{fmtTime(input.created_at)}</p>
       {message && <><Markdown>{message.content}</Markdown><MessageFiles files={message.attachments} onOpen={file=>{setOpen(false);onOpenFile(file);}} /></>}
      </div>
     </details>;
    })}
   </section>
   {run.output&&<Markdown>{run.output}</Markdown>}
   {run.error&&<p className="text-sm text-destructive">{run.error}</p>}
   <div className="my-4 grid grid-cols-1 gap-3">{questions.filter(q=>q.run_id===run.id).map(q=><QuestionView key={q.request_id} q={q} conversationId={conversationId}/>)}</div>
   {active&&<Button size="sm" variant="outline" disabled={cancel.isPending} onClick={()=>cancel.mutate()}>{t('run.cancel')}</Button>}
   {cancel.isSuccess&&active&&<p className="mt-2 text-xs text-muted-foreground">{t('run.cancelPending')}</p>}
   {cancel.isError&&<ErrorState error={cancel.error}/>}
   <RunEvents events={mine} />
  </Dialog.Content></Dialog.Portal>
 </Dialog.Root></section>;
}
