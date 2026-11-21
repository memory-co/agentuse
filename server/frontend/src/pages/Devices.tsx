import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Plus, RefreshCw, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Confirm, CopyButton, Empty, ErrorState, Loading, StatusBadge } from '@/components/Shared';
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
  const [setup, setSetup] = useState<Device | null>(null);
  const add = useMutation({
    mutationFn: async () => {
      setSetup(await api<Device>('/api/devices', { method: 'POST', body: { name: name.trim() } }));
    },
    onSuccess: () => { setName(''); void refresh(); },
  });
  return <div className="grid grid-cols-1 gap-4 sm:gap-5">
    <Card>
      <CardHeader><CardTitle>{t('devices.addTitle')}</CardTitle><CardDescription>{t('devices.addText')}</CardDescription></CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(); }}>
          <Input className="max-w-xs" placeholder={t('devices.namePlaceholder')} value={name} onChange={e => setName(e.target.value)} />
          <Button type="submit" disabled={add.isPending}><Plus />{t('devices.add')}</Button>
        </form>
        {add.isError && <ErrorState error={add.error} />}
      </CardContent>
    </Card>
    {setup?.setup_command && <Card>
      <CardHeader>
        <CardTitle>{t('devices.setupTitle')}</CardTitle>
        <CardDescription>{t('devices.runSetup')}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap gap-2">
        <CopyButton text={setup.setup_command}>{t('devices.copySetup')}</CopyButton>
        <Button variant="ghost" onClick={() => setSetup(null)}>{t('common.close')}</Button>
      </CardContent>
    </Card>}
    {devices.isPending ? <Loading /> : devices.isError ? <ErrorState error={devices.error} />
      : devices.data.length ? devices.data.map(d => <DeviceCard key={d.id} d={d} me={me} onSetup={setSetup} onDelete={() => setSetup(current => current?.id === d.id ? null : current)} />)
      : <Empty>{t('devices.empty')}</Empty>}
  </div>;
}

function DeviceCard({ d, me, onSetup, onDelete }: { d: Device; me: User; onSetup: (device: Device) => void; onDelete: () => void }) {
  const t = useT();
  const harnesses = useHarnesses();
  const displayName = (n: string) => harnesses.data?.find(a => a.name === n)?.display_name || n;
  const del = useMutation({ mutationFn: () => api(`/api/devices/${d.id}`, { method: 'DELETE' }), onSuccess: () => { onDelete(); void refresh(); } });
  const reset = useMutation({
    mutationFn: async () => { onSetup(await api<Device>(`/api/devices/${d.id}/reset`, { method: 'POST' })); },
    onSuccess: refresh,
  });
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
      <div className="grid min-w-0 gap-1.5">
        <CardTitle className="flex flex-wrap items-center gap-x-2 gap-y-1 break-words [overflow-wrap:anywhere]">{d.name || t('devices.unnamed')}<StatusBadge tone={tone}>{label}</StatusBadge></CardTitle>
        <CardDescription className="break-words text-xs tabular-nums [overflow-wrap:anywhere]">{meta}</CardDescription>
      </div>
      <div className="flex gap-2">
        <Confirm title={t('devices.resetTitle')} description={t('devices.resetText')} onConfirm={() => reset.mutate()}>
          <Button variant="ghost" size="sm"><RefreshCw />{t('devices.reset')}</Button>
        </Confirm>
        <Confirm title={t('devices.deleteTitle')} description={t('devices.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
          <Button variant="ghost" size="sm" className="text-destructive"><Trash2 />{t('common.delete')}</Button>
        </Confirm>
      </div>
    </CardHeader>
    <CardContent>
      {reset.isError && <ErrorState error={reset.error} />}
      {d.status === 'pending' ? <p className="text-sm text-muted-foreground">{t('devices.awaitImport')}</p> : d.harnesses.length ? <div>
        <p className="mb-2 text-sm text-muted-foreground">{t('devices.pickHarnesses')}</p>
        <div className="divide-y">
          {d.harnesses.map(a => <label key={a.harness} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-sm">
            <Switch checked={a.enabled} disabled={!a.available || toggle.isPending} onCheckedChange={enabled => toggle.mutate({ harness: a.harness, enabled })} />
            <span className="font-medium">{displayName(a.harness)}</span>
            <span className="min-w-0 break-words text-xs text-muted-foreground">{a.harness} {a.version}{!a.available && t('devices.notDetected')}</span>
          </label>)}
        </div>
      </div> : <p className="text-sm text-muted-foreground">{t('devices.noHarnesses')}</p>}
    </CardContent>
  </Card>;
}
