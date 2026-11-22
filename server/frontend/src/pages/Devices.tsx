import { useEffect, useState } from 'react';
import { ArrowUpCircle, Plus, RefreshCw, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Confirm, CopyBlock, Empty, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import {
  canUpgradeDevice, isDeviceUpgrading, useClientRelease, useCreateDevice, useDeleteDevice, useDevices,
  useHarnesses, useResetDevice, useSetDeviceHarness, useUpgradeDevice,
} from '@/services/devices';
import type { Device, User } from '@/lib/types';
import { fmtTime } from '@/lib/utils';
import { useT } from '@/lib/i18n';

const TONE = { pending: 'warn', online: 'ok', offline: 'muted' } as const;

function hasNewerRelease(current: string | undefined, latest: string | undefined) {
  const stable = /^v?(\d+)\.(\d+)\.(\d+)$/;
  const versions = [current, latest].map(v => v?.match(stable)?.slice(1).map(BigInt));
  const [installed, release] = versions;
  if (!installed || !release) return false;
  const different = installed.findIndex((part, index) => part !== release[index]);
  return different !== -1 && installed[different] < release[different];
}

export function Devices({ me }: { me: User }) {
  const t = useT();
  const devices = useDevices(true);
  const canUpgrade = devices.data?.some(canUpgradeDevice) ?? false;
  const latest = useClientRelease(canUpgrade);
  const [name, setName] = useState('');
  const [setupCommands, setSetupCommands] = useState<Record<string, string>>({});
  useEffect(() => {
    if (!devices.data) return;
    const pending = new Set(devices.data.filter(d => d.status === 'pending').map(d => d.id));
    setSetupCommands(current => {
      const remaining = Object.entries(current).filter(([id]) => pending.has(id));
      return remaining.length === Object.keys(current).length ? current : Object.fromEntries(remaining);
    });
  }, [devices.data]);
  const rememberSetup = (id: string, command: string) => setSetupCommands(current => ({ ...current, [id]: command }));
  const forgetSetup = (id: string) => setSetupCommands(current => {
    const next = { ...current };
    delete next[id];
    return next;
  });
  const add = useCreateDevice(rememberSetup);
  return <div className="grid grid-cols-1 gap-4 sm:gap-5">
    <Card>
      <CardHeader><CardTitle>{t('devices.addTitle')}</CardTitle><CardDescription>{t('devices.addText')}</CardDescription></CardHeader>
      <CardContent>
        <form className="flex flex-wrap gap-2" onSubmit={e => { e.preventDefault(); add.mutate(name, { onSuccess: () => setName('') }); }}>
          <Input className="max-w-xs" placeholder={t('devices.namePlaceholder')} value={name} onChange={e => setName(e.target.value)} />
          <Button type="submit" disabled={add.isPending}><Plus />{t('devices.add')}</Button>
        </form>
        {add.isError && <ErrorState error={add.error} />}
      </CardContent>
    </Card>
    {devices.isPending ? <Loading /> : devices.isError ? <ErrorState error={devices.error} />
      : devices.data.length ? devices.data.map(d => <DeviceCard key={d.id} d={d} me={me} latest={latest.data?.version} setupCommand={setupCommands[d.id]} onSetup={rememberSetup} onDelete={() => forgetSetup(d.id)} />)
      : <Empty>{t('devices.empty')}</Empty>}
    {latest.isError && canUpgrade && <p className="text-sm text-muted-foreground">{t('devices.releaseUnavailable')}</p>}
  </div>;
}

function DeviceCard({ d, me, latest, setupCommand, onSetup, onDelete }: { d: Device; me: User; latest?: string; setupCommand?: string; onSetup: (id: string, command: string) => void; onDelete: () => void }) {
  const t = useT();
  const harnesses = useHarnesses();
  const displayName = (n: string) => harnesses.data?.find(a => a.name === n)?.display_name || n;
  const del = useDeleteDevice(d.id);
  const reset = useResetDevice(d.id, onSetup);
  const upgrade = useUpgradeDevice(d.id);
  const toggle = useSetDeviceHarness(d.id);
  const setHarnessEnabled = (harness: string, enabled: boolean) => toggle.mutate({ harness, enabled }, {
    onSuccess: () => toast.success(enabled ? t('devices.enabled') : t('devices.disabled')),
  });
  const tone = TONE[d.status] || 'muted';
  const label = d.status in TONE ? t(`devices.status.${d.status}`) : d.status;
  const meta = [d.id, me.role === 'admin' && d.user_id !== me.id && t('devices.metaUser', { id: d.user_id }), d.last_seen_at && t('devices.metaSeen', { at: fmtTime(d.last_seen_at) })].filter(Boolean).join(' · ');
  return <Card data-device-id={d.id}>
    <CardHeader className="flex-row flex-wrap items-start justify-between gap-2 space-y-0">
      <div className="grid min-w-0 gap-1.5">
        <CardTitle className="flex flex-wrap items-center gap-x-2 gap-y-1 break-words [overflow-wrap:anywhere]">{d.name || t('devices.unnamed')}<StatusBadge tone={tone}>{label}</StatusBadge></CardTitle>
        <CardDescription className="break-words text-xs tabular-nums [overflow-wrap:anywhere]">{meta}</CardDescription>
        {d.status !== 'pending' && <p className="text-sm text-muted-foreground">{t('devices.clientVersion', { version: d.client_version || t('devices.versionUnknown') })}</p>}
      </div>
      <div className="flex flex-wrap gap-2">
        {canUpgradeDevice(d) && hasNewerRelease(d.client_version, latest) &&
          <Button variant="outline" size="sm" disabled={upgrade.isPending || isDeviceUpgrading(d)} onClick={() => upgrade.mutate()}>
            <ArrowUpCircle />{t('devices.upgradeTo', { version: latest! })}
          </Button>}
        <Confirm title={t(d.status === 'pending' ? 'devices.regenerateSetup' : 'devices.resetTitle')} description={t(d.status === 'pending' ? 'devices.regenerateSetupText' : 'devices.resetText')} onConfirm={() => reset.mutate()}>
          <Button variant="ghost" size="sm" disabled={reset.isPending}><RefreshCw />{t(d.status === 'pending' ? 'devices.regenerateSetup' : 'devices.reset')}</Button>
        </Confirm>
        <Confirm title={t('devices.deleteTitle')} description={t('devices.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate(undefined, { onSuccess: onDelete })}>
          <Button variant="ghost" size="sm" className="text-destructive"><Trash2 />{t('common.delete')}</Button>
        </Confirm>
      </div>
    </CardHeader>
    <CardContent>
      {reset.isError && <ErrorState error={reset.error} />}
      {upgrade.isError && <ErrorState error={upgrade.error} />}
      {d.client_upgrade && <div className="mb-2 space-y-1 text-sm">
        <StatusBadge tone={d.client_upgrade.status === 'failed' ? 'bad' : isDeviceUpgrading(d) ? 'run' : 'ok'}>
          {t(`devices.upgrade.${d.client_upgrade.status}`, { version: d.client_upgrade.target_version })}
        </StatusBadge>
        {d.client_upgrade.error && <p className="break-words text-destructive">{d.client_upgrade.error}</p>}
      </div>}
      {d.status === 'online' && !d.client_version && <p className="mb-2 text-sm text-muted-foreground">{t('devices.upgradeLegacy')}</p>}
      {d.status === 'pending' ? setupCommand ? <div className="grid min-w-0 gap-3">
        <div className="space-y-1">
          <h3 className="text-sm font-medium">{t('devices.setupTitle')}</h3>
          <p className="text-sm text-muted-foreground">{t('devices.runSetup')}</p>
        </div>
        <CopyBlock text={setupCommand} copyLabel={t('devices.copySetup')} />
      </div> : <p className="text-sm text-muted-foreground">{t('devices.awaitImport')}</p> : d.harnesses.length ? <div>
        <p className="mb-2 text-sm text-muted-foreground">{t('devices.pickHarnesses')}</p>
        <div className="divide-y">
          {d.harnesses.map(a => <label key={a.harness} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-sm">
            <Switch checked={a.enabled} disabled={!a.available || toggle.isPending} onCheckedChange={enabled => setHarnessEnabled(a.harness, enabled)} />
            <span className="font-medium">{displayName(a.harness)}</span>
            <span className="min-w-0 break-words text-xs text-muted-foreground">{a.harness} {a.version}{!a.available && t('devices.notDetected')}</span>
          </label>)}
        </div>
      </div> : <p className="text-sm text-muted-foreground">{t('devices.noHarnesses')}</p>}
    </CardContent>
  </Card>;
}
