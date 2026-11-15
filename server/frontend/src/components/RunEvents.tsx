import { useState } from 'react';
import { ArrowDownToLine, ChevronRight, CircleHelp, FileJson2, FileText, Flag, MessageSquare } from 'lucide-react';
import type { ConversationEvent } from '@/lib/types';
import { cn } from '@/lib/utils';
import { useT } from '@/lib/i18n';

// Prefer existing display fields. Do not infer actions from topics or native frames.
const titlePaths = [
  ['data', 'title'], ['title'], ['message'], ['text'], ['output'],
  ['data', 'reason'], ['reason'], ['topic'], ['type'],
] as const;

function valueAt(payload: unknown, path: readonly string[]): unknown {
  let value = payload;
  for (const key of path) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
    value = (value as Record<string, unknown>)[key];
  }
  return value;
}

function textAt(payload: unknown, path: readonly string[]) {
  const value = valueAt(payload, path);
  return typeof value === 'string' && value.trim() ? value : undefined;
}

function eventTitle(event: ConversationEvent) {
  for (const path of titlePaths) {
    const value = textAt(event.payload, path);
    if (value) return { value, path: `$.${path.join('.')}` };
  }
  return { value: event.kind, path: 'event.kind' };
}

function eventDate(event: ConversationEvent) {
  const reported = textAt(event.payload, ['ts']);
  const at = reported ? Date.parse(reported) : NaN;
  const date = new Date(Number.isFinite(at) ? at : event.created_at * 1000);
  return Number.isFinite(date.getTime()) ? date : undefined;
}

const eventIcons: Record<string, typeof FileText> = {
  accepted: ArrowDownToLine,
  message: MessageSquare,
  request: CircleHelp,
  result: Flag,
  log: FileText,
};

function EventRow({ event }: { event: ConversationEvent }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const title = eventTitle(event);
  const topic = textAt(event.payload, ['topic']);
  const level = textAt(event.payload, ['level']);
  const status = textAt(event.payload, ['status']) ?? textAt(event.payload, ['data', 'status']);
  const state = textAt(event.payload, ['state']);
  const date = eventDate(event);
  const Icon = eventIcons[event.kind] ?? FileText;
  const tone = level === 'error' || (event.kind === 'result' && status === 'failed')
    ? 'text-destructive'
    : level === 'warning' ? 'text-amber-600 dark:text-amber-400'
    : event.kind === 'result' && status === 'completed' ? 'text-emerald-600 dark:text-emerald-400'
    : 'text-muted-foreground';

  return <li className="relative min-w-0">
    <details className="group/event min-w-0" data-run-event={event.id} onToggle={e => setOpen(e.currentTarget.open)}>
      <summary className="flex cursor-pointer list-none items-start gap-3 rounded-lg px-1 py-2.5 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <span className={cn('relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full border bg-background', tone)}><Icon className="size-3.5" aria-hidden="true" /></span>
        <span className="grid min-w-0 flex-1 gap-1 pt-0.5">
          <span className="line-clamp-2 whitespace-pre-line break-words text-sm leading-5 [overflow-wrap:anywhere]" data-event-title>
            {title.value.length > 280 ? `${title.value.slice(0, 280)}…` : title.value}
          </span>
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
            <span>{event.kind}</span>
            {topic && topic !== title.value && <span className="max-w-full truncate">{topic}</span>}
            {status && <span className="max-w-full break-words rounded bg-muted px-1.5 py-0.5 [overflow-wrap:anywhere]">status: {status}</span>}
            {state && <span className="max-w-full break-words rounded bg-muted px-1.5 py-0.5 [overflow-wrap:anywhere]">state: {state}</span>}
            {level && <span className={tone}>level: {level}</span>}
          </span>
        </span>
        {date && <time className="hidden shrink-0 pt-1 text-[11px] tabular-nums text-muted-foreground sm:inline" dateTime={date.toISOString()} title={date.toLocaleString()}>{date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</time>}
        <ChevronRight className="mt-1.5 size-3.5 shrink-0 text-muted-foreground transition-transform group-open/event:rotate-90" aria-hidden="true" />
      </summary>
      {open && <div className="mb-3 ml-11 min-w-0 rounded-lg border bg-muted/20" data-event-detail>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2 text-[11px] text-muted-foreground">
          <span className="inline-flex items-center gap-1.5"><FileJson2 className="size-3.5" aria-hidden="true" />{t('run.eventJson')}</span>
          <span>{t('run.eventTitleSource')} <code>{title.path}</code></span>
        </div>
        <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words p-3 text-xs leading-5 [overflow-wrap:anywhere]">{JSON.stringify(event.payload ?? null, null, 2)}</pre>
      </div>}
    </details>
  </li>;
}

export function RunEvents({ events }: { events: ConversationEvent[] }) {
  const t = useT();
  return <details open className="group/events mt-6 min-w-0 border-t pt-4" data-run-events>
    <summary className="flex cursor-pointer list-none items-center gap-2 rounded-md py-1 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <FileText className="size-4 text-muted-foreground" aria-hidden="true" />
      <span>{t('run.events')}</span><span className="text-xs font-normal tabular-nums text-muted-foreground">{events.length}</span>
      <ChevronRight className="ml-auto size-3.5 text-muted-foreground transition-transform group-open/events:rotate-90" aria-hidden="true" />
    </summary>
    {events.length ? <ol className="relative mt-3 min-w-0 before:absolute before:bottom-6 before:left-[17px] before:top-6 before:w-px before:bg-border">
      {events.map(event => <EventRow key={event.id} event={event} />)}
    </ol> : <p className="py-4 text-xs text-muted-foreground">{t('run.noEvents')}</p>}
  </details>;
}
