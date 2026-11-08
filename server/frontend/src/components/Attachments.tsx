import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, Download, Folder, FolderOpen, Plus, Search, Trash2, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState } from '@/components/Shared';
import { FileIcon } from '@/components/MessageFiles';
import { api, ApiError } from '@/lib/api';
import { actAsHeaders } from '@/lib/actas';
import { downloadAttachment } from '@/lib/attachments';
import { queryClient } from '@/lib/query';
import { useT } from '@/lib/i18n';
import { cn } from '@/lib/utils';
import type { Attachment } from '@/lib/types';

interface Node { name: string; path: string; children: Map<string, Node>; file?: Attachment }
function tree(files: Attachment[]) {
  const root = new Map<string, Node>();
  for (const file of files) {
    const parts = file.relative_path.split('/'); let children = root; let path = '';
    parts.forEach((name, index) => {
      path = path ? `${path}/${name}` : name;
      if (!children.has(name)) children.set(name, { name, path, children: new Map() });
      const node = children.get(name)!;
      if (index === parts.length - 1) node.file = file;
      children = node.children;
    });
  }
  return root;
}
function Preview({ file }: { file: Attachment }) {
  const t = useT();
  const image = /^image\/(png|jpeg|gif|webp|avif)$/.test(file.content_type);
  const text = file.content_type.startsWith('text/') || /application\/(json|xml)/.test(file.content_type);
  const tooLarge = file.size_bytes > (image ? 16 * 1024 * 1024 : 512 * 1024);
  const preview = useQuery({ queryKey: ['attachment-preview', file.id], enabled: (image || text) && !tooLarge, staleTime: Infinity,
    queryFn: async () => {
      const response = await fetch(file.download_path, { headers: actAsHeaders(file.download_path) });
      if (!response.ok) throw new ApiError(response.status, (await response.json().catch(() => ({}))).error || response.statusText);
      return image ? response.blob() : response.text();
    } });
  const [url, setUrl] = useState('');
  useEffect(() => { if (!(preview.data instanceof Blob)) { setUrl(''); return; } const next = URL.createObjectURL(preview.data); setUrl(next); return () => URL.revokeObjectURL(next); }, [preview.data]);
  if (tooLarge || (!image && !text)) return <p className="py-6 text-center text-xs text-muted-foreground">{t(tooLarge ? 'attachment.tooLarge' : 'attachment.noPreview')}</p>;
  if (preview.isError) return <ErrorState error={preview.error} />;
  if (preview.isPending) return <p className="py-6 text-center text-xs text-muted-foreground">{t('attachment.loading')}</p>;
  return image ? <img src={url} alt={file.filename} className="mx-auto max-h-60 max-w-full rounded-md object-contain" /> : <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/50 p-3 text-xs leading-5">{typeof preview.data === 'string' ? preview.data : ''}</pre>;
}

