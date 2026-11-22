import { useMutation, useQuery, type QueryFilters } from '@tanstack/react-query';
import { api, apiData, saveDownload } from '@/api/client';
import { queryClient } from '@/lib/query';
import { invalidateConversation, type ConversationScope } from '@/services/conversations';

const runKey = (conversationId: string) => ['runs', conversationId] as const;
const keys = {
  detail: (conversationId: string, runId: string) => [...runKey(conversationId), 'detail', runId] as const,
  events: (conversationId: string) => [...runKey(conversationId), 'events'] as const,
};

export function runChangeQueries(conversationId: string, detailOnly: boolean): QueryFilters {
  return { queryKey: detailOnly ? keys.events(conversationId) : runKey(conversationId) };
}

export function useRunDetails(conversationId: string, runId: string) {
  return useQuery({
    queryKey: keys.detail(conversationId, runId),
    queryFn: ({ signal }) => apiData(api.GET('/api/conversations/{id}/runs/{run_id}', {
      params: { path: { id: conversationId, run_id: runId }, query: { view: 'metadata' } }, signal,
    })),
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
}

export function useRunEvents(conversationId: string, runId: string, cursor: string, enabled: boolean) {
  return useQuery({
    queryKey: [...keys.events(conversationId), runId, cursor],
    queryFn: ({ signal }) => apiData(api.GET('/api/conversations/{id}/runs/{run_id}/events', {
      params: { path: { id: conversationId, run_id: runId }, query: { limit: 20, cursor: cursor || undefined } }, signal,
    })),
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
    mutationFn: () => apiData(api.POST('/api/conversations/{id}/runs/{run_id}/cancel', {
      params: { path: { id: conversation.id, run_id: runId } },
    })),
    onSuccess: () => invalidateRun(conversation, runId),
  });
}

export function useAnswerRunRequest(conversation: ConversationScope, runId: string, requestId: string) {
  return useMutation({
    mutationFn: (answer: unknown) => apiData(api.POST('/api/conversations/{id}/runs/{run_id}/requests/{request_id}/reply', {
      params: { path: { id: conversation.id, run_id: runId, request_id: requestId } }, body: { answer },
    })),
    onSuccess: () => invalidateRun(conversation, runId),
  });
}

export function useDownloadRunEvent(conversationId: string, runId: string) {
  return useMutation({
    mutationFn: async (eventId: string) => {
      const blob = await apiData(api.GET('/api/conversations/{id}/runs/{run_id}/events/{event_id}', {
        params: { path: { id: conversationId, run_id: runId, event_id: eventId } }, parseAs: 'blob',
      }));
      saveDownload(blob, `run-event-${eventId}.json`);
    },
  });
}
