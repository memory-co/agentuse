import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowUp, AtSign, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Input } from '@/components/ui/input';
import { useT } from '@/lib/i18n';
import type { Agent } from '@/lib/types';

export function MentionComposer({ agents, busy, onSend, defaultAgent, placeholder, children }: {
  agents: Agent[]; busy?: boolean; onSend: (text: string, targets: string[]) => Promise<unknown>;
  defaultAgent?: string; placeholder: string; children?: ReactNode;
}) {
  const t = useT();
  const [text, setText] = useState('');
  const [targets, setTargets] = useState<string[]>([]);
  const [query, setQuery] = useState<string | null>(null);
  const [cursor, setCursor] = useState(0);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const mention = useRef<{ start: number; end: number } | null>(null);
  useEffect(() => { if (defaultAgent) setTargets([defaultAgent]); }, [defaultAgent]);

  const options = agents.filter(a => !targets.includes(a.id) && `${a.id} ${a.name}`.toLowerCase().includes((query ?? '').toLowerCase()));
  const manual = query && /^[a-z0-9][a-z0-9_-]{0,63}$/.test(query)
    && !targets.includes(query) && !options.some(a => a.id === query) ? query : null;
  const ids = [...options.map(a => a.id), ...(manual ? [manual] : [])];
  const pick = (id: string) => {
    if (targets.length >= 8 && !targets.includes(id)) { setQuery(null); return; }
    setTargets(v => v.includes(id) ? v : [...v, id]);
    const range = mention.current;
    if (range) setText(v => v.slice(0, range.start) + `@${id} ` + v.slice(range.end));
    mention.current = null;
    setQuery(null);
    setCursor(0);
    textarea.current?.focus();
  };
  const submit = () => {
    if (!text.trim() || busy) return;
    setQuery(null);
    void onSend(text, targets).then(() => setText(''), () => undefined);
  };
  return <form className="relative min-w-0 rounded-xl border bg-background p-2 shadow-sm" onSubmit={e => { e.preventDefault(); submit(); }}>
    <div className="flex flex-wrap gap-1 px-2">
      {targets.map(id => <span key={id} className="inline-flex max-w-full items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs">
        <span className="truncate">@{id}</span>
        <button type="button" disabled={busy} aria-label={`${t('mention.remove')} ${id}`} onClick={() => setTargets(v => v.filter(x => x !== id))}><X className="size-3" /></button>
      </span>)}
    </div>
    <Textarea ref={textarea} value={text} disabled={busy} placeholder={placeholder} enterKeyHint="send"
      className="max-h-60 min-h-[44px] resize-none border-0 p-2 shadow-none focus-visible:ring-0"
      onChange={e => {
        const value = e.target.value, end = e.target.selectionStart;
        setText(value);
        const match = /(?:^|\s)@([a-z0-9_-]*)$/i.exec(value.slice(0, end));
        mention.current = match ? { start: end - match[1].length - 1, end } : null;
        setQuery(match?.[1] ?? null);
        setCursor(0);
      }}
      onKeyDown={e => {
        if (e.nativeEvent.isComposing || e.keyCode === 229) return;
        if (e.key === 'Escape') { setQuery(null); return; }
        if (query !== null) {
          if (ids.length && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
            e.preventDefault(); setCursor(c => (c + (e.key === 'ArrowDown' ? 1 : -1) + ids.length) % ids.length); return;
          }
          if (!e.shiftKey && (e.key === 'Enter' || (e.key === 'Tab' && ids.length))) {
            e.preventDefault(); if (ids.length) pick(ids[cursor % ids.length]); else setQuery(null); return;
          }
        }
        if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submit(); }
      }} />
    {query !== null && <div className="absolute bottom-full left-0 z-20 mb-2 grid max-h-64 w-full gap-1 overflow-auto rounded-lg border bg-popover p-2 shadow-md" role="listbox" aria-label={t('mention.choose')}>
      <Input ref={search} placeholder={t('mention.search')} value={query} onChange={e => { setQuery(e.target.value); setCursor(0); }}
        onKeyDown={e => {
          if (e.nativeEvent.isComposing) return;
          if (e.key === 'Enter') { e.preventDefault(); if (ids.length) pick(ids[cursor % ids.length]); }
          if (e.key === 'Escape') { setQuery(null); textarea.current?.focus(); }
          if (ids.length && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) { e.preventDefault(); setCursor(c => (c + (e.key === 'ArrowDown' ? 1 : -1) + ids.length) % ids.length); }
        }} />
      {options.map((a, i) => <button key={a.id} type="button" role="option" aria-selected={cursor === i}
        className="rounded-md px-2 py-2 text-left text-xs hover:bg-muted aria-selected:bg-muted" onClick={() => pick(a.id)}>
        <span className="font-medium">@{a.id} · {a.name}</span>
        <span className="block break-all text-muted-foreground">{a.user_id} · {a.harness} · {a.daemon_id ?? '—'}</span>
      </button>)}
      {manual && <button type="button" role="option" aria-selected={cursor === ids.length - 1} className="rounded-md p-2 text-left text-xs hover:bg-muted" onClick={() => pick(manual)}>@{manual}</button>}
      {!ids.length && <p className="p-2 text-xs text-muted-foreground">{t('mention.noMatch')}</p>}
    </div>}
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" variant="ghost" size="icon" className="size-8" disabled={busy || targets.length >= 8} aria-label={t('mention.choose')}
        onClick={() => {
          if (query !== null) { setQuery(null); return; }
          mention.current = null; setQuery(''); setCursor(0); requestAnimationFrame(() => search.current?.focus());
        }}><AtSign /></Button>
      <span className="text-xs text-muted-foreground">{targets.length ? t('mention.targets', { n: targets.length }) : t('mention.note')}</span>
      {children}
      <Button type="submit" size="icon" className="ml-auto size-8 rounded-full" disabled={busy || !text.trim()} aria-label={t('session.send')}><ArrowUp /></Button>
    </div>
  </form>;
}