export function Attachments({ conversation, files, pending, error, selected, onSelect, onClose, onAttach, busy, referenced }: {
  conversation: string; files: Attachment[]; pending: boolean; error: unknown; selected: Attachment | null;
  onSelect: (file: Attachment | null) => void; onClose: () => void; onAttach: (file: Attachment) => void; busy?: boolean; referenced: Set<string>;
}) {
  const t = useT(); const [search, setSearch] = useState(''); const [collapsed, setCollapsed] = useState(new Set<string>());
  const visible = files.filter(file => file.relative_path.toLowerCase().includes(search.toLowerCase()));
  const download = useMutation({ mutationFn: downloadAttachment });
  const remove = useMutation({ mutationFn: (file: Attachment) => api(`/api/conversations/${conversation}/attachments/${file.id}`, { method: 'DELETE' }), onSuccess: (_, file) => { if (selected?.id === file.id) onSelect(null); void queryClient.invalidateQueries({ queryKey: ['attachments', conversation] }); } });
  const nodes = (items: Map<string, Node>, depth = 0): ReactNode => Array.from(items.values()).sort((a, b) => Number(!!a.file) - Number(!!b.file) || (a.name === 'user_input' ? -1 : b.name === 'user_input' ? 1 : a.name.localeCompare(b.name))).map(node => {
    const open = !collapsed.has(node.path);
    return node.file ? <button type="button" role="treeitem" aria-selected={selected?.id === node.file.id} key={node.path} data-file-path={node.path} title={node.path} onClick={() => onSelect(node.file!)} className={cn('flex w-full min-w-0 items-center gap-2 rounded-md py-2 pr-2 text-left text-xs hover:bg-muted', selected?.id === node.file.id && 'bg-muted font-medium')} style={{ paddingLeft: `${depth * 14 + 10}px` }}><FileIcon mime={node.file.content_type} /><span className="min-w-0 flex-1 truncate">{node.name}</span><span className="shrink-0 text-[10px] text-muted-foreground">{node.file.size_bytes.toLocaleString()} B</span></button>
      : <div key={node.path} role="treeitem" aria-expanded={open}><button type="button" className="flex w-full items-center gap-1.5 rounded-md py-2 pr-2 text-left text-xs font-medium hover:bg-muted" style={{ paddingLeft: `${depth * 14 + 4}px` }} onClick={() => setCollapsed(current => { const next = new Set(current); next.has(node.path) ? next.delete(node.path) : next.add(node.path); return next; })}>{open ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}{open ? <FolderOpen className="size-4 text-muted-foreground" /> : <Folder className="size-4 text-muted-foreground" />}<span className="truncate">{node.name}</span></button>{open && <div role="group">{nodes(node.children, depth + 1)}</div>}</div>;
  });
  return <section className="flex h-full min-h-0 min-w-0 flex-col bg-background" aria-label={t('attachment.files')}>
    <div className="flex h-14 shrink-0 items-center gap-2 border-b px-4"><FolderOpen className="size-4" /><h2 className="text-sm font-medium">{t('attachment.files')}</h2><span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{files.length}</span><Button variant="ghost" size="icon" className="ml-auto size-7" aria-label={t('attachment.close')} onClick={onClose}><X /></Button></div>
    <div className="relative m-3 shrink-0"><Search className="absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" /><Input className="h-9 pl-8 text-xs" value={search} onChange={event => setSearch(event.target.value)} aria-label={t('attachment.search')} placeholder={t('attachment.search')} /></div>
    <div className="min-h-24 flex-1 overflow-y-auto px-2 pb-3" role="tree" aria-label={t('attachment.files')}><p className="px-2 py-1 font-mono text-xs text-muted-foreground">/</p>{nodes(tree(visible))}{pending && <p className="p-3 text-xs text-muted-foreground">{t('attachment.loading')}</p>}{!pending && !visible.length && <p className="p-3 text-xs text-muted-foreground">{t('attachment.empty')}</p>}{!!error && <ErrorState error={error} />}</div>
    {selected ? <div className="max-h-[55%] shrink-0 overflow-y-auto border-t p-4" aria-label={t('attachment.preview')}><div className="mb-3 flex items-start gap-2"><FileIcon mime={selected.content_type} /><div className="min-w-0 flex-1"><p className="break-all text-xs font-medium">{selected.relative_path}</p><p className="mt-1 text-[11px] text-muted-foreground">{selected.size_bytes.toLocaleString()} B · {selected.created_by_agent_id ? `@${selected.created_by_agent_id}` : selected.created_by}</p></div></div><Preview key={selected.id} file={selected} /><div className="mt-3 flex flex-wrap gap-1"><Button variant="outline" size="sm" disabled={download.isPending} onClick={() => download.mutate(selected)}><Download />{t('attachment.download')}</Button><Button variant="ghost" size="sm" disabled={busy} onClick={() => onAttach(selected)}><Plus />{t('attachment.attach')}</Button>{!referenced.has(selected.id) && <Confirm title={t('attachment.delete')} description={selected.relative_path} onConfirm={() => remove.mutate(selected)}><Button size="icon" variant="ghost" className="ml-auto size-8 text-destructive" aria-label={t('attachment.delete')} disabled={remove.isPending}><Trash2 /></Button></Confirm>}</div>{!referenced.has(selected.id) && <p className="mt-2 text-[10px] text-muted-foreground">{t('attachment.unreferenced')}</p>}</div> : <div className="border-t p-5 text-center text-xs text-muted-foreground">{t('attachment.select')}</div>}
    {[download.error, remove.error].filter(Boolean).map((value, index) => <div className="p-3" key={index}><ErrorState error={value} /></div>)}
  </section>;
}
