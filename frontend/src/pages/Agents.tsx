import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Agent, User } from '@/lib/types';

export const useAgents = () => useQuery({ queryKey: ['agents'], queryFn: () => api<Agent[]>('/api/agents'), staleTime: 30_000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['agents'] });

export function Agents({ me }: { me: User }) {
  const agents = useAgents();
  const [form, setForm] = useState({ name: '', display_name: '', description: '' });
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });
  const add = useMutation({
    mutationFn: () => api('/api/agents', { method: 'POST', body: { name: form.name.trim(), display_name: form.display_name.trim(), description: form.description.trim() } }),
    onSuccess: () => { setForm({ name: '', display_name: '', description: '' }); void refresh(); },
  });
  const del = useMutation({ mutationFn: (n: string) => api(`/api/agents/${encodeURIComponent(n)}`, { method: 'DELETE' }), onSuccess: refresh });
  return <div className="grid gap-4">
    <Card>
      <CardHeader>
        <CardTitle className="text-base">注册自定义 agent</CardTitle>
        <CardDescription>
          自己写的 harness:daemon 把 <code>{'{"session_id","resume","input"}'}</code> 以 JSON 写进 stdin,程序往 stdout 按行输出 JSON(<code>{'{"type":"progress",...}'}</code> / <code>{'{"type":"result","ok":true,"output":...,"session_id":...}'}</code>,session_id 下次通过 resume 带回来)。注册后在 daemon 的 <code>~/.agentuse/daemon.json</code> 里配置本机启动命令。
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="w-44" placeholder="名字，如 my-harness" value={form.name} onChange={set('name')} required />
          <Input className="w-36" placeholder="显示名" value={form.display_name} onChange={set('display_name')} />
          <Input className="min-w-48 flex-1" placeholder="说明" value={form.description} onChange={set('description')} />
          <Button type="submit" disabled={add.isPending}>注册</Button>
        </form>
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle className="text-base">全部 agent</CardTitle></CardHeader>
      <CardContent>
        {agents.isPending ? <Loading /> : agents.isError ? <ErrorState error={agents.error} /> : <Table>
          <TableHeader><TableRow><TableHead>名字</TableHead><TableHead>类型</TableHead><TableHead>显示名</TableHead><TableHead>说明</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>{agents.data.map(a => <TableRow key={a.name}>
            <TableCell className="font-mono text-xs">{a.name}</TableCell>
            <TableCell>{a.kind === 'builtin' ? '内置' : '自定义'}</TableCell>
            <TableCell>{a.display_name}</TableCell>
            <TableCell className="text-muted-foreground">{a.description}</TableCell>
            <TableCell className="text-right">{a.kind === 'custom' && (a.owner_id === me.id || me.role === 'admin') &&
              <Confirm title={`删除 agent ${a.name}?`} action="删除" onConfirm={() => del.mutate(a.name)}>
                <Button variant="ghost" size="icon" className="size-8 text-destructive" aria-label="删除"><Trash2 /></Button>
              </Confirm>}</TableCell>
          </TableRow>)}</TableBody>
        </Table>}
      </CardContent>
    </Card>
  </div>;
}
