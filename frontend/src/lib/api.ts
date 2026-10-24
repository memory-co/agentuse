import { queryClient } from '@/lib/query';

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

// 登录态在 cookie 里(POST /api/login 设置),这里只负责 JSON 和错误。401 时把 me 清掉,Gate 回到登录页。
export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown } = {}): Promise<T> {
  const r = await fetch(path, {
    method: opts.method || 'GET',
    headers: opts.body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (r.status === 204) return null as T;
  const data = await r.json().catch(() => ({}));
  if (r.status === 401 && path !== '/api/login' && path !== '/api/me') queryClient.setQueryData(['me'], null);
  if (!r.ok) throw new ApiError(r.status, data.error || r.statusText);
  return data as T;
}
