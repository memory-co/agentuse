import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Check, ChevronRight, Circle, CircleCheck, LoaderCircle, RotateCcw, Trash2, X } from 'lucide-react';
import { MentionComposer } from '@/components/MentionComposer';
import { Attachments } from '@/components/Attachments';
import { uploadAttachment } from '@/lib/attachments';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Message, Question, Session, SessionDetail as Detail, SessionEvent, SessionStatus, Run, Delivery } from '@/lib/types';
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

function useMessageKey(){
 const ref=useRef<{body:string;key:string}|null>(null);
 return {key:(body:unknown)=>{const encoded=JSON.stringify(body);if(ref.current?.body!==encoded)ref.current={body:encoded,key:`message-${Date.now()}-${Math.random().toString(36).slice(2)}`};return ref.current!.key;},reset:()=>{ref.current=null;}};
}

/** 新会话：选 agent,写第一句话。 */
export function NewSession({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
 const messageKey=useMessageKey();
  const [files, setFiles] = useState<File[]>([]);
  const [progress, setProgress] = useState('');
  const draft = useRef<string | null>(null);
  const uploaded = useRef(new Set<File>());
  const [draftId, setDraftId] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: async ({prompt, targets}: {prompt:string;targets:string[]}) => {
      if (!draft.current) {
        const r = await api<{session: Session}>('/api/sessions', {method: 'POST', body: {mode:"multi_agent"}});
        draft.current = r.session.id; setDraftId(r.session.id); void refresh();
      }
      const id = draft.current;
      try {
        for (const file of files) {
          if (uploaded.current.has(file)) continue;
          await uploadAttachment(id, file, n => setProgress(`${file.name} ${n}%`)); uploaded.current.add(file);
        }
        const body={prompt,target_agent_ids:targets};await api(`/api/sessions/${id}/messages`, {method: 'POST', body,headers:{"Idempotency-Key":messageKey.key(body)}});
        return {session: {id} as Session};
      } finally { setProgress(''); }
    },
    onSuccess: r => { void refresh(); onCreated(r.session.id); },
  });
  return <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-4 py-10">
    <div className="grid gap-1 text-center">
      <h1 className="text-2xl font-semibold">{t('session.ask')}</h1>
      <p className="text-sm text-muted-foreground">{t('mention.shared')}</p>
    </div>
    <MentionComposer showAgentPicker agents={agents.data??[]} placeholder={t('session.taskPlaceholder')} busy={add.isPending} onSend={(prompt,targets)=>add.mutateAsync({prompt,targets})}>
      <label className="cursor-pointer text-xs text-muted-foreground">{t('attachment.upload')}
        <input type="file" multiple disabled={add.isPending} className="sr-only" onChange={e => setFiles(Array.from(e.target.files ?? []))} />
      </label>
      {files.length > 0 && <span className="min-w-0 break-all text-xs text-muted-foreground">{t('attachment.selected', {n: files.length})}: {files.map(f => f.name).join(', ')}</span>}
    </MentionComposer>
    {progress && <p role="status" className="text-center text-xs">{progress}</p>}
    {draftId && <div className="grid gap-1 text-center text-xs"><p>{t('attachment.created')}</p><a className="underline" href={`#/s/${draftId}`}>{t('attachment.open')}</a></div>}
    {add.isError && <div className="text-center"><ErrorState error={add.error} /></div>}
    {agents.isError && <ErrorState error={agents.error} />}
  </div>;
}

