import { useMutation, useQuery } from '@tanstack/react-query';
import { actAsHeaders } from '@/lib/actas';
import { ApiError, api, downloadFile } from '@/lib/api';
import { clearCurrentUser, queryClient } from '@/lib/query';
import type { Attachment } from '@/lib/types';

const attachmentPath = (conversationId: string) => `/api/conversations/${encodeURIComponent(conversationId)}/attachments`;
const previewKey = (fileId: string) => ['attachment-preview', fileId] as const;

export function attachmentListQuery(conversationId: string) {
  return { queryKey: ['attachments', conversationId] as const, exact: true };
}

export function invalidateAttachmentList(conversationId: string) {
  void queryClient.invalidateQueries(attachmentListQuery(conversationId));
}

export function useAttachments(conversationId: string, enabled: boolean) {
  return useQuery({
    queryKey: attachmentListQuery(conversationId).queryKey,
    queryFn: ({ signal }) => listAttachments(conversationId, signal),
    enabled,
  });
}

async function listAttachments(conversationId: string, signal: AbortSignal): Promise<Attachment[]> {
  const out: Attachment[] = [];
  let cursor: string | null = null;
  do {
    const page: { attachments: Attachment[]; next_cursor: string | null } = await api(`${attachmentPath(conversationId)}${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, { signal });
    out.push(...page.attachments);
    cursor = page.next_cursor;
  } while (cursor);
  return out;
}

export function attachmentPreviewKind(file: Attachment) {
  const image = /^image\/(png|jpeg|gif|webp|avif)$/.test(file.content_type);
  const text = file.content_type.startsWith('text/') || /application\/(json|xml)/.test(file.content_type);
  if (file.size_bytes > (image ? 16 * 1024 * 1024 : 512 * 1024)) return 'tooLarge';
  if (image) return 'image';
  return text ? 'text' : 'noPreview';
}

export function useAttachmentPreview(file: Attachment) {
  const kind = attachmentPreviewKind(file);
  const query = useQuery({
    queryKey: previewKey(file.id),
    enabled: kind === 'image' || kind === 'text',
    staleTime: Infinity,
    queryFn: async ({ signal }) => {
      const response = await fetch(file.download_path, { headers: actAsHeaders(file.download_path), signal });
      if (!response.ok) throw new ApiError(response.status, (await response.json().catch(() => ({}))).error || response.statusText);
      return kind === 'image' ? response.blob() : response.text();
    },
  });
  return { kind, query };
}

export function uploadAttachment(conversationId: string, file: File, progress: (percent: number) => void): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const path = attachmentPath(conversationId);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', path);
    for (const [key, value] of Object.entries(actAsHeaders(path))) xhr.setRequestHeader(key, value);
    xhr.upload.onprogress = e => { if (e.lengthComputable) progress(Math.round(e.loaded * 100 / e.total)); };
    xhr.onerror = () => reject(new Error('Upload interrupted; check the attachment list before retrying.'));
    xhr.onabort = () => reject(new Error('Upload cancelled'));
    xhr.onload = () => {
      let data: any;
      try { data = JSON.parse(xhr.responseText); } catch { reject(new Error(xhr.statusText || 'Upload failed')); return; }
      if (xhr.status === 401) clearCurrentUser();
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else reject(new ApiError(xhr.status, data.error || xhr.statusText));
    };
    const body = new FormData(); body.append('file', file); xhr.send(body);
  });
}

export function useDownloadAttachment() {
  return useMutation({ mutationFn: (file: Attachment) => downloadFile(file.download_path, file.filename) });
}

export function useDeleteAttachment(conversationId: string) {
  return useMutation({
    mutationFn: (file: Attachment) => api(`${attachmentPath(conversationId)}/${encodeURIComponent(file.id)}`, { method: 'DELETE' }),
    onSuccess: (_, file) => {
      queryClient.removeQueries({ queryKey: previewKey(file.id), exact: true });
      invalidateAttachmentList(conversationId);
    },
  });
}
