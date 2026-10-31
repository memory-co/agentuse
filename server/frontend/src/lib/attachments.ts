import { actAsHeaders } from '@/lib/actas';
import { ApiError, api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Attachment } from '@/lib/types';

export async function listAttachments(session: string): Promise<Attachment[]> {
  const out: Attachment[] = [];
  let cursor: string | null = null;
  do {
    const page: { attachments: Attachment[]; next_cursor: string | null } = await api(`/api/sessions/${session}/attachments${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`);
    out.push(...page.attachments); cursor = page.next_cursor;
  } while (cursor);
  return out;
}
export function uploadAttachment(session: string, file: File, progress: (percent: number) => void): Promise<Attachment> {
  return new Promise((resolve, reject) => {
    const path = `/api/sessions/${session}/attachments`;
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
export async function downloadAttachment(a: Attachment) {
  const res = await fetch(a.download_path, { headers: actAsHeaders(a.download_path) });
  if (!res.ok) {
    if (res.status === 401) queryClient.setQueryData(['me'], null);
    const data = await res.json().catch(() => ({})); throw new ApiError(res.status, data.error || res.statusText);
  }
  const blob = await res.blob(); const url = URL.createObjectURL(blob);
  const link = document.createElement('a'); link.href = url; link.download = a.filename; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
