import { useEffect, useState, type FormEvent } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { FileText, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import { useAgents } from '@/pages/Agents';
import { Conversation } from '@/pages/Sessions';
import type { Daemon, Session, User, Work } from '@/lib/types';
import { useT } from '@/lib/i18n';

// 工作：协调者 + 线程 + 共用说明(docs/v2/designs/work.md)。
export const useWorks = () => useQuery({ queryKey: ['works'], queryFn: () => api<Work[]>('/api/works'), refetchInterval: 5000 });
export const useWork = (id: string) => useQuery({ queryKey: ['works', id], queryFn: () => api<Work>(`/api/works/${encodeURIComponent(id)}`) });
const refresh = () => Promise.all([queryClient.invalidateQueries({ queryKey: ['works'] }), queryClient.invalidateQueries({ queryKey: ['sessions'] })]);

/** 新建工作：名字、协调者用的 agent(下拉，只列自己的;运行时用不了的灰掉)、说明。 */
export function NewWork({ me, onCreated }: { me: User; onCreated: (id: string) => void }) {
  const t = useT();
  const agents = useAgents();
  const daemons = useQuery({ queryKey: ['daemons'], queryFn: () => api<Daemon[]>('/api/daemons'), staleTime: 10_000 });
  const [name, setName] = useState('');
  const [agent, setAgent] = useState('');
  const [instructions, setInstructions] = useState('');
  const options = (agents.data || []).filter(a => a.user_id === me.id).map(a => {
    const d = daemons.data?.find(d => d.id === a.daemon_id);
    const ok = !!d?.harnesses.some(h => h.harness === a.harness && h.available && h.enabled);
    return { id: a.id, ok, label: `${a.name} · ${d?.name ?? '?'} · ${a.harness}${!ok ? t('agents.unavailable') : d?.status === 'online' ? '' : t('agents.offline')}` };
  });
  useEffect(() => { if (!agent && daemons.data) { const o = options.find(o => o.ok); if (o) setAgent(o.id); } }, [agent, daemons.data, options]);
  const create = useMutation({
    mutationFn: () => api<{ work: Work; coordinator: Session }>('/api/works', { method: 'POST', body: { name, agent_id: agent, instructions } }),
    onSuccess: r => { void refresh(); onCreated(r.work.id); },
  });
  const submit = (e: FormEvent) => { e.preventDefault(); if (name.trim() && agent) create.mutate(); };
  return <div className="mx-auto w-full max-w-2xl px-4 py-6">
    <Card>
      <CardHeader><CardTitle>{t('work.newTitle')}</CardTitle><CardDescription>{t('work.newText')}</CardDescription></CardHeader>
      <CardContent>
        <form className="grid gap-3" onSubmit={submit}>
          <Input placeholder={t('work.namePlaceholder')} value={name} onChange={e => setName(e.target.value)} aria-label={t('common.name')} autoFocus />
          <label className="grid gap-1 text-sm">
            <span className="text-muted-foreground">{t('work.agentLabel')}</span>
            <Select value={agent} onValueChange={setAgent}>
              <SelectTrigger><SelectValue placeholder={t('work.pickAgent')} /></SelectTrigger>
              <SelectContent>
                {options.map(o => <SelectItem key={o.id} value={o.id} disabled={!o.ok}>{o.label}</SelectItem>)}
                {agents.isSuccess && !options.length && <p className="px-2 py-1.5 text-sm text-muted-foreground">{t('work.noAgents')}</p>}
              </SelectContent>
            </Select>
          </label>
          <Textarea rows={5} placeholder={t('work.instructionsPlaceholder')} value={instructions} onChange={e => setInstructions(e.target.value)} aria-label={t('work.instructions')} />
          {create.isError && <ErrorState error={create.error} />}
          <div><Button type="submit" disabled={!name.trim() || !agent || create.isPending}>{t('common.create')}</Button></div>
        </form>
      </CardContent>
    </Card>
  </div>;
}

/** 工作页：上面一行工具(说明、删除),下面就是和协调者的对话。线程由协调者开，用户不能手动开。 */
export function WorkPage({ id, onDeleted }: { id: string; onDeleted: () => void }) {
  const t = useT();
  const w = useWork(id);
  const [showInstructions, setShowInstructions] = useState(false);
  const del = useMutation({ mutationFn: () => api(`/api/works/${encodeURIComponent(id)}`, { method: 'DELETE' }), onSuccess: () => { void refresh(); onDeleted(); } });
  if (w.isPending) return <div className="px-6"><Loading /></div>;
  if (w.isError) return <div className="px-6 py-6"><ErrorState error={w.error} /></div>;
  const work = w.data;
  return <div className="flex min-h-0 flex-1 flex-col">
    <div className="shrink-0 border-b pb-2">
    <div className="mx-auto flex w-full max-w-3xl flex-wrap items-center gap-1 px-4 pt-2">
      <Button variant={showInstructions ? 'secondary' : 'ghost'} size="sm" className="h-7 gap-1 px-2 text-xs" onClick={() => setShowInstructions(v => !v)}><FileText />{t('work.instructions')}</Button>
      <span className="text-xs text-muted-foreground">{t('work.threads', { n: work.thread_count })}</span>
      <Confirm title={t('work.deleteTitle', { name: work.name })} description={t('work.deleteText')} action={t('common.delete')} onConfirm={() => del.mutate()}>
        <Button variant="ghost" size="icon" className="ml-auto size-7 text-destructive" aria-label={t('work.delete')}><Trash2 /></Button>
      </Confirm>
    </div>
    {showInstructions && <WorkSettings key={work.id + work.updated_at} work={work} />}
    </div>
    <Conversation id={work.coordinator_id} onDeleted={onDeleted} placeholder={t('work.coordinatorPlaceholder')} />
  </div>;
}

/** 改名字和说明。 */
function WorkSettings({ work }: { work: Work }) {
  const t = useT();
  const [name, setName] = useState(work.name);
  const [instructions, setInstructions] = useState(work.instructions);
  const save = useMutation({
    mutationFn: () => api<Work>(`/api/works/${encodeURIComponent(work.id)}`, { method: 'PATCH', body: { name, instructions } }),
    onSuccess: () => { void refresh(); toast.success(t('work.saved')); },
  });
  const dirty = name !== work.name || instructions !== work.instructions;
  return <form className="mx-auto grid w-full max-w-3xl gap-2 px-4 pt-3" onSubmit={e => { e.preventDefault(); if (name.trim() && dirty) save.mutate(); }}>
    <Input value={name} onChange={e => setName(e.target.value)} aria-label={t('common.name')} />
    <Textarea rows={6} value={instructions} placeholder={t('work.instructionsPlaceholder')} onChange={e => setInstructions(e.target.value)} aria-label={t('work.instructions')} />
    <div className="flex items-center gap-2">
      <Button type="submit" size="sm" disabled={!name.trim() || !dirty || save.isPending}>{t('work.save')}</Button>
      <span className="text-xs text-muted-foreground">{t('work.instructionsHint')}</span>
    </div>
  </form>;
}
