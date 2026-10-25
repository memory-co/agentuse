import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { CopyBlock, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Role, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';
import { useT } from '@/lib/i18n';

export function Users(_: { me: User }) {
  const t = useT();
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/api/users') });
  const [form, setForm] = useState({ id: '', name: '', role: 'user' as Role });
  const add = useMutation({
    mutationFn: () => api<{ user: User; login_url: string }>('/api/users', { method: 'POST', body: { id: form.id.trim(), name: form.name.trim(), role: form.role } }),
    onSuccess: () => { setForm({ ...form, id: '', name: '' }); void queryClient.invalidateQueries({ queryKey: ['users'] }); },
  });
  return <div className="grid gap-4">
    <Card>
      <CardHeader><CardTitle className="text-base">{t('users.newTitle')}</CardTitle></CardHeader>
      <CardContent className="grid gap-3">
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="min-w-64 flex-1" placeholder={t('users.idPlaceholder')} value={form.id} onChange={e => setForm({ ...form, id: e.target.value })} />
          <Input className="w-40" placeholder={t('users.namePlaceholder')} value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
          <Select value={form.role} onValueChange={v => setForm({ ...form, role: v as Role })}>
            <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
            <SelectContent><SelectItem value="user">{t('common.normalUser')}</SelectItem><SelectItem value="admin">{t('common.admin')}</SelectItem></SelectContent>
          </Select>
          <Button type="submit" disabled={add.isPending}>{t('common.create')}</Button>
        </form>
        {add.data && <div className="grid gap-2">
          <p className="text-sm">{t('users.created1')}<b>{add.data.user.name}</b>{t('users.created2')}</p>
          <CopyBlock text={add.data.login_url} />
        </div>}
      </CardContent>
    </Card>
    <Card>
      <CardHeader><CardTitle className="text-base">{t('users.listTitle')}</CardTitle></CardHeader>
      <CardContent>
        {users.isPending ? <Loading /> : users.isError ? <ErrorState error={users.error} /> : <Table>
          <TableHeader><TableRow><TableHead>{t('users.name')}</TableHead><TableHead>{t('users.role')}</TableHead><TableHead>id</TableHead><TableHead>{t('users.createdAt')}</TableHead></TableRow></TableHeader>
          <TableBody>{users.data.map(u => <TableRow key={u.id}>
            <TableCell>{u.name}</TableCell>
            <TableCell>{u.role === 'admin' ? t('common.admin') : t('common.normalUser')}</TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">{u.id}</TableCell>
            <TableCell className="text-xs text-muted-foreground">{fmtTime(u.created_at)}</TableCell>
          </TableRow>)}</TableBody>
        </Table>}
      </CardContent>
    </Card>
  </div>;
}