/** 会话页：上面是消息流，下面是回复框。工作页里协调者的对话也用它(placeholder 换掉，不能删、不能标记完成)。 */
export function Conversation({ id, onDeleted, placeholder,defaultAgent }: { id: string; onDeleted: () => void; placeholder?: string; defaultAgent?:string }) {
  const t = useT();
 const agents=useAgents();
 const messageKey=useMessageKey();
  const q = useQuery({ queryKey: ['sessions', id], queryFn: () => api<Detail>(`/api/sessions/${id}`), refetchInterval: 3000 });
  const reply = useMutation({
    mutationFn: ({prompt,targets}: {prompt:string;targets?:string[]}) => {const body={prompt,target_agent_ids:targets};return api(`/api/sessions/${id}/messages`, { method: 'POST', body,headers:{"Idempotency-Key":messageKey.key(body)} });},
    onSuccess: () => {messageKey.reset(); void refresh(); },
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
  const lastUserMessage = [...messages].reverse().find(message => message.role === 'user' && message.target_agent_ids?.length);
  const initialTargets = lastUserMessage ? lastUserMessage.target_agent_ids ?? [] : defaultAgent ? [defaultAgent] : [];
  const meta = t("mention.shared");
  const coordinator = x.work_role === 'coordinator';
  const settled = x.status === 'idle' || x.status === 'resolved';
  return <div className="flex min-h-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid min-w-0 w-full max-w-3xl grid-cols-1 gap-6 px-4 py-6">
        <div className="flex items-start gap-2 border-b pb-3">
          <p className="min-w-0 flex-1 break-all font-mono text-xs text-muted-foreground">{meta}</p>
          {!coordinator && settled && <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs" disabled={resolve.isPending} onClick={() => resolve.mutate(x.status === 'idle')}>
            {x.status === 'idle' ? <><CircleCheck />{t('session.resolve')}</> : <><RotateCcw />{t('session.reopen')}</>}
          </Button>}
          {!coordinator && settled && <Confirm title={t('session.deleteTitle')} description={t('session.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
            <Button variant="ghost" size="icon" className="size-7 text-destructive" aria-label={t('session.delete')}><Trash2 /></Button>
          </Confirm>}
        </div>
        <Attachments key={id} session={id} running={!settled} />
        {messages.map(m=><div key={m.id} className="grid min-w-0 grid-cols-1 gap-2"><MessageView m={m}/>
        {m.role==='user'&&<div className="flex flex-wrap gap-2">{(q.data.deliveries??[]).filter(d=>d.message_id===m.id && !(q.data.runs??[]).some(run=>run.id===d.run_id && run.message_id===m.id)).map(d=><span key={d.id} className="rounded-md border px-2 py-1 text-xs" title={d.error}>@{d.agent_key} · {d.kind==='steer'?t('mention.steer')+' · ':''}{t(`mention.${d.status}`)}</span>)}</div>}
        {(q.data.runs??[]).filter(r=>r.message_id===m.id).map(run=><RunView key={run.id} run={run} events={events} questions={questions} messages={messages} deliveries={q.data.deliveries ?? []} sessionId={id}/>)}
        </div>)}
        <div ref={bottom} />
      </div>
    </div>
    <div className="mx-auto w-full max-w-3xl px-4 pb-4">
      {reply.isError && <div className="pb-2"><ErrorState error={reply.error} /></div>}
      <MentionComposer key={id} agents={agents.data??[]} initialTargets={initialTargets} placeholder={placeholder??t('session.replyPlaceholder')} busy={reply.isPending} onSend={(prompt,targets)=>reply.mutateAsync({prompt,targets})}/>

    </div>
  </div>;
}

function MessageView({ m }: { m: Message }) {
 const t=useT();const c=m.content||{};
 if(m.role==='user') {
  const text=typeof c.prompt==='string'?c.prompt:JSON.stringify(c,null,2);
  const targets = m.target_agent_ids ?? [];
  const targetLabel = <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground" data-message-targets={m.id}>
    {targets.length ? targets.map(agent => <span key={agent} className="break-all font-medium">@{agent}</span>) : <span>{t('mention.noteOnly')}</span>}
  </div>;
  return typeof c.from_session === 'string'
    ? <div className="grid min-w-0 gap-1.5" data-message-id={m.id}><ThreadNote m={m}/>{targetLabel}</div>
    : <div className="ml-auto grid min-w-0 max-w-[85%] gap-1.5 rounded-2xl bg-muted px-4 py-2.5 text-sm" title={fmtTime(m.created_at)} data-message-id={m.id}>
        {targetLabel}
        <div className="whitespace-pre-wrap break-words">{text}</div>
      </div>;
 }
  const out = c.ok ? (typeof c.output === 'string' ? c.output : JSON.stringify(c, null, 2)) : (c.error || t('session.failed')) + (c.output ? '\n' + c.output : '');
  return <div className="grid min-w-0 grid-cols-1 gap-1.5" title={fmtTime(m.created_at)}>
    {m.agent_key&&<div className="text-xs font-medium text-muted-foreground">@{m.agent_key}{m.agent_name&&` · ${m.agent_name}`}</div>}
    {!c.ok && <div><StatusBadge tone="bad">{t('session.failed')}</StatusBadge></div>}
    <div className={cn('min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere] text-sm leading-7', !c.ok && 'text-destructive')}>{out}</div>
  </div>;
}

/** 线程回完后发给协调者的消息：显示成一张小卡片，点了进线程(docs/v2/designs/work.md)。 */
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

type Step = { text: string; status: string };

/** 计划：harness 列的步骤清单，显示最新一份(docs/v2/designs/plan.md)。 */
function PlanView({ steps }: { steps?: Step[] }) {
  if (!Array.isArray(steps) || !steps.length) return null;
  return <ul className="grid gap-1.5 rounded-lg border px-3 py-2.5 text-sm">
    {steps.map((s, i) => <li key={i} className="flex items-start gap-2">
      {s.status === 'completed' ? <Check className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        : s.status === 'in_progress' ? <LoaderCircle className="mt-0.5 size-4 shrink-0 animate-spin" />
        : <Circle className="mt-0.5 size-4 shrink-0 text-muted-foreground/60" />}
      <span className={cn('min-w-0 break-words', s.status === 'completed' && 'text-muted-foreground', s.status === 'in_progress' && 'font-medium')}>{s.text}</span>
    </li>)}
  </ul>;
}

/** 提问:harness 用 AskUserQuestion 问用户，选选项或自己写(docs/v2/designs/question.md)。 */
function QuestionView({ q, sessionId }: { q: Question; sessionId: string }) {
  const t = useT();
  const [picked, setPicked] = useState<Record<string, string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const answer = useMutation({
    mutationFn: (answers: Record<string, string>) => api(`/api/sessions/${sessionId}/runs/${q.run_id}/questions/${q.id}/answer`, { method: 'POST', body: { answers } }),
    onSuccess: () => { void refresh(); },
  });
  if (q.status !== 'pending' || q.answers) {
    const note = q.status === 'answered' ? 'question.answered' : q.status === 'expired' && !q.answers ? 'question.expired' : 'question.sending';
    return <div className="grid gap-1 rounded-lg border px-3 py-2.5 text-sm">
      {q.questions.map(it => <div key={it.question} className="grid gap-0.5">
        <span className="break-words"><span className="text-muted-foreground">{t('question.ask')}:</span>{it.question}</span>
        {q.answers?.[it.question] && <span className="break-words"><span className="text-muted-foreground">{t('question.answer')}:</span>{q.answers[it.question]}</span>}
      </div>)}
      <span className="text-[11px] text-muted-foreground">{t(note)}</span>
    </div>;
  }
  const value = (it: Question['questions'][number]) => [...(picked[it.question] ?? []), ...(other[it.question]?.trim() ? [other[it.question].trim()] : [])].join(', ');
  const ready = q.questions.every(it => value(it) !== '');
  const toggle = (it: Question['questions'][number], label: string) => setPicked(p => {
    const cur = p[it.question] ?? [];
    const next = cur.includes(label) ? cur.filter(l => l !== label) : it.multi_select ? [...cur, label] : [label];
    return { ...p, [it.question]: next };
  });
  return <form className="grid gap-3 rounded-lg border border-amber-500/50 px-3 py-3 text-sm" onSubmit={e => {
    e.preventDefault();
    if (ready) answer.mutate(Object.fromEntries(q.questions.map(it => [it.question, value(it)])));
  }}>
    {q.questions.map(it => <fieldset key={it.question} className="grid gap-2">
      <legend className="mb-1 break-words font-medium">{it.header && <span className="mr-2 text-xs text-muted-foreground">{it.header}</span>}{it.question}</legend>
      <div className="flex flex-wrap gap-2">
        {it.options?.map(o => <Button key={o.label} type="button" size="sm" title={o.description}
          variant={(picked[it.question] ?? []).includes(o.label) ? 'default' : 'outline'} onClick={() => toggle(it, o.label)}>{o.label}</Button>)}
      </div>
      <Input className="h-8 text-xs" placeholder={t('question.other')} value={other[it.question] ?? ''}
        onChange={e => { const v = e.target.value; setOther(o => ({ ...o, [it.question]: v })); if (!it.multi_select && v) setPicked(p => ({ ...p, [it.question]: [] })); }} />
    </fieldset>)}
    <div><Button type="submit" size="sm" disabled={!ready || answer.isPending}>{t('question.submit')}</Button></div>
  </form>;
}

function RunView({ run, events, questions, messages, deliveries, sessionId }: { run: Run; events: SessionEvent[]; questions: Question[]; messages: Message[]; deliveries: Delivery[]; sessionId: string }) {
  const t = useT();
  const cancel = useMutation({
    mutationFn: () => api(`/api/sessions/${sessionId}/runs/${run.id}/cancel`, { method: 'POST' }),
    onSuccess: () => { void refresh(); },
  });
  const mine = events.filter(event => event.run_id === run.id);
  const progress = mine.filter(event => event.kind === 'progress');
  const steers = deliveries.filter(delivery => delivery.run_id === run.id && delivery.kind === 'steer')
    .map(delivery => ({ delivery, message: messages.find(message => message.id === delivery.message_id) }))
    .sort((a, b) => a.delivery.message_id - b.delivery.message_id);
  const plan = mine.filter(event => event.kind === 'plan').pop();
  const active = ['pending', 'running', 'waiting'].includes(run.status);
  return <section data-run-id={run.id} aria-label={`run @${run.agent_key}`}>
    <Dialog.Root>
      <Dialog.Trigger asChild>
        <button type="button" className="inline-flex max-w-full items-center gap-2 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground" aria-label={t('mention.detailsFor', { agent: run.agent_key })}>
          {active && <LoaderCircle className={cn('size-3.5 shrink-0', run.status === 'running' && 'animate-spin')} />}
          <span className="truncate font-medium">@{run.agent_key}</span>
          <span className="shrink-0">{t(`mention.${run.status}`)}</span>
          <ChevronRight className="size-3.5 shrink-0" />
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 grid max-h-[85dvh] w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 grid-rows-[auto_minmax(0,1fr)] rounded-xl border bg-background shadow-lg" data-run-detail={run.id}>
          <div className="flex items-start gap-3 border-b p-4">
            <div className="min-w-0 flex-1">
              <Dialog.Title className="break-words font-semibold">@{run.agent_key} · {t('mention.details')}</Dialog.Title>
              <Dialog.Description className="mt-1 text-sm text-muted-foreground">{t(`mention.${run.status}`)}</Dialog.Description>
            </div>
            <Dialog.Close asChild><Button variant="ghost" size="icon" className="size-7 shrink-0" aria-label={t('common.close')}><X className="size-4" /></Button></Dialog.Close>
          </div>
          <div className="grid min-h-0 gap-4 overflow-y-auto p-4 text-xs">
            <p className="break-all text-muted-foreground">{run.harness} · {run.daemon_id ?? '—'}</p>
            {run.error && run.status !== 'cancelled' && <p className="break-words text-destructive">{run.error}</p>}
            {steers.length > 0 && <section className="grid min-w-0 gap-2" aria-label={t('mention.steerMessages')}>
              <h3 className="font-medium">{t('mention.steerMessages')}</h3>
              {steers.map(({ delivery, message }) => <div key={delivery.id} className="grid min-w-0 gap-2 rounded-lg border p-3" data-steer-message={delivery.message_id}>
                <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
                  <span>@{delivery.agent_key}</span>
                  <span>{t(`mention.${delivery.status}`)}</span>
                  {message && <time className="ml-auto" dateTime={new Date(message.created_at * 1000).toISOString()}>{fmtTime(message.created_at)}</time>}
                </div>
                {message && <p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{typeof message.content?.prompt === 'string' ? message.content.prompt : JSON.stringify(message.content, null, 2)}</p>}
                {delivery.error && <p className="break-words text-destructive">{delivery.error}</p>}
              </div>)}
            </section>}
            {plan && <PlanView steps={plan.payload?.steps} />}
            {questions.filter(question => question.run_id === run.id).map(question => <QuestionView key={question.id} q={question} sessionId={sessionId} />)}
            {progress.length > 0 && <div className="grid min-w-0 gap-2">
              <p className="font-medium">{t('session.progress', { n: progress.length })}</p>
              <div className="grid gap-1 font-mono">{progress.map(event => <p className="[overflow-wrap:anywhere]" key={event.id}>{evText(event.payload)}</p>)}</div>
            </div>}
            {cancel.isError && <ErrorState error={cancel.error} />}
            {active && <div><Button variant="outline" size="sm" disabled={cancel.isPending} onClick={() => cancel.mutate()}>{t('mention.cancel')}</Button></div>}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </section>;
}
