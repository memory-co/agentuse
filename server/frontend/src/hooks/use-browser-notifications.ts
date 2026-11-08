import { useEffect } from 'react';
import { queryClient } from '@/lib/query';
import { useActAs } from '@/lib/actas';
import { api, ApiError } from '@/lib/api';

type Change = { resource: string; id?: string; work_id?: string };

function refresh(changes: Change[], stillCurrent: () => boolean) {
  const keys = new Map<string, readonly unknown[]>();
  const add = (key: readonly unknown[]) => { keys.set(JSON.stringify(key), key); };
  const reset = changes.some(change => change.resource === 'reset');
  const workChanged = changes.some(change => change.resource === 'works');
  for (const change of changes) {
    switch (change.resource) {
      case 'sessions':
        add(['sessions']);
        if (change.id) add(['sessions', change.id]);
        if (change.work_id) {
          add(['sessions', 'work', change.work_id]);
          add(['works']);
          add(['works', change.work_id]);
        }
        break;
      case 'attachments': if (change.id) add(['attachments', change.id]); break;
      case 'agents':
        add(['agents']);
        if (change.id) add(['agent-versions', change.id]);
        break;
      case 'skills':
        add(['skills']);
        if (change.id) add(['skill-versions', change.id]);
        break;
      case 'daemons': add(['daemons']); break;
      case 'users': add(['users']); break;
    }
  }
  const predicate = (query: { queryKey: readonly unknown[] }) => reset
    || (workChanged && ['works', 'sessions'].includes(String(query.queryKey[0])))
    || keys.has(JSON.stringify(query.queryKey));
  // Cancel an older in-flight snapshot, including a query with no cached data,
  // so its completion cannot hide a notification that arrived during that GET.
  void queryClient.cancelQueries({ predicate }).then(() => {
    if (stillCurrent()) void queryClient.invalidateQueries({ predicate });
  });
}

// One notification socket per authenticated application. HTTP remains the data
// source; ready always reconciles active queries, including after a disconnect.
export function useBrowserNotifications(userID: string) {
  const actingAs = useActAs();
  useEffect(() => {
    let stopped = false;
    let socket: WebSocket | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let delay = 1000;
    const connect = () => {
      if (stopped) return;
      const url = new URL('/api/browser/ws', location.href);
      url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      if (actingAs) url.searchParams.set('as', actingAs);
      const current = new WebSocket(url);
      socket = current;
      current.onmessage = event => {
        if (stopped || socket !== current) return;
        try {
          const notice = JSON.parse(event.data) as { type: string; changes?: Change[] };
          if (notice.type === 'ready') {
            delay = 1000;
            refresh([{ resource: 'reset' }], () => !stopped && socket === current);
          } else if (notice.type === 'changed' && Array.isArray(notice.changes)) {
            refresh(notice.changes, () => !stopped && socket === current);
          }
        } catch { /* Ignore malformed notifications; reconnect reconciles state. */ }
      };
      current.onclose = () => {
        if (stopped || socket !== current) return;
        // Browser WebSocket APIs hide failed handshake status codes. Verify the
        // cookie through HTTP so expired login returns to the login page.
        void api('/api/me').catch(error => {
          if (!stopped && error instanceof ApiError && error.status === 401) queryClient.setQueryData(['me'], null);
        });
        retry = setTimeout(connect, delay + Math.random() * 250);
        delay = Math.min(delay * 2, 30000);
      };
    };
    connect();
    return () => { stopped = true; if (retry) clearTimeout(retry); socket?.close(); };
  }, [userID, actingAs]);
}
