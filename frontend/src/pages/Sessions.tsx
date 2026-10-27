import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ArrowUp, Check, ChevronRight, Circle, LoaderCircle, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Message, Question, Session, SessionDetail as Detail, SessionEvent, SessionStatus, Steer } from '@/lib/types';
import { cn, fmtTime } from '@/lib/utils';
import { t, useT } from '@/lib/i18n';

export const STATUS_TONE: Record<SessionStatus, 'ok' | 'warn' | 'run'> = { idle: 'ok', pending: 'warn', running: 'run', waiting: 'warn' };
export const statusLabel = (s: SessionStatus) => t(`session.status.${s}`);
export const useSessions = () => useQuery({ queryKey: ['sessions'], queryFn: () => api<Session[]>('/api/sessions'), refetchInterval: 3000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['sessions'] });
// progress 回报只有 {text} 时直接显示文字，否则显示 JSON。
const evText = (p: any) => (p && typeof p === 'object' && Object.keys(p).length === 1 && 'text' in p ? String(p.text) : JSON.stringify(p ?? ''));

/** 底部输入框:回车发送,Shift+回车换行;children 放在输入框下面一行(选 agent)。 */
function Composer({ placeholder, disabled, busy, onSend, children }: {
  placeholder: string; disabled?: boolean; busy?: boolean; onSend: (text: string) => Promise<unknown>; children?: ReactNode;
}) {
  const t = useT();
  const [text, setText] = useState('');
  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    if (!text.trim() || disabled || busy) return;
    void onSend(text).then(() => setText(''), () => undefined);
  };
  return <form onSubmit={submit} className="rounded-xl border bg-background p-2 shadow-sm focus-within:ring-1 focus-within:ring-ring">
    <Textarea
      value={text} onChange={e => setText(e.target.value)} placeholder={placeholder} disabled={disabled}
      enterKeyHint="send"
      onKeyDown={e => {                                              // 输入法选词时的 Enter 不算(Safari 上 isComposing 已复位，靠 keyCode 229 兜底)
        if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) { e.preventDefault(); submit(); }
      }}
      className="max-h-60 min-h-[44px] resize-none border-0 p-2 shadow-none focus-visible:ring-0"
    />
    <div className="flex flex-wrap items-center gap-2">
      {children}
      <Button type="submit" size="icon" className="ml-auto size-8 rounded-full" disabled={disabled || busy || !text.trim()} aria-label={t('session.send')}><ArrowUp /></Button>
    </div>
  </form>;
}

