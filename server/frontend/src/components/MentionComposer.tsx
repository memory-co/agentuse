import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ArrowUp, AtSign, ChevronDown, Paperclip, UserRound, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Input } from '@/components/ui/input';
import { useT } from '@/lib/i18n';
import { DraftFiles } from '@/components/MessageFiles';
import type { DraftFile } from '@/hooks/use-message-files';
import type { Agent } from '@/lib/types';

export function MentionComposer({ agents, users = [], busy, onSend, initialTargets = [], placeholder, children, showAgentPicker = false, files = [], onFiles, onRemoveFile }: {
  agents: Agent[]; users?: string[]; busy?: boolean; onSend: (text: string, toAgentIds: string[], toUserIds: string[]) => Promise<unknown>;
  files?: DraftFile[]; onFiles?: (files: File[]) => void; onRemoveFile?: (key: string) => void;
  initialTargets?: string[]; placeholder: string; children?: ReactNode; showAgentPicker?: boolean;
}) {
  const t = useT();
  const [text, setText] = useState('');
  const [targets, setTargets] = useState<string[]>(() => [...initialTargets]);
  const targetsEdited = useRef(false);
  // History may arrive from a fresher snapshot after mount. Seed it until the
  // user chooses recipients; background refreshes must preserve that choice.
  useEffect(() => {
    if (!targetsEdited.current) setTargets(current =>
      current.length === initialTargets.length && current.every((id, index) => id === initialTargets[index]) ? current : [...initialTargets]);
  }, [initialTargets]);
  const changeTargets = (update: (current: string[]) => string[]) => {
    targetsEdited.current = true;
    setTargets(update);
  };
  const [userTargets, setUserTargets] = useState<string[]>([]);
  const [userQuery, setUserQuery] = useState('');
  const [query, setQuery] = useState<string | null>(null);
  const [cursor, setCursor] = useState(0);
  const [pickerQuery, setPickerQuery] = useState('');
  const fileInput = useRef<HTMLInputElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const mention = useRef<{ start: number; end: number } | null>(null);
  const pickUser = (id: string) => {
    id = id.trim();
    if (!id) return;
    setUserTargets(current => current.includes(id) ? current : [...current, id]);
    setUserQuery('');
  };

  const options = agents.filter(a => !targets.includes(a.id) && `${a.id} ${a.name}`.toLowerCase().includes((query ?? '').toLowerCase()));
  const manual = query && /^[a-z0-9][a-z0-9_-]{0,63}$/.test(query)
    && !targets.includes(query) && !options.some(a => a.id === query) ? query : null;
  const ids = [...options.map(a => a.id), ...(manual ? [manual] : [])];
  const pick = (id: string) => {
    if (targets.length >= 8 && !targets.includes(id)) { setQuery(null); return; }
    changeTargets(v => v.includes(id) ? v : [...v, id]);
    const range = mention.current;
    if (range) setText(v => v.slice(0, range.start) + `@${id} ` + v.slice(range.end));
    mention.current = null;
    setQuery(null);
    setCursor(0);
    textarea.current?.focus();
  };
  const submit = () => {
    if ((!text.trim() && !files.length) || busy) return;
    setQuery(null);
    void onSend(text, targets, userTargets).then(() => setText(''), () => undefined);
  };
  return <form className="relative min-w-0 rounded-xl border bg-background p-2 shadow-sm" onSubmit={e => { e.preventDefault(); submit(); }}>
    {showAgentPicker && <div className="mb-2 border-b px-2 pb-3">
      <Popover onOpenChange={() => setPickerQuery('')}>
        <PopoverTrigger asChild>
          <Button type="button" variant="outline" disabled={busy} className="w-full justify-between gap-2" aria-label={t('mention.multi')}>
            <span className="min-w-0 truncate">{targets.length ? t('mention.targets', { n: targets.length }) : t('mention.multi')}</span>
            <ChevronDown className="size-4 shrink-0 text-muted-foreground" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] max-w-[calc(100vw-2rem)] p-2" onKeyDown={e => { if (e.key === 'Enter') e.preventDefault(); }}>
          <Input aria-label={t('mention.search')} placeholder={t('mention.search')} value={pickerQuery} onChange={e => setPickerQuery(e.target.value)} />
          <div className="mt-2 max-h-60 overflow-y-auto" role="group" aria-label={t('mention.multi')}>
            {agents.filter(agent => `${agent.id} ${agent.name}`.toLowerCase().includes(pickerQuery.toLowerCase())).map(agent => {
              const selected = targets.includes(agent.id);
              return <label key={agent.id} className="flex min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm hover:bg-muted">
                <input type="checkbox" checked={selected} disabled={busy || (!selected && targets.length >= 8)}
                  onChange={() => changeTargets(current => current.includes(agent.id) ? current.filter(id => id !== agent.id) : current.length < 8 ? [...current, agent.id] : current)} />
                <span className="min-w-0 break-words">{agent.name}<span className="ml-1 text-xs text-muted-foreground">@{agent.id}</span></span>
              </label>;
            })}
            {!agents.some(agent => `${agent.id} ${agent.name}`.toLowerCase().includes(pickerQuery.toLowerCase())) && <p className="p-2 text-xs text-muted-foreground">{t(agents.length ? 'mention.noMatch' : 'conversation.noAgents')}</p>}
          </div>
          <p className="mt-2 border-t px-2 pt-2 text-xs text-muted-foreground">{t('mention.multiHint')}</p>
        </PopoverContent>
      </Popover>
    </div>}
    <div className="flex flex-wrap gap-1 px-2">
      {targets.map(id => <span key={id} className="inline-flex max-w-full items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs">
        <span className="truncate">@{id}</span>
        <button type="button" disabled={busy} aria-label={`${t('mention.remove')} ${id}`} onClick={() => changeTargets(v => v.filter(x => x !== id))}><X className="size-3" /></button>
      </span>)}
      {userTargets.map(id => <span key={`user:${id}`} className="inline-flex max-w-full items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs">
        <UserRound className="size-3 shrink-0" /><span className="truncate">{id}</span>
        <button type="button" disabled={busy} aria-label={`${t('mention.removeUser')} ${id}`} onClick={() => setUserTargets(v => v.filter(x => x !== id))}><X className="size-3" /></button>
      </span>)}
    </div>
    <DraftFiles files={files} busy={busy} onRemove={onRemoveFile ?? (() => undefined)} />
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
        <span className="block break-all text-muted-foreground">{a.user_id} · {a.harness} · {a.device_id ?? '—'}</span>
      </button>)}
      {manual && <button type="button" role="option" aria-selected={cursor === ids.length - 1} className="rounded-md p-2 text-left text-xs hover:bg-muted" onClick={() => pick(manual)}>@{manual}</button>}
      {!ids.length && <p className="p-2 text-xs text-muted-foreground">{t('mention.noMatch')}</p>}
    </div>}
    <div className="flex flex-wrap items-center gap-2">
      {onFiles && <><Button type="button" variant="ghost" size="icon" className="size-8" disabled={busy || files.length >= 64} aria-label={t('attachment.upload')} onClick={() => fileInput.current?.click()}><Paperclip className="size-4" /></Button><input ref={fileInput} type="file" multiple className="hidden" disabled={busy} aria-label={t('attachment.upload')} onChange={event => { const next=Array.from(event.target.files ?? []); event.target.value=''; onFiles(next); }} /></>}

      {!showAgentPicker && <Button type="button" variant="outline" size="sm" className="h-8 gap-1.5" disabled={busy || targets.length >= 8} aria-label={t('mention.choose')}
        onClick={() => {
          if (query !== null) { setQuery(null); return; }
          mention.current = null; setQuery(''); setCursor(0); requestAnimationFrame(() => search.current?.focus());
        }}><AtSign className="size-4" />{t('mention.choose')}</Button>}
      <Popover onOpenChange={() => setUserQuery('')}>
        <PopoverTrigger asChild><Button type="button" variant="outline" size="sm" className="h-8 gap-1.5" disabled={busy}><UserRound className="size-4" />{t('mention.users')}</Button></PopoverTrigger>
        <PopoverContent align="start" className="w-80 max-w-[calc(100vw-2rem)] p-3">
          <div className="flex gap-2">
            <Input aria-label={t('mention.userId')} placeholder={t('mention.userId')} value={userQuery} onChange={e => setUserQuery(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && !e.nativeEvent.isComposing) { e.preventDefault(); pickUser(userQuery); } }} />
            <Button type="button" size="sm" disabled={!userQuery.trim()} onClick={() => pickUser(userQuery)}>{t('mention.addUser')}</Button>
          </div>
          <div className="mt-2 grid max-h-48 gap-1 overflow-y-auto">
            {users.filter(id => !userTargets.includes(id) && id.toLowerCase().includes(userQuery.toLowerCase())).map(id => <button key={id} type="button" className="break-all rounded-md px-2 py-2 text-left text-sm hover:bg-muted" onClick={() => pickUser(id)}>{id}</button>)}
          </div>
          <p className="mt-2 text-xs text-muted-foreground">{t('mention.userHint')}</p>
        </PopoverContent>
      </Popover>
      <span className="text-xs text-muted-foreground">{targets.length || userTargets.length ? t('mention.recipients', { agents: targets.length, users: userTargets.length }) : t('mention.note')}</span>
      {children}
      <Button type="submit" size="icon" className="ml-auto size-8 rounded-full" disabled={busy || (!text.trim() && !files.length)} aria-label={t('conversation.send')}><ArrowUp /></Button>
    </div>
  </form>;
}
