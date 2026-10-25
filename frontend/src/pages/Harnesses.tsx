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
import type { Harness, User } from '@/lib/types';
import { useT } from '@/lib/i18n';

export const useHarnesses = () => useQuery({ queryKey: ['harnesses'], queryFn: () => api<Harness[]>('/api/harnesses'), staleTime: 30_000 });
const refresh = () => queryClient.invalidateQueries({ queryKey: ['harnesses'] });

export function Harnesses({ me }: { me: User }) {
  const t = useT();
  const harnesses = useHarnesses();
  const [form, setForm] = useState({ name: '', display_name: '', description: '' });
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value });
  const add = useMutation({
    mutationFn: () => api('/api/harnesses', { method: 'POST', body: { name: form.name.trim(), display_name: form.display_name.trim(), description: form.description.trim() } }),
    onSuccess: () => { setForm({ name: '', display_name: '', description: '' }); void refresh(); },
  });
  const del = useMutation({ mutationFn: (n: string) => api(`/api/harnesses/${encodeURIComponent(n)}`, { method: 'DELETE' }), onSuccess: refresh });
  return <div className="grid grid-cols-1 gap-4">
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t('harnesses.newTitle')}</CardTitle>
        <CardDescription>
          {t('harnesses.newText1')}<code>{'{"session_id","resume","input"}'}</code>{t('harnesses.newText2')}<code>{'{"type":"progress",...}'}</code> / <code>{'{"type":"result","ok":true,"output":...,"session_id":...}'}</code>{t('harnesses.newText3')}<code>~/.agentuse/daemon.json</code>{t('harnesses.newText4')}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="w-44" placeholder={t('harnesses.namePlaceholder')} value={form.name} onChange={set('name')} required />
          <Input className="w-36" placeholder={t('harnesses.displayName')} value={form.display_name} onChange={set('display_name')} />
          <Input className="min-w-48 flex-1" placeholder={t('common.description')} value={form.description} onChange={set('description')} />
          <Button type="submit" disabled={add.isPending}>{t('harnesses.register')}</Button>
        </form>
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle className="text-base">{t('harnesses.listTitle')}</CardTitle></CardHeader>
      <CardContent>
        {harnesses.isPending ? <Loading /> : harnesses.isError ? <ErrorState error={harnesses.error} /> : <Table>
          <TableHeader><TableRow><TableHead>{t('common.name')}</TableHead><TableHead>{t('harnesses.kind')}</TableHead><TableHead>{t('harnesses.displayName')}</TableHead><TableHead>{t('common.description')}</TableHead><TableHead /></TableRow></TableHeader>
          <TableBody>{harnesses.data.map(a => <TableRow key={a.name}>
            <TableCell className="font-mono text-xs">{a.name}</TableCell>
            <TableCell>{a.kind === 'builtin' ? t('harnesses.builtin') : t('harnesses.custom')}</TableCell>
            <TableCell>{a.display_name}</TableCell>
            <TableCell className="text-muted-foreground">{a.description}</TableCell>
            <TableCell className="text-right">{a.kind === 'custom' && (a.owner_id === me.id || me.role === 'admin') &&
              <Confirm title={t('harnesses.deleteTitle', { name: a.name })} action={t('common.delete')} onConfirm={() => del.mutate(a.name)}>
                <Button variant="ghost" size="icon" className="size-8 text-destructive" aria-label={t('common.delete')}><Trash2 /></Button>
              </Confirm>}</TableCell>
          </TableRow>)}</TableBody>
        </Table>}
      </CardContent>
    </Card>
  </div>;
}
