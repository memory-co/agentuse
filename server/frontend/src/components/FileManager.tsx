import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, Download, Folder, FolderOpen, Search, Trash2, X, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Confirm, ErrorState, Loading } from '@/components/Shared';
import { FileIcon } from '@/components/MessageFiles';
import { api, ApiError } from '@/lib/api';
import { actAsHeaders } from '@/lib/actas';
import { downloadAttachment } from '@/lib/attachments';
import { queryClient } from '@/lib/query';
import { useT } from '@/lib/i18n';
import { cn } from '@/lib/utils';
import type { Attachment } from '@/lib/types';

interface FileNode { name: string; path: string; children: Map<string, FileNode>; file?: Attachment }
function fileTree(files: Attachment[]) {
  const root = new Map<string, FileNode>();
  for (const file of files) {
    let children = root;
    let path = '';
    const parts = file.relative_path.split('/');
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
const sortedNodes = (nodes: Map<string, FileNode>) => Array.from(nodes.values()).sort((a, b) =>
  Number(!!a.file) - Number(!!b.file)
  || (a.name === 'user_input' ? -1 : b.name === 'user_input' ? 1 : a.name.localeCompare(b.name)));

function FilePreview({ file }: { file: Attachment }) {
  const t = useT();
  const image = /^image\/(png|jpeg|gif|webp|avif)$/.test(file.content_type);
  const text = file.content_type.startsWith('text/') || /application\/(json|xml)/.test(file.content_type);
  const tooLarge = file.size_bytes > (image ? 16 * 1024 * 1024 : 512 * 1024);
  const preview = useQuery({
    queryKey: ['attachment-preview', file.id], enabled: (image || text) && !tooLarge, staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await fetch(file.download_path, { headers: actAsHeaders(file.download_path), signal });
      if (!response.ok) throw new ApiError(response.status, (await response.json().catch(() => ({}))).error || response.statusText);
      return image ? response.blob() : response.text();
    },
  });
  const [url, setUrl] = useState('');
  useEffect(() => {
    if (!(preview.data instanceof Blob)) { setUrl(''); return; }
    const next = URL.createObjectURL(preview.data);
    setUrl(next);
    return () => URL.revokeObjectURL(next);
  }, [preview.data]);
  if (tooLarge || (!image && !text)) return <div className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">{t(tooLarge ? 'attachment.tooLarge' : 'attachment.noPreview')}</div>;
  if (preview.isError) return <div className="p-6"><ErrorState error={preview.error} /></div>;
  if (preview.isPending) return <div className="flex h-full items-center justify-center"><Loading /></div>;
  return image
    ? <div className="flex h-full items-center justify-center p-4 sm:p-6"><img src={url} alt={file.filename} className="max-h-full max-w-full rounded-md object-contain" /></div>
    : <pre className="min-h-full whitespace-pre-wrap break-words p-4 font-mono text-xs leading-6 sm:p-6 sm:text-sm">{typeof preview.data === 'string' ? preview.data : ''}</pre>;
}

interface FileBrowserProps {
  conversation: string; files: Attachment[]; pending: boolean; error: unknown; selected: Attachment | null;
  onSelect: (file: Attachment | null) => void; onAttach: (file: Attachment) => void; busy?: boolean; referenced: Set<string>;
}

export function FileManager({ open, onOpenChange, onRestoreFocus, ...props }: FileBrowserProps & {
  open: boolean; onOpenChange: (open: boolean) => void; onRestoreFocus: () => void;
}) {
  const t = useT();
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-black/45 backdrop-blur-sm" />
      <Dialog.Content
        className="fixed left-1/2 top-1/2 z-50 flex h-[calc(100dvh-1rem)] w-[calc(100%-1rem)] max-w-6xl -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border bg-background shadow-2xl sm:h-[85dvh] sm:w-[calc(100%-3rem)]"
        data-conversation-files={props.conversation}
        onCloseAutoFocus={event => { event.preventDefault(); onRestoreFocus(); }}
      >
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
          <FolderOpen className="size-4 text-muted-foreground" />
          <Dialog.Title className="text-sm font-semibold">{t('attachment.manager')}</Dialog.Title>
          <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">{props.files.length}</span>
          <Dialog.Description className="sr-only">{t('attachment.managerDescription')}</Dialog.Description>
          <Dialog.Close asChild><Button variant="ghost" size="icon" className="ml-auto size-8" aria-label={t('attachment.close')}><X /></Button></Dialog.Close>
        </header>
        <FileBrowser {...props} />
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}

function FileBrowser({ conversation, files, pending, error, selected, onSelect, onAttach, busy, referenced }: FileBrowserProps) {
  const t = useT();
  const [search, setSearch] = useState('');
  const [folder, setFolder] = useState('');
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const [pane, setPane] = useState<'files' | 'preview'>(selected ? 'preview' : 'files');
  const [focused, setFocused] = useState('');
  const treeRef = useRef<HTMLDivElement>(null);
  const selectedFile = files.find(file => file.id === selected?.id) ?? selected;
  const path = selectedFile?.relative_path ?? folder;
  const parts = path.split('/').filter(Boolean);
  const visible = files.filter(file => file.relative_path.toLowerCase().includes(search.toLowerCase()));
  const roots = fileTree(visible);
  const visiblePaths = new Set<string>();
  const collectPaths = (items: Map<string, FileNode>) => {
    for (const node of sortedNodes(items)) {
      visiblePaths.add(node.path);
      if (!node.file && (search || !collapsed.has(node.path))) collectPaths(node.children);
    }
  };
  collectPaths(roots);
  const focusPath = visiblePaths.has(focused) ? focused : visiblePaths.values().next().value;
  const download = useMutation({ mutationFn: downloadAttachment });
  const remove = useMutation({
    mutationFn: (file: Attachment) => api(`/api/conversations/${conversation}/attachments/${file.id}`, { method: 'DELETE' }),
    onSuccess: (_, file) => {
      if (selected?.id === file.id) { setFolder(file.relative_path.split('/').slice(0, -1).join('/')); onSelect(null); setPane('files'); }
      queryClient.removeQueries({ queryKey: ['attachment-preview', file.id] });
      void queryClient.invalidateQueries({ queryKey: ['attachments', conversation] });
    },
  });
  useEffect(() => {
    if (!selected) return;
    setCollapsed(current => new Set([...current].filter(value => !selected.relative_path.startsWith(`${value}/`))));
    setFocused(selected.relative_path);
  }, [selected?.id]);
  useEffect(() => {
    if (!selected) return;
    treeRef.current?.querySelector(`[data-file-path="${CSS.escape(selected.relative_path)}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [selected?.id, files, collapsed]);

  const navigate = (directory: string) => {
    setFolder(directory); onSelect(null); setPane('files'); setSearch('');
    setCollapsed(current => new Set([...current].filter(value => directory !== value && !directory.startsWith(`${value}/`))));
  };
  const choose = (file: Attachment) => { onSelect(file); setPane('preview'); download.reset(); remove.reset(); };
  const onTreeKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const active = document.activeElement as HTMLElement;
    const items = Array.from(treeRef.current?.querySelectorAll<HTMLElement>('[role="treeitem"]') ?? []);
    const index = items.indexOf(active);
    if (index < 0) return;
    let next: HTMLElement | undefined;
    switch (event.key) {
      case 'ArrowDown': next = items[Math.min(items.length - 1, index + 1)]; break;
      case 'ArrowUp': next = items[Math.max(0, index - 1)]; break;
      case 'Home': next = items[0]; break;
      case 'End': next = items[items.length - 1]; break;
      case 'ArrowRight':
        if (active.getAttribute('aria-expanded') === 'false') active.click();
        else if (active.hasAttribute('aria-expanded')) next = items[index + 1];
        break;
      case 'ArrowLeft':
        if (active.getAttribute('aria-expanded') === 'true') active.click();
        else next = items.find(item => item.dataset.treePath === active.dataset.parent);
        break;
      default: return;
    }
    event.preventDefault(); next?.focus();
  };
  const nodes = (items: Map<string, FileNode>, depth = 0, parent = ''): ReactNode => sortedNodes(items).map(node => {
    const open = !!search || !collapsed.has(node.path);
    const active = selectedFile ? selectedFile.id === node.file?.id : folder === node.path;
    return <div key={node.path} role="none">
      <button type="button" role="treeitem" aria-selected={active} aria-expanded={node.file ? undefined : open} aria-level={depth + 1}
        data-file-path={node.file ? node.path : undefined} data-tree-path={node.path} data-parent={parent} title={node.path}
        tabIndex={focusPath === node.path ? 0 : -1}
        onFocus={() => setFocused(node.path)}
        onClick={() => {
          if (node.file) choose(node.file);
          else {
            navigate(node.path);
            if (open) setCollapsed(current => new Set(current).add(node.path));
          }
        }}
        className={cn('flex w-full min-w-0 items-center gap-2 rounded-md py-2 pr-2 text-left text-xs outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring', active && 'bg-muted font-medium')}
        style={{ paddingLeft: `${depth * 14 + 8}px` }}
      >
        {!node.file && (open ? <ChevronDown className="size-3 shrink-0" /> : <ChevronRight className="size-3 shrink-0" />)}
        {node.file ? <FileIcon mime={node.file.content_type} /> : open ? <FolderOpen className="size-4 shrink-0 text-muted-foreground" /> : <Folder className="size-4 shrink-0 text-muted-foreground" />}
        <span className="min-w-0 flex-1 truncate">{node.name}</span>
        {node.file && <span className="shrink-0 text-[10px] text-muted-foreground">{node.file.size_bytes.toLocaleString()} B</span>}
      </button>
      {!node.file && open && <div role="group">{nodes(node.children, depth + 1, node.path)}</div>}
    </div>;
  });

  return <>
    <nav aria-label={t('attachment.path')} className="flex h-11 shrink-0 items-center gap-1 overflow-x-auto border-b bg-muted/20 px-3 text-xs" data-file-breadcrumbs>
      <button type="button" onClick={() => navigate('')} className="shrink-0 rounded px-2 py-1.5 font-mono hover:bg-muted" aria-label={t('attachment.root')}>/</button>
      {parts.map((part, index) => <span className="flex shrink-0 items-center gap-1" key={parts.slice(0, index + 1).join('/')}>
        <ChevronRight className="size-3 text-muted-foreground" />
        {selectedFile && index === parts.length - 1 ? <span className="px-2 font-medium" aria-current="page">{part}</span>
          : <button type="button" className="rounded px-2 py-1.5 hover:bg-muted" onClick={() => navigate(parts.slice(0, index + 1).join('/'))}>{part}</button>}
      </span>)}
    </nav>
    <div className="flex shrink-0 gap-1 border-b p-2 md:hidden" role="tablist" aria-label={t('attachment.manager')}>
      <button type="button" role="tab" aria-selected={pane === 'files'} aria-controls="file-browser-tree" id="file-tree-tab" className={cn('flex-1 rounded-md px-3 py-1.5 text-xs', pane === 'files' && 'bg-muted font-medium')} onClick={() => setPane('files')}>{t('attachment.browse')}</button>
      <button type="button" role="tab" aria-selected={pane === 'preview'} aria-controls="file-browser-preview" id="file-preview-tab" className={cn('flex-1 rounded-md px-3 py-1.5 text-xs', pane === 'preview' && 'bg-muted font-medium')} onClick={() => setPane('preview')}>{t('attachment.preview')}</button>
    </div>
    <div className="flex min-h-0 flex-1">
      <div id="file-browser-tree" className={cn('flex min-h-0 min-w-0 flex-1 flex-col md:w-64 md:flex-none md:border-r lg:w-72', pane === 'preview' && 'hidden md:flex')}>
        <div className="relative m-3 shrink-0">
          <Search className="absolute left-2.5 top-2.5 size-3.5 text-muted-foreground" />
          <Input className="h-9 pl-8 text-xs" value={search} onChange={event => setSearch(event.target.value)} aria-label={t('attachment.search')} placeholder={t('attachment.search')} />
        </div>
        <div ref={treeRef} className="min-h-0 flex-1 overflow-y-auto px-2 pb-3" role="tree" aria-label={t('attachment.files')} onKeyDown={onTreeKeyDown}>
          {nodes(roots)}
          {pending && <p className="p-3 text-xs text-muted-foreground">{t('attachment.loading')}</p>}
          {!pending && !error && !visible.length && <p className="p-3 text-xs text-muted-foreground">{t(search ? 'attachment.noMatches' : 'attachment.empty')}</p>}
          {!!error && <div className="p-3"><ErrorState error={error} /></div>}
        </div>
      </div>
      <section id="file-browser-preview" aria-label={t('attachment.preview')} className={cn('flex min-h-0 min-w-0 flex-1 flex-col', pane === 'files' && 'hidden md:flex')}>
        {selectedFile ? <>
          <div className="flex shrink-0 flex-wrap items-center gap-3 border-b p-4">
            <FileIcon mime={selectedFile.content_type} className="size-5 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1"><p className="truncate text-sm font-medium" title={selectedFile.relative_path}>{selectedFile.filename}</p><p className="mt-1 truncate text-xs text-muted-foreground">{selectedFile.size_bytes.toLocaleString()} B{selectedFile.created_by_agent_id && ` · @${selectedFile.created_by_agent_id}`}</p></div>
            <div className="flex w-full flex-wrap items-center gap-1 sm:w-auto">
              <Button variant="outline" size="sm" disabled={download.isPending} onClick={() => download.mutate(selectedFile)}><Download />{t('attachment.download')}</Button>
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => onAttach(selectedFile)}><Plus />{t('attachment.attach')}</Button>
              {!referenced.has(selectedFile.id) && <Confirm title={t('attachment.delete')} description={selectedFile.relative_path} onConfirm={() => remove.mutate(selectedFile)}><Button size="icon" variant="ghost" className="size-8 text-destructive" aria-label={t('attachment.delete')} disabled={remove.isPending}><Trash2 /></Button></Confirm>}
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-auto bg-muted/10"><FilePreview key={selectedFile.id} file={selectedFile} /></div>
          {!referenced.has(selectedFile.id) && <p className="shrink-0 border-t px-4 py-2 text-xs text-muted-foreground">{t('attachment.unreferenced')}</p>}
          {[download.error, remove.error].filter(Boolean).map((value, index) => <div className="shrink-0 px-4 py-2" key={index}><ErrorState error={value} /></div>)}
        </> : <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center text-muted-foreground"><FolderOpen className="size-10 opacity-40" /><p className="text-sm">{t('attachment.select')}</p></div>}
      </section>
    </div>
  </>;
}
