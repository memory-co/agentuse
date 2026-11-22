import { queryClient } from '@/lib/query';
import { actAsHeaders } from '@/lib/actas';

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

// 登录态在 cookie 里(POST /api/login 设置),这里只负责 JSON、错误和管理员的代为用户头。401 时把 me 清掉,Gate 回到登录页。
export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown; headers?: Record<string,string>; signal?: AbortSignal } = {}): Promise<T> {
  const r = await fetch(path, {
    method: opts.method || 'GET',
    signal: opts.signal,
    headers: { ...actAsHeaders(path),...opts.headers, ...(opts.body !== undefined && !(opts.body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}) },
    body: opts.body instanceof FormData ? opts.body : opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (r.status === 204) return null as T;
  const data = await r.json().catch(error => {
    if (opts.signal?.aborted) throw error;
    return {};
  });
  if (r.status === 401 && path !== '/api/login' && path !== '/api/me') queryClient.setQueryData(['me'], null);
  if (!r.ok) throw new ApiError(r.status, data.error || r.statusText);
  return data as T;
}

export async function downloadFile(path: string, filename: string) {
  const res = await fetch(path, { headers: actAsHeaders(path) });
  if (!res.ok) {
    if (res.status === 401) queryClient.setQueryData(['me'], null);
    const data = await res.json().catch(() => ({}));
    throw new ApiError(res.status, data.error || res.statusText);
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
