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
import { useHarnesses } from '@/pages/Harnesses';
import type { Daemon, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';
import { useT } from '@/lib/i18n';

const TONE = { pending: 'warn', online: 'ok', offline: 'muted' } as const;
const refresh = () => queryClient.invalidateQueries({ queryKey: ['daemons'] });

export function Daemons({ me }: { me: User }) {
  const t = useT();
  const daemons = useQuery({ queryKey: ['daemons'], queryFn: () => api<Daemon[]>('/api/daemons'), refetchInterval: 3000 });
  const [name, setName] = useState('');
  const add = useMutation({
    mutationFn: () => api('/api/daemons', { method: 'POST', body: { name: name.trim() } }),
    onSuccess: () => { setName(''); void refresh(); },
  });
  return <div className="grid grid-cols-1 gap-4">
    <Card>
      <CardHeader><CardTitle className="text-base">{t('daemons.addTitle')}</CardTitle><CardDescription>{t('daemons.addText')}</CardDescription></CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="max-w-xs" placeholder={t('daemons.namePlaceholder')} value={name} onChange={e => setName(e.target.value)} />
          <Button type="submit" disabled={add.isPending}><Plus />{t('daemons.add')}</Button>
        </form>
      </CardContent>
    </Card>
    {daemons.isPending ? <Loading /> : daemons.isError ? <ErrorState error={daemons.error} />
      : daemons.data.length ? daemons.data.map(d => <DaemonCard key={d.id} d={d} me={me} />)
      : <Empty>{t('daemons.empty')}</Empty>}
  </div>;
}

function DaemonCard({ d, me }: { d: Daemon; me: User }) {
  const t = useT();
  const harnesses = useHarnesses();
  const displayName = (n: string) => harnesses.data?.find(a => a.name === n)?.display_name || n;
  const del = useMutation({ mutationFn: () => api(`/api/daemons/${d.id}`, { method: 'DELETE' }), onSuccess: refresh });
  const reset = useMutation({ mutationFn: () => api(`/api/daemons/${d.id}/reset`, { method: 'POST' }), onSuccess: refresh });
  const toggle = useMutation({
    mutationFn: ({ harness, enabled }: { harness: string; enabled: boolean }) =>
      api(`/api/daemons/${d.id}/harnesses/${encodeURIComponent(harness)}`, { method: 'PUT', body: { enabled } }),
    onSuccess: (_, v) => { toast.success(v.enabled ? t('daemons.enabled') : t('daemons.disabled')); void refresh(); },
  });
  const tone = TONE[d.status] || 'muted';
  const label = d.status in TONE ? t(`daemons.status.${d.status}`) : d.status;
  const meta = [d.id, me.role === 'admin' && d.user_id !== me.id && t('daemons.metaUser', { id: d.user_id }), d.last_seen_at && t('daemons.metaSeen', { at: fmtTime(d.last_seen_at) })].filter(Boolean).join(' · ');
  return <Card>
    <CardHeader className="flex-row flex-wrap items-start justify-between gap-2 space-y-0">
      <div className="grid gap-1">
        <CardTitle className="flex items-center gap-2 text-base">{d.name || t('daemons.unnamed')}<StatusBadge tone={tone}>{label}</StatusBadge></CardTitle>
        <CardDescription className="font-mono text-xs">{meta}</CardDescription>
      </div>
      <div className="flex gap-2">
        {d.status !== 'pending' && <Confirm title={t('daemons.resetTitle')} description={t('daemons.resetText')} onConfirm={() => reset.mutate()}>
          <Button variant="outline" size="sm"><RefreshCw />{t('daemons.reset')}</Button>
        </Confirm>}
        <Confirm title={t('daemons.deleteTitle')} description={t('daemons.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
          <Button variant="outline" size="sm" className="text-destructive"><Trash2 />{t('common.delete')}</Button>
        </Confirm>
      </div>
    </CardHeader>
    <CardContent>
      {d.status === 'pending' ? <div className="grid gap-2">
        <p className="text-sm text-muted-foreground">{t('daemons.runSetup')}</p>
        <CopyBlock text={d.setup_command || ''} />
      </div> : d.harnesses.length ? <div className="grid gap-3">
        <p className="text-sm text-muted-foreground">{t('daemons.pickHarnesses')}</p>
        {d.harnesses.map(a => <label key={a.harness} className="flex items-center gap-3 text-sm">
          <Switch checked={a.enabled} disabled={!a.available || toggle.isPending} onCheckedChange={enabled => toggle.mutate({ harness: a.harness, enabled })} />
          <span className="font-medium">{displayName(a.harness)}</span>
          <span className="text-xs text-muted-foreground">{a.harness} {a.version}{!a.available && t('daemons.notDetected')}</span>
        </label>)}
      </div> : <p className="text-sm text-muted-foreground">{t('daemons.noHarnesses')}</p>}
    </CardContent>
  </Card>;
}
