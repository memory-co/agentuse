import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Send, Trash2, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Textarea } from '@/components/ui/textarea';
import { Confirm, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useHarnesses } from '@/pages/Harnesses';
import type { Message, Session, SessionDetail as Detail, SessionEvent, SessionStatus, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';

const STATUS: Record<SessionStatus, [string, 'ok' | 'warn' | 'run']> = { idle: ['已回复', 'ok'], pending: ['排队中', 'warn'], running: ['运行中', 'run'] };
const refresh = () => queryClient.invalidateQueries({ queryKey: ['sessions'] });
// progress 回报只有 {text} 时直接显示文字，否则显示 JSON。
const evText = (p: any) => (p && typeof p === 'object' && Object.keys(p).length === 1 && 'text' in p ? String(p.text) : JSON.stringify(p ?? ''));

export function Sessions(_: { me: User }) {
  const sessions = useQuery({ queryKey: ['sessions'], queryFn: () => api<Session[]>('/api/sessions'), refetchInterval: 3000 });
  const [open, setOpen] = useState<string | null>(null);
  return <div className="grid gap-4">
    <NewSession onCreated={setOpen} />
    {open && <SessionDetail id={open} onClose={() => setOpen(null)} />}
    <Card>
      <CardHeader><CardTitle className="text-base">会话</CardTitle></CardHeader>
      <CardContent>
        {sessions.isPending ? <Loading /> : sessions.isError ? <ErrorState error={sessions.error} /> : <Table>
          <TableHeader><TableRow><TableHead>标题</TableHead><TableHead>harness</TableHead><TableHead>状态</TableHead><TableHead>更新</TableHead></TableRow></TableHeader>
          <TableBody>
            {sessions.data.length ? sessions.data.map(x => <TableRow key={x.id} data-state={x.id === open ? 'selected' : undefined} className="cursor-pointer" onClick={() => setOpen(x.id)}>
              <TableCell className="max-w-md truncate font-medium">{x.title || x.id}</TableCell>
              <TableCell>{x.harness}</TableCell>
              <TableCell><StatusBadge tone={STATUS[x.status]?.[1] || 'muted'}>{STATUS[x.status]?.[0] || x.status}</StatusBadge></TableCell>
              <TableCell className="text-xs text-muted-foreground">{fmtTime(x.updated_at)}</TableCell>
            </TableRow>) : <TableRow><TableCell colSpan={4} className="text-center text-muted-foreground">还没有会话</TableCell></TableRow>}
          </TableBody>
        </Table>}
      </CardContent>
    </Card>
  </div>;
}

function NewSession({ onCreated }: { onCreated: (id: string) => void }) {
  const harnesses = useHarnesses();
  const [harness, setHarness] = useState('');
  const [cwd, setCwd] = useState('');
  const [prompt, setPrompt] = useState('');
  useEffect(() => { if (!harness && harnesses.data?.length) setHarness(harnesses.data[0].name); }, [harness, harnesses.data]);
  const add = useMutation({
    mutationFn: () => api<{ session: Session }>('/api/sessions', { method: 'POST', body: { harness, prompt, cwd: cwd.trim() } }),
    onSuccess: r => { setPrompt(''); onCreated(r.session.id); void refresh(); },
  });
  return <Card>
    <CardHeader><CardTitle className="text-base">新建会话</CardTitle></CardHeader>
    <CardContent>
      <form className="grid gap-3" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
        <div className="flex flex-wrap gap-2">
          <Select value={harness} onValueChange={setHarness}>
            <SelectTrigger className="w-56"><SelectValue placeholder="选择 harness" /></SelectTrigger>
            <SelectContent>{harnesses.data?.map(a => <SelectItem key={a.name} value={a.name}>{a.display_name} ({a.name})</SelectItem>)}</SelectContent>
          </Select>
          <Input className="min-w-64 flex-1" placeholder="工作目录(可选，daemon 本机路径，整个会话都在这里跑)" value={cwd} onChange={e => setCwd(e.target.value)} />
        </div>
        <Textarea placeholder="要 harness 做什么" value={prompt} onChange={e => setPrompt(e.target.value)} />
        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" disabled={!harness || !prompt.trim() || add.isPending}><Send />发送</Button>
          <span className="text-xs text-muted-foreground">会话只派给你自己的、开启了这个 harness 的在线 daemon;之后一直在同一台上续接。</span>
        </div>
      </form>
    </CardContent>
  </Card>;
}

function SessionDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const q = useQuery({ queryKey: ['sessions', id], queryFn: () => api<Detail>(`/api/sessions/${id}`), refetchInterval: 3000 });
  const [prompt, setPrompt] = useState('');
  const reply = useMutation({
    mutationFn: () => api(`/api/sessions/${id}/messages`, { method: 'POST', body: { prompt } }),
    onSuccess: () => { setPrompt(''); void refresh(); },
  });
  const del = useMutation({ mutationFn: () => api(`/api/sessions/${id}`, { method: 'DELETE' }), onSuccess: () => { onClose(); void refresh(); } });
  if (q.isPending) return <Card><CardContent className="pt-6"><Loading /></CardContent></Card>;
  if (q.isError) return <Card><CardContent className="pt-6"><ErrorState error={q.error} /></CardContent></Card>;
  const { session: x, messages, events } = q.data;
  const meta = [x.id, `harness ${x.harness}`, x.daemon_id && `daemon ${x.daemon_id}`, x.cwd, x.harness_session_id && `harness 会话 ${x.harness_session_id}`].filter(Boolean).join(' · ');
  const last = events.filter(e => e.kind === 'progress').pop();
  return <Card>
    <CardHeader className="flex-row items-start justify-between gap-2 space-y-0">
      <div className="grid min-w-0 gap-1"><CardTitle className="truncate text-base">{x.title || x.id}</CardTitle><CardDescription className="break-all font-mono text-xs">{meta}</CardDescription></div>
      <div className="flex gap-2">
        {x.status !== 'running' && <Confirm title="删除这个会话?" description="只删 agentuse 里的记录。" action="删除" onConfirm={() => del.mutate()}>
          <Button variant="outline" size="sm" className="text-destructive"><Trash2 />删除</Button>
        </Confirm>}
        <Button variant="ghost" size="icon" className="size-8" onClick={onClose} aria-label="关闭"><X /></Button>
      </div>
    </CardHeader>
    <CardContent className="grid gap-4">
      {messages.map(m => <MessageView key={m.id} m={m} events={events} />)}
      {x.status !== 'idle' && <div className="flex items-start gap-2 text-sm">
        <StatusBadge tone={STATUS[x.status][1]}>{STATUS[x.status][0]}</StatusBadge>
        <span className="text-muted-foreground">{last ? evText(last.payload).slice(0, 300) : ''}</span>
      </div>}
      {x.status === 'idle' && (x.daemon_id ? <form className="grid gap-2" onSubmit={e => { e.preventDefault(); reply.mutate(); }}>
        <Textarea placeholder="接着说…" value={prompt} onChange={e => setPrompt(e.target.value)} />
        <div><Button type="submit" disabled={!prompt.trim() || reply.isPending}><Send />回复</Button></div>
      </form> : <p className="text-sm text-muted-foreground">原来的 daemon 已删除，这个会话不能继续。</p>)}
    </CardContent>
  </Card>;
}

