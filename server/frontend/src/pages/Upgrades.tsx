import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ArrowUpCircle, ExternalLink, LoaderCircle, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Empty, ErrorState, Loading, StatusBadge } from '@/components/Shared';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { queryClient } from '@/lib/query';
import { fmtTime } from '@/lib/utils';

type UpgradeStatus = 'checking' | 'downloading' | 'prepared' | 'installing' | 'restarting' | 'succeeded' | 'failed' | 'unchanged';
type Upgrade = {
  id: string; from_version: string; target_version: string; status: UpgradeStatus; release_url: string;
  downloaded_bytes: number; total_bytes: number; error: string; requested_by: string;
  created_at: number; updated_at: number; completed_at: number | null;
};
type State = {
  current_version: string; platform: string; supported: boolean; reason: string;
  active: Upgrade | null; upgrades: Upgrade[]; next_cursor: string | null;
};
type Release = { version: string; current_version: string; available: boolean; url: string; notes: string; published_at: string };
const bytes = (value: number) => value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / 1024 / 1024).toFixed(1)} MiB`;

export function Upgrades() {
  const t = useT();
  const [cursor, setCursor] = useState('');
  const state = useQuery({
    queryKey: ['upgrades', cursor],
    queryFn: () => api<State>(`/api/upgrades${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`),
    refetchInterval: query => query.state.data?.active || query.state.error ? 1500 : false,
  });
  const loadedVersion = useRef<string>();
  useEffect(() => {
    const current = state.data?.current_version;
    if (!current) return;
    if (loadedVersion.current && loadedVersion.current !== current) location.reload();
    loadedVersion.current = current;
  }, [state.data?.current_version]);
  const check = useMutation({ mutationFn: () => api<Release>('/api/upgrades/latest') });
  const start = useMutation({
    mutationFn: () => api<Upgrade>('/api/upgrades', { method: 'POST' }),
    onSuccess: () => { setCursor(''); void queryClient.invalidateQueries({ queryKey: ['upgrades'] }); },
  });
  if (state.isPending) return <Loading />;
  if (!state.data) return <ErrorState error={state.error} />;
  const data = state.data;
  const active = data.active;
  const available = check.data && check.data.version !== data.current_version && check.data.available;
  return <div className="grid min-w-0 gap-5 [overflow-wrap:anywhere]" data-upgrades-page>
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base"><ArrowUpCircle className="size-5" /><h1>{t('upgrade.title')}</h1></CardTitle>
        <CardDescription>{t('upgrade.description')}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('upgrade.current')}</dt><dd className="break-all font-medium" data-current-version>{data.current_version}</dd>
          <dt className="text-muted-foreground">{t('upgrade.platform')}</dt><dd>{data.platform}</dd>
          {check.data && <><dt className="text-muted-foreground">{t('upgrade.latest')}</dt><dd className="flex flex-wrap items-center gap-2"><span>{check.data.version}</span><StatusBadge tone={available ? 'warn' : 'ok'}>{t(available ? 'upgrade.available' : 'upgrade.currentLatest')}</StatusBadge>{check.data.url && <a href={check.data.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-muted-foreground underline">{t('upgrade.releaseNotes')}<ExternalLink className="size-3" /></a>}</dd></>}
        </dl>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={check.isPending || !!active} onClick={() => check.mutate()}>{check.isPending ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}{t('upgrade.check')}</Button>
          <Button disabled={!data.supported || !!active || start.isPending || (check.isSuccess && !available)} onClick={() => start.mutate()}>{start.isPending || active ? <LoaderCircle className="animate-spin" /> : <ArrowUpCircle />}{t('upgrade.start')}</Button>
        </div>
        {!data.supported && <p className="text-sm text-muted-foreground">{data.reason}</p>}
        {state.isError && <p className="flex items-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />{t('upgrade.reconnecting')}</p>}
        {check.isError && <ErrorState error={check.error} />}
        {start.isError && <ErrorState error={start.error} />}
      </CardContent>
    </Card>
    {active && <Card data-active-upgrade={active.id}>
      <CardHeader><CardTitle className="flex items-center gap-2 text-base"><LoaderCircle className="size-4 animate-spin" /><h2>{t(`upgrade.status.${active.status}`)}</h2></CardTitle><CardDescription>{active.from_version} → {active.target_version || t('upgrade.discovering')}</CardDescription></CardHeader>
      <CardContent className="grid gap-2">
        {active.total_bytes > 0 && <><progress className="h-2 w-full accent-primary" max={active.total_bytes} value={active.downloaded_bytes} aria-label={t('upgrade.progress')} /><p className="text-xs text-muted-foreground">{bytes(active.downloaded_bytes)} / {bytes(active.total_bytes)}</p></>}
        {active.status === 'restarting' && <p className="text-sm text-muted-foreground">{t('upgrade.reconnecting')}</p>}
      </CardContent>
    </Card>}
    <div className="flex items-center justify-between gap-2"><h2 className="text-base font-semibold">{t('upgrade.history')}</h2><Button variant="ghost" size="sm" onClick={() => void state.refetch()}><RefreshCw />{t('upgrade.refresh')}</Button></div>
    {data.upgrades.length ? <ul className="grid gap-3">{data.upgrades.map(item => <li key={item.id} className="grid gap-2 rounded-lg border p-4" data-upgrade-record={item.id}>
      <div className="flex flex-wrap items-center justify-between gap-2"><span className="break-all text-sm font-medium">{item.from_version} → {item.target_version || t('upgrade.discovering')}</span><StatusBadge tone={item.status === 'succeeded' || item.status === 'unchanged' ? 'ok' : item.status === 'failed' ? 'bad' : 'run'}>{t(`upgrade.status.${item.status}`)}</StatusBadge></div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"><span>{fmtTime(item.created_at)}</span><span className="break-all">{t('upgrade.requestedBy')}: {item.requested_by}</span>{item.completed_at && <span>{t('upgrade.finished')}: {fmtTime(item.completed_at)}</span>}</div>
      {item.error && <p className="break-words text-sm text-destructive">{item.error}</p>}
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">{t('upgrade.recordId')}</summary><code className="mt-1 block break-all">{item.id}</code></details>
    </li>)}</ul> : <Empty>{t('upgrade.empty')}</Empty>}
    <div className="flex justify-end gap-2">{cursor && <Button variant="outline" size="sm" onClick={() => setCursor('')}>{t('upgrade.firstPage')}</Button>}{data.next_cursor && <Button variant="outline" size="sm" onClick={() => setCursor(data.next_cursor!)}>{t('upgrade.more')}</Button>}</div>
  </div>;
}
