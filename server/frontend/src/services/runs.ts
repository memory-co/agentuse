import { useMutation, useQuery, type QueryFilters } from '@tanstack/react-query';
import { api, downloadFile } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { RunDetail, RunEventPage } from '@/lib/types';
import { invalidateConversation, type ConversationScope } from '@/services/conversations';

const runKey = (conversationId: string) => ['runs', conversationId] as const;
const keys = {
  detail: (conversationId: string, runId: string) => [...runKey(conversationId), 'detail', runId] as const,
  events: (conversationId: string) => [...runKey(conversationId), 'events'] as const,
};

const runPath = (conversationId: string, runId: string) =>
  `/api/conversations/${encodeURIComponent(conversationId)}/runs/${encodeURIComponent(runId)}`;

export function runChangeQueries(conversationId: string, detailOnly: boolean): QueryFilters {
  return { queryKey: detailOnly ? keys.events(conversationId) : runKey(conversationId) };
}

export function useRunDetails(conversationId: string, runId: string) {
  return useQuery({
    queryKey: keys.detail(conversationId, runId),
    queryFn: ({ signal }) => api<RunDetail>(`${runPath(conversationId, runId)}?view=metadata`, { signal }),
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
}

export function useRunEvents(conversationId: string, runId: string, cursor: string, enabled: boolean) {
  const params = new URLSearchParams({ limit: '20' });
  if (cursor) params.set('cursor', cursor);
  return useQuery({
    queryKey: [...keys.events(conversationId), runId, cursor],
    queryFn: ({ signal }) => api<RunEventPage>(`${runPath(conversationId, runId)}/events?${params}`, { signal }),
    enabled,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
}

function invalidateRun(conversation: ConversationScope, runId: string) {
  void queryClient.invalidateQueries({ queryKey: keys.detail(conversation.id, runId), exact: true });
  invalidateConversation(conversation);
}

export function useCancelRun(conversation: ConversationScope, runId: string) {
  return useMutation({
    mutationFn: () => api(`${runPath(conversation.id, runId)}/cancel`, { method: 'POST' }),
    onSuccess: () => invalidateRun(conversation, runId),
  });
}

export function useAnswerRunRequest(conversation: ConversationScope, runId: string, requestId: string) {
  return useMutation({
    mutationFn: (answer: unknown) => api(`${runPath(conversation.id, runId)}/requests/${encodeURIComponent(requestId)}/reply`, { method: 'POST', body: { answer } }),
    onSuccess: () => invalidateRun(conversation, runId),
  });
}

export function useDownloadRunEvent(conversationId: string, runId: string) {
  return useMutation({
    mutationFn: (eventId: string) => downloadFile(`${runPath(conversationId, runId)}/events/${encodeURIComponent(eventId)}`, `run-event-${eventId}.json`),
  });
}
