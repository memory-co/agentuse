import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ArrowUp, ChevronRight, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Message, Session, SessionDetail as Detail, SessionEvent, SessionStatus } from '@/lib/types';
import { cn, fmtTime } from '@/lib/utils';
import { t, useT } from '@/lib/i18n';

export const STATUS_TONE: Record<SessionStatus, 'ok' | 'warn' | 'run'> = { idle: 'ok', pending: 'warn', running: 'run' };
export const statusLabel = (s: SessionStatus) => t(`session.status.${s}`);
export const useSessions = () => useQuery({ queryKey: ['sessions'], queryFn: () => api<Session[]>('/api/sessions'), refetchInterval: 3000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['sessions'] });
// progress 回报只有 {text} 时直接显示文字，否则显示 JSON。
const evText = (p: any) => (p && typeof p === 'object' && Object.keys(p).length === 1 && 'text' in p ? String(p.text) : JSON.stringify(p ?? ''));

/** 底部输入框:回车发送,Shift+回车换行;children 放在输入框下面一行(选 agent、工作目录)。 */
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

/** 新会话：选 agent、可选工作目录，写第一句话。 */
export function NewSession({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
  const [agent, setAgent] = useState('');
  const [cwd, setCwd] = useState('');
  useEffect(() => { if (!agent && agents.data?.length) setAgent(agents.data[0].id); }, [agent, agents.data]);
  const add = useMutation({
    mutationFn: (prompt: string) => api<{ session: Session }>('/api/sessions', { method: 'POST', body: { agent_id: agent.trim(), prompt, cwd: cwd.trim() } }),
    onSuccess: r => { void refresh(); onCreated(r.session.id); },
  });
  return <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-4 py-10">
    <div className="grid gap-1 text-center">
      <h1 className="text-2xl font-semibold">{t('session.ask')}</h1>
      <p className="text-sm text-muted-foreground">{t('session.askText')}</p>
    </div>
    <Composer placeholder={t('session.taskPlaceholder')} disabled={!agent.trim()} busy={add.isPending} onSend={p => add.mutateAsync(p)}>
      <Input className="h-8 w-52 font-mono text-xs" list="agent-ids" placeholder={t('session.agentPlaceholder')} value={agent} onChange={e => setAgent(e.target.value.replace(/^@/, ''))} aria-label="agent" />
      <datalist id="agent-ids">{agents.data?.map(a => <option key={a.id} value={a.id}>{a.name} ({a.harness})</option>)}</datalist>
      <Input className="h-8 min-w-40 flex-1 text-xs" placeholder={t('session.cwdPlaceholder')} value={cwd} onChange={e => setCwd(e.target.value)} aria-label={t('session.cwd')} />
    </Composer>
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
  const meta = [x.agent_id && `@${x.agent_id}`, t('session.metaHarness', { harness: x.harness }), x.created_by !== x.user_id && t('session.metaOnBehalf', { by: x.created_by, owner: x.user_id }), x.daemon_id && t('session.metaDaemon', { id: x.daemon_id }), x.cwd, x.harness_session_id && t('session.metaHarnessSession', { id: x.harness_session_id })].filter(Boolean).join(' · ');
  const last = events.filter(e => e.kind === 'progress').pop();
  return <div className="flex min-h-0 flex-1 flex-col">
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto grid w-full max-w-3xl gap-6 px-4 py-6">
        <div className="flex items-start gap-2 border-b pb-3">
          <p className="min-w-0 flex-1 break-all font-mono text-xs text-muted-foreground">{meta}</p>
          {x.status !== 'running' && <Confirm title={t('session.deleteTitle')} description={t('session.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
            <Button variant="ghost" size="icon" className="size-7 text-destructive" aria-label={t('session.delete')}><Trash2 /></Button>
          </Confirm>}
        </div>
        {messages.map(m => <MessageView key={m.id} m={m} events={events} />)}
        {x.status !== 'idle' && <div className="flex items-start gap-2 text-sm">
          <StatusBadge tone={STATUS_TONE[x.status]}>{statusLabel(x.status)}</StatusBadge>
          <span className="min-w-0 break-words text-muted-foreground">{last ? evText(last.payload).slice(0, 300) : ''}</span>
        </div>}
        <div ref={bottom} />
      </div>
    </div>
    <div className="mx-auto w-full max-w-3xl px-4 pb-4">
      {x.daemon_id || x.status !== 'idle'
        ? <Composer placeholder={x.status === 'idle' ? t('session.replyPlaceholder') : t('session.busyPlaceholder')} disabled={x.status !== 'idle'} busy={reply.isPending} onSend={p => reply.mutateAsync(p)} />
        : <p className="py-2 text-center text-sm text-muted-foreground">{t('session.daemonGone')}</p>}
    </div>
  </div>;
}

function MessageView({ m, events }: { m: Message; events: SessionEvent[] }) {
  const t = useT();
  const c = m.content || {};
  if (m.role === 'user') {
    const text = typeof c.prompt === 'string' ? c.prompt : JSON.stringify(c, null, 2);
    const progress = events.filter(e => e.message_id === m.id && e.kind === 'progress');
    return <div className="grid gap-3">
      <div className="ml-auto max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-muted px-4 py-2.5 text-sm" title={fmtTime(m.created_at)}>{text}</div>
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
