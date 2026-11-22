import type { QueryFilters } from '@tanstack/react-query';
import { queryClient } from '@/lib/query';
import { agentChangeQueries } from '@/services/agents';
import { attachmentListQuery } from '@/services/attachments';
import { conversationChangeQueries } from '@/services/conversations';
import { deviceChangeQuery } from '@/services/devices';
import { runChangeQueries } from '@/services/runs';
import { skillChangeQueries } from '@/services/skills';
import { upgradeChangeQuery } from '@/services/upgrades';
import { userChangeQuery } from '@/services/users';

type Change = { resource: string; id?: string; parent_conversation_id?: string; detail_only?: boolean };

export type BrowserNotice = { type: string; changes?: Change[] };

function queriesForChange(change: Change): QueryFilters[] {
  const { resource, id, parent_conversation_id: parent, detail_only: detailOnly } = change;
  switch (resource) {
    case 'conversations': {
      const runs = id ? [runChangeQueries(id, !!detailOnly)] : [];
      return detailOnly ? runs : [...conversationChangeQueries(id, parent), ...runs];
    }
    case 'attachments': return id ? [attachmentListQuery(id)] : [];
    case 'agents': return agentChangeQueries(id);
    case 'skills': return skillChangeQueries(id);
    case 'devices': return [deviceChangeQuery];
    case 'users': return [userChangeQuery];
    case 'upgrades': return [upgradeChangeQuery];
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
