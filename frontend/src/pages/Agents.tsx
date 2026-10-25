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
import { useHarnesses } from '@/pages/Harnesses';
import type { Agent, Harness, User } from '@/lib/types';
import { useT } from '@/lib/i18n';

export const useAgents = () => useQuery({ queryKey: ['agents'], queryFn: () => api<Agent[]>('/api/agents'), staleTime: 30_000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['agents'] });

function HarnessSelect({ value, onChange, harnesses, className }: { value: string; onChange: (v: string) => void; harnesses?: Harness[]; className?: string }) {
  const t = useT();
  return <Select value={value} onValueChange={onChange}>
    <SelectTrigger className={className}><SelectValue placeholder={t('agents.pickHarness')} /></SelectTrigger>
    <SelectContent>{harnesses?.map(a => <SelectItem key={a.name} value={a.name}>{a.display_name} ({a.name})</SelectItem>)}</SelectContent>
  </Select>;
}

export function Agents({ me }: { me: User }) {
  const t = useT();
  const agents = useAgents();
  const harnesses = useHarnesses();
  const [form, setForm] = useState({ id: '', name: '', harness: '', description: '' });
  useEffect(() => { if (!form.harness && harnesses.data?.length) setForm(f => ({ ...f, harness: harnesses.data[0].name })); }, [form.harness, harnesses.data]);
  const add = useMutation({
    mutationFn: () => api('/api/agents', { method: 'POST', body: { id: form.id.trim(), name: form.name.trim(), harness: form.harness, description: form.description.trim() } }),
    onSuccess: () => { setForm(f => ({ ...f, id: '', name: '', description: '' })); void refresh(); },
  });
  const swap = useMutation({
    mutationFn: ({ id, harness }: { id: string; harness: string }) => api(`/api/agents/${id}`, { method: 'PATCH', body: { harness } }),
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
          <HarnessSelect className="w-56" value={form.harness} onChange={harness => setForm({ ...form, harness })} harnesses={harnesses.data} />
          <Input className="min-w-48 flex-1" placeholder={t('common.description')} value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} />
          <Button type="submit" disabled={!form.harness || add.isPending}>{t('common.new')}</Button>
        </form>
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle className="text-base">{t('agents.listTitle')}</CardTitle></CardHeader>
      <CardContent>
        {agents.isPending ? <Loading /> : agents.isError ? <ErrorState error={agents.error} /> : <Table>
          <TableHeader><TableRow><TableHead>id</TableHead><TableHead>{t('common.name')}</TableHead><TableHead>{t('agents.harness')}</TableHead><TableHead>{t('common.description')}</TableHead>{me.role === 'admin' && <TableHead>{t('common.user')}</TableHead>}<TableHead /></TableRow></TableHeader>
          <TableBody>
            {agents.data.length ? agents.data.map(a => <TableRow key={a.id}>
              <TableCell className="font-mono text-xs">@{a.id}</TableCell>
              <TableCell className="font-medium">{a.name}</TableCell>
              <TableCell><HarnessSelect className="h-8 w-52" value={a.harness} onChange={harness => swap.mutate({ id: a.id, harness })} harnesses={harnesses.data} /></TableCell>
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
