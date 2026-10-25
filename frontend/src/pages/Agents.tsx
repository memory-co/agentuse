import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Agent, Daemon, User } from '@/lib/types';
import { useT } from '@/lib/i18n';

export const useAgents = () => useQuery({ queryKey: ['agents'], queryFn: () => api<Agent[]>('/api/agents'), staleTime: 30_000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['agents'] });

// 运行时 = 某台 daemon + 上面上报过的某个 harness,下拉框的值写成 "daemon_id/harness"。
type Runtime = { daemon_id: string; harness: string };
const rtKey = (r: Runtime) => `${r.daemon_id}/${r.harness}`;
const parseRt = (k: string): Runtime => { const i = k.indexOf('/'); return { daemon_id: k.slice(0, i), harness: k.slice(i + 1) }; };
const runtimesOf = (daemons: Daemon[] | undefined, userId: string) =>
  (daemons || []).filter(d => d.user_id === userId).flatMap(d => d.harnesses.map(h => ({ key: rtKey({ daemon_id: d.id, harness: h.harness }), label: `${d.name} · ${h.harness}` })));

function RuntimeSelect({ value, onChange, daemons, userId, className }: { value: string; onChange: (v: string) => void; daemons?: Daemon[]; userId: string; className?: string }) {
  const t = useT();
  const options = runtimesOf(daemons, userId);
  return <Select value={value} onValueChange={onChange}>
    <SelectTrigger className={className}><SelectValue placeholder={t('agents.pickRuntime')} /></SelectTrigger>
    <SelectContent>{options.map(o => <SelectItem key={o.key} value={o.key}>{o.label}</SelectItem>)}</SelectContent>
  </Select>;
}

export function Agents({ me }: { me: User }) {
  const t = useT();
  const agents = useAgents();
  const daemons = useQuery({ queryKey: ['daemons'], queryFn: () => api<Daemon[]>('/api/daemons'), staleTime: 10_000 });
  const [form, setForm] = useState({ id: '', name: '', runtime: '', description: '' });
  useEffect(() => {
    const first = runtimesOf(daemons.data, me.id)[0];
    if (!form.runtime && first) setForm(f => ({ ...f, runtime: first.key }));
  }, [form.runtime, daemons.data, me.id]);
  const add = useMutation({
    mutationFn: () => api('/api/agents', { method: 'POST', body: { id: form.id.trim(), name: form.name.trim(), ...parseRt(form.runtime), description: form.description.trim() } }),
    onSuccess: () => { setForm(f => ({ ...f, id: '', name: '', description: '' })); void refresh(); },
  });
  const swap = useMutation({
    mutationFn: ({ id, runtime }: { id: string; runtime: string }) => api(`/api/agents/${id}`, { method: 'PATCH', body: parseRt(runtime) }),
    onSuccess: refresh,
  });
  const del = useMutation({ mutationFn: (id: string) => api(`/api/agents/${id}`, { method: 'DELETE' }), onSuccess: refresh });
  return <div className="grid grid-cols-1 gap-4">
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t('agents.newTitle')}</CardTitle>
        <CardDescription>{t('agents.newText')}</CardDescription>
      </CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="w-40 font-mono" placeholder={t('agents.idPlaceholder')} value={form.id} onChange={e => setForm({ ...form, id: e.target.value })} />
          <Input className="w-44" placeholder={t('agents.namePlaceholder')} value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} required />
          <RuntimeSelect className="w-56" value={form.runtime} onChange={runtime => setForm({ ...form, runtime })} daemons={daemons.data} userId={me.id} />
          <Input className="min-w-48 flex-1" placeholder={t('common.description')} value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} />
          <Button type="submit" disabled={!form.runtime || add.isPending}>{t('common.new')}</Button>
        </form>
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle className="text-base">{t('agents.listTitle')}</CardTitle></CardHeader>
      <CardContent>
        {agents.isPending ? <Loading /> : agents.isError ? <ErrorState error={agents.error} /> : <Table>
          <TableHeader><TableRow><TableHead>id</TableHead><TableHead>{t('common.name')}</TableHead><TableHead>{t('agents.runtime')}</TableHead><TableHead>{t('common.description')}</TableHead>{me.role === 'admin' && <TableHead>{t('common.user')}</TableHead>}<TableHead /></TableRow></TableHeader>
          <TableBody>
            {agents.data.length ? agents.data.map(a => <TableRow key={a.id}>
              <TableCell className="font-mono text-xs">{a.id}</TableCell>
              <TableCell className="font-medium">{a.name}</TableCell>
              <TableCell><RuntimeSelect className="h-8 w-52" value={a.daemon_id ? rtKey({ daemon_id: a.daemon_id, harness: a.harness }) : ''} onChange={runtime => swap.mutate({ id: a.id, runtime })} daemons={daemons.data} userId={a.user_id} /></TableCell>
              <TableCell className="text-muted-foreground">{a.description}</TableCell>
              {me.role === 'admin' && <TableCell className="font-mono text-xs">{a.user_id}</TableCell>}
              <TableCell className="text-right">
                <Confirm title={t('agents.deleteTitle', { name: a.name })} description={t('agents.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate(a.id)}>
                  <Button variant="ghost" size="icon" className="size-8 text-destructive" aria-label={t('common.delete')}><Trash2 /></Button>
                </Confirm>
              </TableCell>
            </TableRow>) : <TableRow><TableCell colSpan={6} className="text-center text-muted-foreground">{t('agents.empty')}</TableCell></TableRow>}
          </TableBody>
        </Table>}
      </CardContent>
    </Card>
  </div>;
}
