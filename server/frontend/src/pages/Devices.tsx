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
import type { Device, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';
import { useT } from '@/lib/i18n';

const TONE = { pending: 'warn', online: 'ok', offline: 'muted' } as const;
const refresh = () => queryClient.invalidateQueries({ queryKey: ['devices'] });

export function Devices({ me }: { me: User }) {
  const t = useT();
  const devices = useQuery({ queryKey: ['devices'], queryFn: () => api<Device[]>('/api/devices') });
  const [name, setName] = useState('');
  const add = useMutation({
    mutationFn: () => api('/api/devices', { method: 'POST', body: { name: name.trim() } }),
    onSuccess: () => { setName(''); void refresh(); },
  });
  return <div className="grid grid-cols-1 gap-4">
    <Card>
      <CardHeader><CardTitle className="text-base">{t('devices.addTitle')}</CardTitle><CardDescription>{t('devices.addText')}</CardDescription></CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="max-w-xs" placeholder={t('devices.namePlaceholder')} value={name} onChange={e => setName(e.target.value)} />
          <Button type="submit" disabled={add.isPending}><Plus />{t('devices.add')}</Button>
        </form>
      </CardContent>
    </Card>
    {devices.isPending ? <Loading /> : devices.isError ? <ErrorState error={devices.error} />
      : devices.data.length ? devices.data.map(d => <DeviceCard key={d.id} d={d} me={me} />)
      : <Empty>{t('devices.empty')}</Empty>}
  </div>;
}

function DeviceCard({ d, me }: { d: Device; me: User }) {
  const t = useT();
  const harnesses = useHarnesses();
  const displayName = (n: string) => harnesses.data?.find(a => a.name === n)?.display_name || n;
  const del = useMutation({ mutationFn: () => api(`/api/devices/${d.id}`, { method: 'DELETE' }), onSuccess: refresh });
  const reset = useMutation({ mutationFn: () => api(`/api/devices/${d.id}/reset`, { method: 'POST' }), onSuccess: refresh });
  const toggle = useMutation({
    mutationFn: ({ harness, enabled }: { harness: string; enabled: boolean }) =>
      api(`/api/devices/${d.id}/harnesses/${encodeURIComponent(harness)}`, { method: 'PUT', body: { enabled } }),
    onSuccess: (_, v) => { toast.success(v.enabled ? t('devices.enabled') : t('devices.disabled')); void refresh(); },
  });
  const tone = TONE[d.status] || 'muted';
  const label = d.status in TONE ? t(`devices.status.${d.status}`) : d.status;
  const meta = [d.id, me.role === 'admin' && d.user_id !== me.id && t('devices.metaUser', { id: d.user_id }), d.last_seen_at && t('devices.metaSeen', { at: fmtTime(d.last_seen_at) })].filter(Boolean).join(' · ');
  return <Card>
    <CardHeader className="flex-row flex-wrap items-start justify-between gap-2 space-y-0">
      <div className="grid gap-1">
        <CardTitle className="flex items-center gap-2 text-base">{d.name || t('devices.unnamed')}<StatusBadge tone={tone}>{label}</StatusBadge></CardTitle>
        <CardDescription className="font-mono text-xs">{meta}</CardDescription>
      </div>
      <div className="flex gap-2">
        {d.status !== 'pending' && <Confirm title={t('devices.resetTitle')} description={t('devices.resetText')} onConfirm={() => reset.mutate()}>
          <Button variant="outline" size="sm"><RefreshCw />{t('devices.reset')}</Button>
        </Confirm>}
        <Confirm title={t('devices.deleteTitle')} description={t('devices.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
          <Button variant="outline" size="sm" className="text-destructive"><Trash2 />{t('common.delete')}</Button>
        </Confirm>
      </div>
    </CardHeader>
    <CardContent>
      {d.status === 'pending' ? <div className="grid gap-2">
        <p className="text-sm text-muted-foreground">{t('devices.runSetup')}</p>
        <CopyBlock text={d.setup_command || ''} />
      </div> : d.harnesses.length ? <div className="grid gap-3">
        <p className="text-sm text-muted-foreground">{t('devices.pickHarnesses')}</p>
        {d.harnesses.map(a => <label key={a.harness} className="flex items-center gap-3 text-sm">
          <Switch checked={a.enabled} disabled={!a.available || toggle.isPending} onCheckedChange={enabled => toggle.mutate({ harness: a.harness, enabled })} />
          <span className="font-medium">{displayName(a.harness)}</span>
          <span className="text-xs text-muted-foreground">{a.harness} {a.version}{!a.available && t('devices.notDetected')}</span>
        </label>)}
      </div> : <p className="text-sm text-muted-foreground">{t('devices.noHarnesses')}</p>}
    </CardContent>
  </Card>;
}