function MessageView({ m, events }: { m: Message; events: SessionEvent[] }) {
  const c = m.content || {};
  const pre = 'whitespace-pre-wrap break-words rounded-md border bg-muted p-3 font-mono text-xs';
  if (m.role === 'user') {
    const text = typeof c.prompt === 'string' ? c.prompt : JSON.stringify(c, null, 2);
    const progress = events.filter(e => e.message_id === m.id && e.kind === 'progress');
    return <div className="grid gap-1.5">
      <div className="text-sm"><b>你</b> <span className="text-xs text-muted-foreground">{fmtTime(m.created_at)}</span></div>
      <pre className={pre}>{text}</pre>
      {progress.length > 0 && <details className="text-xs text-muted-foreground">
        <summary className="cursor-pointer">过程({progress.length})</summary>
        <div className="mt-1 grid max-h-64 gap-1 overflow-auto">{progress.map(e => <div key={e.id}>{evText(e.payload).slice(0, 600)}</div>)}</div>
      </details>}
    </div>;
  }
  const out = c.ok ? (typeof c.output === 'string' ? c.output : JSON.stringify(c, null, 2)) : (c.error || '失败') + (c.output ? '\n' + c.output : '');
  return <div className="grid gap-1.5">
    <div className="flex items-center gap-2 text-sm"><b>harness</b>{!c.ok && <StatusBadge tone="bad">失败</StatusBadge>}<span className="text-xs text-muted-foreground">{fmtTime(m.created_at)}</span></div>
    <pre className={pre}>{out}</pre>
  </div>;
}
