import { useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Download, Paperclip, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Confirm, ErrorState } from '@/components/Shared';
import { api } from '@/lib/api';
import { downloadAttachment, listAttachments, uploadAttachment } from '@/lib/attachments';
import { queryClient } from '@/lib/query';
import { useT } from '@/lib/i18n';
import type { Attachment } from '@/lib/types';

export function Attachments({ session, running }: { session: string; running: boolean }) {
  const t = useT(); const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState('');
  const key = ['attachments', session];
  const q = useQuery({ queryKey: key, queryFn: () => listAttachments(session), refetchInterval: 3000 });
  const refresh = () => queryClient.invalidateQueries({ queryKey: key });
  const upload = useMutation({
    mutationFn: async (files: File[]) => {
      try { for (const file of files) { setProgress(file.name); await uploadAttachment(session, file, n => setProgress(`${file.name} ${n}%`)); } }
      finally { setProgress(''); void refresh(); }
    },
  });
  const download = useMutation({ mutationFn: downloadAttachment });
  const del = useMutation({ mutationFn: (a: Attachment) => api(`/api/sessions/${session}/attachments/${a.id}`, {method: 'DELETE'}), onSuccess: refresh });
  return <section className="grid min-w-0 grid-cols-1 gap-2 rounded-lg border p-3" aria-label={t('attachment.title')}>
    <div className="flex items-center gap-2 text-sm font-medium"><Paperclip className="size-4" />{t('attachment.title')}
      <Button type="button" variant="outline" size="sm" className="ml-auto" disabled={upload.isPending} onClick={() => input.current?.click()}>{t('attachment.upload')}</Button>
      <input ref={input} type="file" multiple className="hidden" aria-label={t('attachment.upload')} onChange={e => { const files = Array.from(e.target.files ?? []); e.target.value = ''; if (files.length) upload.mutate(files); }} />
    </div>
    {running && <p className="text-xs text-muted-foreground">{t('attachment.nextTurn')}</p>}
    {progress && <p className="break-all text-xs" role="status">{progress}</p>}
    {q.isPending && <p className="text-xs text-muted-foreground">{t('attachment.loading')}</p>}
    {q.data?.map(a => <div key={a.id} className="flex items-center gap-2 text-xs">
      <div className="min-w-0 flex-1"><p className="break-all">{a.filename}</p><p className="break-words text-muted-foreground">{a.size_bytes.toLocaleString()} bytes · {a.created_by} · {new Date(a.created_at * 1000).toLocaleString()}</p></div>
      <Button size="icon" variant="ghost" className="size-7" aria-label={`${t('attachment.download')} ${a.filename}`} disabled={download.isPending} onClick={() => download.mutate(a)}><Download /></Button>
      <Confirm title={t('attachment.delete')} description={a.filename} action={t('common.delete')} onConfirm={() => del.mutate(a)}>
        <Button size="icon" variant="ghost" className="size-7 text-destructive" aria-label={`${t('attachment.delete')} ${a.filename}`} disabled={del.isPending}><Trash2 /></Button>
      </Confirm>
    </div>)}
    {q.data?.length === 0 && <p className="text-xs text-muted-foreground">{t('attachment.empty')}</p>}
    {[q.error, upload.error, download.error, del.error].filter(Boolean).map((error, i) => <ErrorState key={i} error={error} />)}
  </section>;
}
