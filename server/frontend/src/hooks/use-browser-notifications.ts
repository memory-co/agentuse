import { useEffect } from 'react';
import { useActAs } from '@/lib/actas';
import { createBrowserQueryRefresh, type BrowserNotice } from '@/services/browser-notifications';
import { verifyBrowserSession } from '@/services/session';

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
      const stillCurrent = () => !stopped && socket === current;
      const refresh = createBrowserQueryRefresh(stillCurrent);
      socket = current;
      current.onmessage = event => {
        if (stopped || socket !== current) return;
        try {
          const notice = JSON.parse(event.data) as BrowserNotice;
          if (notice.type === 'ready') {
            delay = 1000;
            refresh([{ resource: 'reset' }]);
          } else if (notice.type === 'changed' && Array.isArray(notice.changes)) {
            refresh(notice.changes);
          }
        } catch { /* Ignore malformed notifications; reconnect reconciles state. */ }
      };
      current.onclose = () => {
        if (stopped || socket !== current) return;
        // Browser WebSocket APIs hide failed handshake status codes. Verify the
        // cookie through HTTP so expired login returns to the login page.
        verifyBrowserSession(stillCurrent);
        retry = setTimeout(connect, delay + Math.random() * 250);
        delay = Math.min(delay * 2, 30000);
      };
    };
    connect();
    return () => { stopped = true; if (retry) clearTimeout(retry); socket?.close(); };
  }, [userID, actingAs]);
}
