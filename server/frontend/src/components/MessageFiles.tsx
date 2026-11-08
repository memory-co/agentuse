import { useEffect, useState } from 'react';
import { FileText, Image as ImageIcon, LoaderCircle, X } from 'lucide-react';
import { useT } from '@/lib/i18n';
import type { Attachment } from '@/lib/types';
import type { DraftFile } from '@/hooks/use-message-files';

export function FileIcon({ mime, className = 'size-4 shrink-0' }: { mime: string; className?: string }) {
  const Icon = mime.startsWith('image/') ? ImageIcon : FileText;
  return <Icon className={className} />;
}
function DraftImage({ file }: { file: File }) {
  const [url, setUrl] = useState('');
  useEffect(() => { const value = URL.createObjectURL(file); setUrl(value); return () => URL.revokeObjectURL(value); }, [file]);
  return <img src={url} alt="" className="size-10 rounded-md object-cover" />;
}
export function DraftFiles({ files, busy, onRemove }: { files: DraftFile[]; busy?: boolean; onRemove: (key: string) => void }) {
  const t = useT();
  if (!files.length) return null;
  return <div className="mb-2 flex flex-wrap gap-2 px-2" aria-label={t('attachment.thisMessage')}>
    {files.map(item => <div key={item.key} className="flex max-w-full items-center gap-2 rounded-lg border bg-muted/40 p-2" data-draft-file={item.key}>
      {item.file?.type.startsWith('image/') ? <DraftImage file={item.file} /> : <FileIcon mime={item.attachment?.content_type || item.file?.type || ''} />}
      <div className="min-w-0 text-xs"><p className="max-w-48 truncate font-medium">{item.attachment?.filename || item.file?.name}</p><p className="text-muted-foreground">{item.progress !== undefined ? `${item.progress}%` : t('attachment.thisMessage')}</p></div>
      {busy && item.progress !== undefined && item.progress < 100 && <LoaderCircle className="size-3 animate-spin" />}
      <button type="button" disabled={busy} aria-label={`${t('attachment.remove')} ${item.attachment?.filename || item.file?.name}`} onClick={() => onRemove(item.key)}><X className="size-3.5" /></button>
    </div>)}
  </div>;
}
export function MessageFiles({ files, onOpen }: { files: Attachment[]; onOpen: (file: Attachment) => void }) {
  const t = useT();
  if (!files.length) return null;
  return <div className="flex min-w-0 flex-wrap gap-2 pt-1" aria-label={t('attachment.thisMessage')} data-message-files="true">
    {files.map(file => <button key={file.id} type="button" onClick={() => onOpen(file)} title={file.relative_path}
      className="flex min-w-0 max-w-full items-center gap-2 rounded-lg border bg-background/70 px-3 py-2 text-left text-xs hover:bg-background">
      <FileIcon mime={file.content_type} /><span className="min-w-0"><span className="block truncate font-medium">{file.filename}</span><span className="block truncate text-muted-foreground">{file.relative_path} · {file.size_bytes.toLocaleString()} B</span></span>
    </button>)}
  </div>;
}
