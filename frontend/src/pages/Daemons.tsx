import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Plus, RefreshCw, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Confirm, CopyBlock, Empty, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import type { Daemon, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';

const STATUS = { pending: ['待注册', 'warn'], online: ['在线', 'ok'], offline: ['离线', 'muted'] } as const;
const refresh = () => queryClient.invalidateQueries({ queryKey: ['daemons'] });

export function Daemons({ me }: { me: User }) {
  const daemons = useQuery({ queryKey: ['daemons'], queryFn: () => api<Daemon[]>('/api/daemons'), refetchInterval: 3000 });
  const [name, setName] = useState('');
  const add = useMutation({
    mutationFn: () => api('/api/daemons', { method: 'POST', body: { name: name.trim() } }),
    onSuccess: () => { setName(''); void refresh(); },
  });
  return <div className="grid gap-4">
    <Card>
      <CardHeader><CardTitle className="text-base">添加机器</CardTitle><CardDescription>在要跑 agent 的电脑上执行生成的安装命令，就能把它注册成一个 daemon。</CardDescription></CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="max-w-xs" placeholder="名字(可选，默认用主机名)" value={name} onChange={e => setName(e.target.value)} />
          <Button type="submit" disabled={add.isPending}><Plus />添加 daemon</Button>
        </form>
      </CardContent>
    </Card>
    {daemons.isPending ? <Loading /> : daemons.isError ? <ErrorState error={daemons.error} />
      : daemons.data.length ? daemons.data.map(d => <DaemonCard key={d.id} d={d} me={me} />)
      : <Empty>还没有 daemon,先添加一台。</Empty>}
  </div>;
}

function DaemonCard({ d, me }: { d: Daemon; me: User }) {
  const agents = useAgents();
  const displayName = (n: string) => agents.data?.find(a => a.name === n)?.display_name || n;
  const del = useMutation({ mutationFn: () => api(`/api/daemons/${d.id}`, { method: 'DELETE' }), onSuccess: refresh });
  const reset = useMutation({ mutationFn: () => api(`/api/daemons/${d.id}/reset`, { method: 'POST' }), onSuccess: refresh });
  const toggle = useMutation({
    mutationFn: ({ agent, enabled }: { agent: string; enabled: boolean }) =>
      api(`/api/daemons/${d.id}/agents/${encodeURIComponent(agent)}`, { method: 'PUT', body: { enabled } }),
    onSuccess: (_, v) => { toast.success(v.enabled ? '已开启' : '已关闭'); void refresh(); },
  });
  const [label, tone] = STATUS[d.status] || [d.status, 'muted'];
  const meta = [d.id, me.role === 'admin' && d.user_id !== me.id && `用户 ${d.user_id}`, d.last_seen_at && `最近 ${fmtTime(d.last_seen_at)}`].filter(Boolean).join(' · ');
  return <Card>
    <CardHeader className="flex-row flex-wrap items-start justify-between gap-2 space-y-0">
      <div className="grid gap-1">
        <CardTitle className="flex items-center gap-2 text-base">{d.name || '(未命名)'}<StatusBadge tone={tone}>{label}</StatusBadge></CardTitle>
        <CardDescription className="font-mono text-xs">{meta}</CardDescription>
      </div>
      <div className="flex gap-2">
        {d.status !== 'pending' && <Confirm title="重新生成安装命令?" description="这台电脑现有的密钥会立即失效，需要重新跑安装命令。" onConfirm={() => reset.mutate()}>
          <Button variant="outline" size="sm"><RefreshCw />重新生成安装命令</Button>
        </Confirm>}
        <Confirm title="删除这台 daemon?" description="它会断开，绑定在它上面的会话不能再继续。" action="删除" onConfirm={() => del.mutate()}>
          <Button variant="outline" size="sm" className="text-destructive"><Trash2 />删除</Button>
        </Confirm>
      </div>
    </CardHeader>
    <CardContent>
      {d.status === 'pending' ? <div className="grid gap-2">
        <p className="text-sm text-muted-foreground">在要接入的电脑上运行(令牌只能用一次):</p>
        <CopyBlock text={d.setup_command || ''} />
      </div> : d.agents.length ? <div className="grid gap-3">
        <p className="text-sm text-muted-foreground">选择这台电脑上开启哪些 agent:</p>
        {d.agents.map(a => <label key={a.agent} className="flex items-center gap-3 text-sm">
          <Switch checked={a.enabled} disabled={!a.available || toggle.isPending} onCheckedChange={enabled => toggle.mutate({ agent: a.agent, enabled })} />
          <span className="font-medium">{displayName(a.agent)}</span>
          <span className="text-xs text-muted-foreground">{a.agent} {a.version}{!a.available && ' · 本机未检测到'}</span>
        </label>)}
      </div> : <p className="text-sm text-muted-foreground">这台电脑还没上报可用的 agent(连上后会自动检测 claude、codex 等)。</p>}
    </CardContent>
  </Card>;
}
