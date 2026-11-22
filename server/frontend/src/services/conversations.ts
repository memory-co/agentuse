import { useMutation, useQuery, type QueryFilters } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Conversation, ConversationSummary } from '@/lib/types';

const keys = {
  list: ['conversations'] as const,
  children: (parent: string) => ['conversations', 'children', parent] as const,
  summary: (id: string) => ['conversations', 'summary', id] as const,
};

const conversationPath = (id: string) => `/api/conversations/${encodeURIComponent(id)}`;

export type ConversationScope = Pick<Conversation, 'id' | 'parent_conversation_id'>;

export function isInvalidConversationRound(round?: number) {
  return round !== undefined && (!Number.isSafeInteger(round) || round <= 0);
}

export function useConversations() {
  return useQuery({
    queryKey: keys.list,
    queryFn: ({ signal }) => api<Conversation[]>('/api/conversations', { signal }),
    refetchOnWindowFocus: false,
  });
}

export function useChildren(parent: string, enabled = true) {
  return useQuery({
    queryKey: keys.children(parent),
    queryFn: ({ signal }) => api<Conversation[]>(`/api/conversations?parent_conversation_id=${encodeURIComponent(parent)}`, { signal }),
    enabled,
    refetchOnWindowFocus: false,
  });
}

export function useConversation(id: string, round?: number) {
  return useQuery({
    queryKey: round === undefined ? keys.summary(id) : [...keys.summary(id), 'round', round],
    queryFn: ({ signal }) => api<ConversationSummary>(`${conversationPath(id)}?view=summary${round === undefined ? '' : `&round=${round}`}`, { signal }),
    enabled: !!id && !isInvalidConversationRound(round),
    refetchOnWindowFocus: false,
  });
}

export function conversationChangeQueries(id?: string, parent?: string): QueryFilters[] {
  const list = { queryKey: parent ? keys.children(parent) : keys.list, exact: true };
  return id ? [{ queryKey: keys.summary(id) }, list] : [list];
}

export function invalidateConversation(conversation: ConversationScope) {
  for (const filter of conversationChangeQueries(conversation.id, conversation.parent_conversation_id)) {
    void queryClient.invalidateQueries(filter);
  }
}

export async function createConversation(mode: Conversation['mode']) {
  const { conversation } = await api<{ conversation: Conversation }>('/api/conversations', { method: 'POST', body: { mode } });
  invalidateConversation(conversation);
  return conversation;
}

export async function sendConversationMessage(conversation: ConversationScope, input: {
  prompt: string;
  targets: string[];
  users: string[];
  attachmentIds: string[];
}) {
  await api(`${conversationPath(conversation.id)}/messages`, {
    method: 'POST',
    body: { prompt: input.prompt, to_agent_ids: input.targets, to_user_ids: input.users, attachment_ids: input.attachmentIds },
  });
  invalidateConversation(conversation);
}

export function useRenameConversation(id: string) {
  return useMutation({
    mutationFn: (title: string) => api<Conversation>(conversationPath(id), { method: 'PATCH', body: { title } }),
    onSuccess: invalidateConversation,
  });
}

export function useSetConversationResolved(id: string) {
  return useMutation({
    mutationFn: (resolved: boolean) => api<Conversation>(`${conversationPath(id)}/${resolved ? 'resolve' : 'reopen'}`, { method: 'POST' }),
    onSuccess: invalidateConversation,
  });
}

export function useDeleteConversation() {
  return useMutation({
    mutationFn: (conversation: ConversationScope & Pick<Conversation, 'mode'>) =>
      api(`${conversationPath(conversation.id)}${conversation.mode === 'collaborative' ? '?recursive=true' : ''}`, { method: 'DELETE' }),
    onSuccess: (_, conversation) => invalidateConversation(conversation),
  });
}
