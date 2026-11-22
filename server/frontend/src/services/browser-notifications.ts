import type { QueryFilters } from '@tanstack/react-query';
import { queryClient } from '@/lib/query';
import { conversationChangeQueries } from '@/services/conversations';
import { runChangeQueries } from '@/services/runs';

type Change = { resource: string; id?: string; parent_conversation_id?: string; detail_only?: boolean };

export type BrowserNotice = { type: string; changes?: Change[] };

function queriesForChange(change: Change): QueryFilters[] {
  const { resource, id, parent_conversation_id: parent, detail_only: detailOnly } = change;
  switch (resource) {
    case 'conversations': {
      const runs = id ? [runChangeQueries(id, !!detailOnly)] : [];
      return detailOnly ? runs : [...conversationChangeQueries(id, parent), ...runs];
    }
    case 'attachments': return id ? [{ queryKey: ['attachments', id], exact: true }] : [];
    case 'agents':
      return id ? [{ queryKey: ['agents'], exact: true }, { queryKey: ['agent-versions', id], exact: true }]
        : [{ queryKey: ['agents'], exact: true }];
    case 'skills':
      return id ? [{ queryKey: ['skills'], exact: true }, { queryKey: ['skill-versions', id], exact: true }]
        : [{ queryKey: ['skills'], exact: true }];
    case 'devices':
    case 'users': return [{ queryKey: [resource], exact: true }];
    case 'upgrades': return [{ queryKey: ['upgrades'] }];
    default: return [];
  }
}

export function createBrowserQueryRefresh(stillCurrent: () => boolean) {
  const waiting = new Set<string>();
  const cache = queryClient.getQueryCache();
  return (changes: Change[]) => {
    const filters = changes.some(change => change.resource === 'reset') ? [{}] : changes.flatMap(queriesForChange);
    const queries = new Set(filters.flatMap(filter => cache.findAll(filter)));
    for (const query of queries) {
      if (waiting.has(query.queryHash)) continue;
      waiting.add(query.queryHash);
      const inFlight = query.state.fetchStatus === 'fetching' ? query.promise : undefined;
      // Keep one trailing refresh for changes that arrive during a read.
      void Promise.resolve(inFlight).catch(() => undefined).then(() => {
        waiting.delete(query.queryHash);
        if (stillCurrent()) void queryClient.invalidateQueries({ queryKey: query.queryKey, exact: true }, { cancelRefetch: false });
      });
    }
  };
}
