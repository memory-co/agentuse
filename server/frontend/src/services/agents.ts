import { useMutation, useQuery, type QueryFilters } from '@tanstack/react-query';
import { api, apiData } from '@/api/client';
import { queryClient } from '@/lib/query';
import type { Agent, AgentVersion, SkillVersion } from '@/lib/types';

const listKey = ['agents'] as const;
const versionsKey = (id: string) => ['agent-versions', id] as const;
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
    queryFn: ({ signal }) => apiData(api.GET('/api/agents', { signal })),
    staleTime: 30_000,
  });
}

export function useAgentVersions(id: string) {
  return useQuery({
    queryKey: versionsKey(id),
    queryFn: ({ signal }) => apiData(api.GET('/api/agents/{id}/versions', { params: { path: { id } }, signal })),
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
        const previous = editing?.skills?.find(item => item.skill_version_id === versionId);
        if (previous) return previous;
        throw new Error('Unknown skill version');
      });
      const body = { ...fields, name: fields.name.trim(), model: fields.model.trim(), skills };
      if (editing) {
        await apiData(api.POST('/api/agents/{id}/versions', {
          params: { path: { id: editing.id } }, body: { ...body, base_version_id: editing.current_version_id },
        }));
        return editing.id;
      }
      const created = await apiData(api.POST('/api/agents', { body: { ...body, id: id.trim() } }));
      return created.id;
    },
    onSuccess: invalidateAgent,
  });
}

export function useDeleteAgent() {
  return useMutation({
    mutationFn: (id: string) => apiData(api.DELETE('/api/agents/{id}', { params: { path: { id } } })),
    onSuccess: (_, id) => invalidateAgent(id),
  });
}
