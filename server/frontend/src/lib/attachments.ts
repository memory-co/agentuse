import { actAsHeaders } from '@/lib/actas';
import { ApiError, api, downloadFile } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Attachment } from '@/lib/types';

export async function listAttachments(conversation: string): Promise<Attachment[]> {
  const out: Attachment[] = [];
  let cursor: string | null = null;
  do {
    const page: { attachments: Attachment[]; next_cursor: string | null } = await api(`/api/conversations/${conversation}/attachments${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`);
    out.push(...page.attachments); cursor = page.next_cursor;
  } while (cursor);
  return out;
}
export function uploadAttachment(conversation: string, file: File, progress: (percent: number) => void): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const path = `/api/conversations/${conversation}/attachments`;
    const xhr = new XMLHttpRequest();
    xhr.open('POST', path);
    for (const [key, value] of Object.entries(actAsHeaders(path))) xhr.setRequestHeader(key, value);
    xhr.upload.onprogress = e => { if (e.lengthComputable) progress(Math.round(e.loaded * 100 / e.total)); };
    xhr.onerror = () => reject(new Error('Upload interrupted; check the attachment list before retrying.'));
    xhr.onabort = () => reject(new Error('Upload cancelled'));
    xhr.onload = () => {
      let data: any;
      try { data = JSON.parse(xhr.responseText); } catch { reject(new Error(xhr.statusText || 'Upload failed')); return; }
      if (xhr.status === 401) queryClient.setQueryData(['me'], null);
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else reject(new ApiError(xhr.status, data.error || xhr.statusText));
    };
    const body = new FormData(); body.append('file', file); xhr.send(body);
  });
}
export function downloadAttachment(a: Attachment) {
  return downloadFile(a.download_path, a.filename);
}
