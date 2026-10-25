import { useSyncExternalStore } from 'react';
import { queryClient } from '@/lib/query';

// 管理员「代为某个用户」:选中后除了 /api/me、/api/users 和登录登出，所有请求都带 X-Agentuse-User(见 docs/v2/designs/embedding.md)。
// 记在 localStorage 里，刷新不丢;空串表示管理员自己。
const KEY = 'agentuse.actAs';
const listeners = new Set<() => void>();
let current = (() => { try { return localStorage.getItem(KEY) || ''; } catch { return ''; } })();

export const getActAs = () => current;
export function setActAs(id: string) {
  if (id === current) return;
  current = id;
  try { if (id) localStorage.setItem(KEY, id); else localStorage.removeItem(KEY); } catch { /* 无痕模式 */ }
  listeners.forEach(l => l());
  // 换了身份，除了 me / users 之外的数据都要重新拿。
  queryClient.removeQueries({ predicate: q => q.queryKey[0] !== 'me' && q.queryKey[0] !== 'users' });
}
export const useActAs = () => useSyncExternalStore(l => { listeners.add(l); return () => listeners.delete(l); }, getActAs);

const SELF = /^\/api\/(me|users|login|logout)(\/|$)/;
export const actAsHeaders = (path: string): Record<string, string> => (current && !SELF.test(path) ? { 'X-Agentuse-User': current } : {});
