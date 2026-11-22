import { useMutation, useQuery, type QueryFilters } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { Agent, AgentVersion, SkillVersion } from '@/lib/types';

const listKey = ['agents'] as const;
const versionsKey = (id: string) => ['agent-versions', id] as const;
const agentPath = (id: string) => `/api/agents/${encodeURIComponent(id)}`;

export function agentChangeQueries(id?: string): QueryFilters[] {
  const list = { queryKey: listKey, exact: true };
  return id ? [list, { queryKey: versionsKey(id), exact: true }] : [list];
}

function invalidateAgent(id: string) {
  for (const filter of agentChangeQueries(id)) void queryClient.invalidateQueries(filter);
}

export function useAgents() {
  return useQuery({
    queryKey: listKey,
    queryFn: ({ signal }) => api<Agent[]>('/api/agents', { signal }),
    staleTime: 30_000,
  });
}

export function useAgentVersions(id: string) {
  return useQuery({
    queryKey: versionsKey(id),
    queryFn: ({ signal }) => api<AgentVersion[]>(`${agentPath(id)}/versions`, { signal }),
    enabled: !!id,
  });
}

type AgentDraft = {
  id: string;
  name: string;
  description: string;
  device_id: string;
  harness: string;
  model: string;
  instructions: string;
  skillVersionIds: string[];
};

export function useSaveAgent() {
  return useMutation({
    mutationFn: async ({ draft, editing, versions }: { draft: AgentDraft; editing: Agent | null; versions?: SkillVersion[] }) => {
      const { id, skillVersionIds, ...fields } = draft;
      const skills = skillVersionIds.map(versionId => {
        const version = versions?.find(item => item.id === versionId);
        if (version) return { skill_id: version.skill_id, skill_version_id: version.id };
        const previous = editing?.skills.find(item => item.skill_version_id === versionId);
        if (previous) return previous;
        throw new Error('Unknown skill version');
      });
      const body = { ...fields, name: fields.name.trim(), model: fields.model.trim(), skills };
      if (editing) {
        await api(`${agentPath(editing.id)}/versions`, { method: 'POST', body: { ...body, base_version_id: editing.current_version_id } });
        return editing.id;
      }
      const created = await api<Agent>('/api/agents', { method: 'POST', body: { ...body, id: id.trim() } });
      return created.id;
    },
    onSuccess: invalidateAgent,
  });
}

export function useDeleteAgent() {
  return useMutation({
    mutationFn: (id: string) => api(agentPath(id), { method: 'DELETE' }),
    onSuccess: (_, id) => invalidateAgent(id),
  });
}