/** 新会话：选 agent,写第一句话。 */
export function NewSession({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
  const [agent, setAgent] = useState('');
  useEffect(() => { if (!agent && agents.data?.length) setAgent(agents.data[0].id); }, [agent, agents.data]);
  const add = useMutation({
    mutationFn: (prompt: string) => api<{ session: Session }>('/api/sessions', { method: 'POST', body: { agent_id: agent.trim(), prompt } }),
    onSuccess: r => { void refresh(); onCreated(r.session.id); },
  });
  return <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-4 py-10">
    <div className="grid gap-1 text-center">
      <h1 className="text-2xl font-semibold">{t('session.ask')}</h1>
      <p className="text-sm text-muted-foreground">{t('session.askText')}</p>
    </div>
    <Composer placeholder={t('session.taskPlaceholder')} disabled={!agent.trim()} busy={add.isPending} onSend={p => add.mutateAsync(p)}>
      <Input className="h-8 w-52 font-mono text-xs" list="agent-ids" placeholder={t('session.agentPlaceholder')} value={agent} onChange={e => setAgent(e.target.value)} aria-label="agent" />
      <datalist id="agent-ids">{agents.data?.map(a => <option key={a.id} value={a.id}>{a.name} ({a.harness})</option>)}</datalist>
    </Composer>
    {add.isError && <div className="text-center"><ErrorState error={add.error} /></div>}
    {agents.isSuccess && !agents.data.length && <p className="text-center text-sm text-muted-foreground">{t('session.noAgents')}</p>}
  </div>;
}

/** 会话页：上面是消息流，下面是回复框。 */
export function Conversation({ id, onDeleted }: { id: string; onDeleted: () => void }) {
  const t = useT();
  const q = useQuery({ queryKey: ['sessions', id], queryFn: () => api<Detail>(`/api/sessions/${id}`), refetchInterval: 3000 });
  const reply = useMutation({
    mutationFn: (prompt: string) => api(`/api/sessions/${id}/messages`, { method: 'POST', body: { prompt } }),
    onSuccess: () => { void refresh(); },
  });
  const del = useMutation({ mutationFn: () => api(`/api/sessions/${id}`, { method: 'DELETE' }), onSuccess: () => { void refresh(); onDeleted(); } });
  const bottom = useRef<HTMLDivElement>(null);
  const count = (q.data?.messages.length ?? 0) + (q.data?.events.length ?? 0);
  useEffect(() => { bottom.current?.scrollIntoView({ block: 'end' }); }, [count]);
  if (q.isPending) return <div className="px-6"><Loading /></div>;
  if (q.isError) return <div className="px-6 py-6"><ErrorState error={q.error} /></div>;
  const { session: x, messages, events } = q.data;
  const steers = q.data.steers ?? [];
  const questions = q.data.questions ?? [];
  const meta = [x.agent_id, t('session.metaHarness', { harness: x.harness }), x.created_by !== x.user_id && t('session.metaOnBehalf', { by: x.created_by, owner: x.user_id }), x.daemon_id && t('session.metaDaemon', { id: x.daemon_id }), x.harness_session_id && t('session.metaHarnessSession', { id: x.harness_session_id })].filter(Boolean).join(' · ');
  const last = events.filter(e => e.kind === 'progress').pop();
  return <div className="flex min-h-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid w-full max-w-3xl gap-6 px-4 py-6">
        <div className="flex items-start gap-2 border-b pb-3">
          <p className="min-w-0 flex-1 break-all font-mono text-xs text-muted-foreground">{meta}</p>
          {x.status !== 'running' && x.status !== 'waiting' && <Confirm title={t('session.deleteTitle')} description={t('session.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
            <Button variant="ghost" size="icon" className="size-7 text-destructive" aria-label={t('session.delete')}><Trash2 /></Button>
          </Confirm>}
        </div>
        {messages.map(m => <MessageView key={m.id} m={m} events={events} steers={steers} questions={questions} sessionId={x.id} />)}
        {x.status !== 'idle' && <div className="flex items-start gap-2 text-sm">
          <StatusBadge tone={STATUS_TONE[x.status]}>{statusLabel(x.status)}</StatusBadge>
          <span className="min-w-0 break-words text-muted-foreground">{q.data.daemon_offline ? t('session.daemonOffline') : last ? evText(last.payload).slice(0, 300) : ''}</span>
        </div>}
        <div ref={bottom} />
      </div>
    </div>
    <div className="mx-auto w-full max-w-3xl px-4 pb-4">
      {reply.isError && <div className="pb-2"><ErrorState error={reply.error} /></div>}
      {x.daemon_id || x.status !== 'idle'
        ? <Composer placeholder={x.status === 'idle' ? t('session.replyPlaceholder') : t('session.busyPlaceholder')} busy={reply.isPending} onSend={p => reply.mutateAsync(p)} />
        : <p className="py-2 text-center text-sm text-muted-foreground">{t('session.daemonGone')}</p>}
    </div>
  </div>;
}

function MessageView({ m, events, steers, questions, sessionId }: { m: Message; events: SessionEvent[]; steers: Steer[]; questions: Question[]; sessionId: string }) {
  const t = useT();
  const c = m.content || {};
  if (m.role === 'user') {
    const text = typeof c.prompt === 'string' ? c.prompt : JSON.stringify(c, null, 2);
    const progress = events.filter(e => e.message_id === m.id && e.kind === 'progress');
    const plan = events.filter(e => e.message_id === m.id && e.kind === 'plan').pop();
    // 插话：转成下一条消息的(queued)不再单独显示
    const mine = steers.filter(s => s.message_id === m.id && s.status !== 'queued');
    return <div className="grid gap-3">
      <div className="ml-auto max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-muted px-4 py-2.5 text-sm" title={fmtTime(m.created_at)}>{text}</div>
      {mine.map(s => <div key={s.id} className="ml-auto grid max-w-[75%] justify-items-end gap-0.5" title={fmtTime(s.created_at)}>
        <div className="whitespace-pre-wrap break-words rounded-2xl border border-dashed px-3 py-1.5 text-xs">{s.prompt}</div>
        <span className="text-[11px] text-muted-foreground">{t(s.status === 'delivered' ? 'session.steerDelivered' : 'session.steerPending')}</span>
      </div>)}
      {plan && <PlanView steps={plan.payload?.steps} />}
      {questions.filter(q => q.message_id === m.id).map(q => <QuestionView key={q.id} q={q} sessionId={sessionId} />)}
      {progress.length > 0 && <details className="group text-sm text-muted-foreground">
        <summary className="flex cursor-pointer list-none items-center gap-1 hover:text-foreground"><ChevronRight className="size-3.5 transition-transform group-open:rotate-90" />{t('session.progress', { n: progress.length })}</summary>
        <div className="mt-2 grid max-h-64 gap-1 overflow-auto border-l pl-3 font-mono text-xs">{progress.map(e => <div key={e.id} className="break-words">{evText(e.payload).slice(0, 600)}</div>)}</div>
      </details>}
    </div>;
  }
  const out = c.ok ? (typeof c.output === 'string' ? c.output : JSON.stringify(c, null, 2)) : (c.error || t('session.failed')) + (c.output ? '\n' + c.output : '');
  return <div className="grid gap-1.5" title={fmtTime(m.created_at)}>
    {!c.ok && <div><StatusBadge tone="bad">{t('session.failed')}</StatusBadge></div>}
    <div className={cn('whitespace-pre-wrap break-words text-sm leading-7', !c.ok && 'text-destructive')}>{out}</div>
  </div>;
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
    mutationFn: (answers: Record<string, string>) => api(`/api/sessions/${sessionId}/questions/${q.id}/answer`, { method: 'POST', body: { answers } }),
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
