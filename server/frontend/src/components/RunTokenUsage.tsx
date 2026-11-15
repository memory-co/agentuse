import type { RunUsage } from '@/lib/types';
import { useT } from '@/lib/i18n';

const counters = [
  ['input_tokens', 'run.tokenInput', 'run.tokenInputShort'],
  ['output_tokens', 'run.tokenOutput', 'run.tokenOutputShort'],
  ['cache_read_tokens', 'run.tokenCacheRead', 'run.tokenCacheReadShort'],
  ['cache_write_tokens', 'run.tokenCacheWrite', 'run.tokenCacheWriteShort'],
] as const;

export function RunTokenUsage({ usage, compact = false }: { usage: RunUsage | null; compact?: boolean }) {
  const t = useT();
  const count = (key: keyof RunUsage) => usage?.[key] == null
    ? compact ? '—' : t('run.tokenUnreported')
    : usage[key]!.toLocaleString();

  if (compact) return <span className="inline-flex max-w-full flex-wrap gap-x-3 gap-y-1 tabular-nums" aria-label={t('run.tokenUsage')} data-run-token-summary>
    <span>{t('run.tokenUsage')}</span>
    {counters.map(([key, , short]) => <span key={key} className="inline-flex items-center gap-1 whitespace-nowrap" data-usage-field={key} title={usage?.[key] == null ? t('run.tokenUnreported') : undefined}>
      <span>{t(short)}</span><span className="font-medium text-foreground/80">{count(key)}</span>
    </span>)}
  </span>;

  return <section className="my-4 grid gap-2" aria-label={t('run.tokenUsage')} data-run-token-usage>
    <h3 className="text-sm font-medium">{t('run.tokenUsage')}</h3>
    <dl className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {counters.map(([key, label]) => <div key={key} className="min-w-0 rounded-lg border bg-muted/20 px-3 py-2.5" data-usage-field={key}>
        <dt className="text-xs text-muted-foreground">{t(label)}</dt>
        <dd className="mt-1 break-words text-base font-medium tabular-nums">{count(key)}</dd>
      </div>)}
    </dl>
    <p className="text-xs leading-5 text-muted-foreground">{t('run.tokenHint')}</p>
  </section>;
}
