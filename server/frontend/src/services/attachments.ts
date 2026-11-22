import { useMutation, useQuery } from '@tanstack/react-query';
import { defaultPathSerializer } from 'openapi-fetch';
import { ApiError, api, apiData, downloadFile, fetchFile, multipartBody } from '@/api/client';
import type { paths } from '@/api/schema';
import { actAsHeaders } from '@/lib/actas';
import { clearCurrentUser, queryClient } from '@/lib/query';
import type { Attachment } from '@/lib/types';

const attachmentsPath = '/api/conversations/{id}/attachments';
type Upload = paths[typeof attachmentsPath]['post'];
type AttachmentPage = paths[typeof attachmentsPath]['get']['responses'][200]['content']['application/json'];
type UploadedAttachment = Upload['responses'][201]['content']['application/json'];
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
    const page: AttachmentPage = await apiData(api.GET(attachmentsPath, {
      params: { path: { id: conversationId }, query: { cursor: cursor || undefined } }, signal,
    }));
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
      const response = await fetchFile(file.download_path, signal);
      return kind === 'image' ? response.blob() : response.text();
    },
  });
  return { kind, query };
}

export function uploadAttachment(conversationId: string, file: File, progress: (percent: number) => void): Promise<UploadedAttachment> {
  return new Promise((resolve, reject) => {
    const params: Upload['parameters']['path'] = { id: conversationId };
    const path = defaultPathSerializer(attachmentsPath, params);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', path);
    for (const [key, value] of Object.entries(actAsHeaders(path))) xhr.setRequestHeader(key, value);
    xhr.upload.onprogress = e => { if (e.lengthComputable) progress(Math.round(e.loaded * 100 / e.total)); };
    xhr.onerror = () => reject(new Error('Upload interrupted; check the attachment list before retrying.'));
    xhr.onabort = () => reject(new Error('Upload cancelled'));
    xhr.onload = () => {
      let data: unknown;
      try { data = JSON.parse(xhr.responseText); } catch { reject(new Error(xhr.statusText || 'Upload failed')); return; }
      if (xhr.status === 401) clearCurrentUser();
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data as UploadedAttachment);
      } else {
        const error = data && typeof data === 'object' && 'error' in data ? data.error : undefined;
        reject(new ApiError(xhr.status, typeof error === 'string' && error ? error : xhr.statusText));
      }
    };
    const fields: Upload['requestBody']['content']['multipart/form-data'] = { file };
    xhr.send(multipartBody(fields));
  });
}

export function useDownloadAttachment() {
  return useMutation({ mutationFn: (file: Attachment) => downloadFile(file.download_path, file.filename) });
}

export function useDeleteAttachment(conversationId: string) {
  return useMutation({
    mutationFn: (file: Attachment) => apiData(api.DELETE('/api/conversations/{id}/attachments/{attachment_id}', {
      params: { path: { id: conversationId, attachment_id: file.id } },
    })),
    onSuccess: (_, file) => {
      queryClient.removeQueries({ queryKey: previewKey(file.id), exact: true });
      invalidateAttachmentList(conversationId);
    },
  });
}
