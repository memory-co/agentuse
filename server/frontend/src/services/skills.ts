import { queryOptions, useMutation, useQueries, useQuery, type QueryFilters, type UseQueryResult } from '@tanstack/react-query';
import { api, apiData, downloadFile, multipartBody } from '@/api/client';
import { queryClient } from '@/lib/query';
import type { Skill, SkillVersion } from '@/lib/types';

const listKey = ['skills'] as const;
const versionsKey = (id: string) => ['skill-versions', id] as const;
export function skillChangeQueries(id?: string): QueryFilters[] {
  const list = { queryKey: listKey, exact: true };
  return id ? [list, { queryKey: versionsKey(id), exact: true }] : [list];
}

function invalidateSkill(id: string) {
  for (const filter of skillChangeQueries(id)) void queryClient.invalidateQueries(filter);
}

export function useSkills() {
  return useQuery({ queryKey: listKey, queryFn: ({ signal }) => apiData(api.GET('/api/skills', { signal })) });
}

function skillVersionsOptions(id: string) {
  return queryOptions({
    queryKey: versionsKey(id),
    queryFn: ({ signal }) => apiData(api.GET('/api/skills/{id}/versions', { params: { path: { id } }, signal })),
    enabled: !!id,
  });
}

export function useSkillVersions(id: string) {
  return useQuery(skillVersionsOptions(id));
}

function combineVersions(results: UseQueryResult<SkillVersion[]>[]) {
  const failed = results.find(result => result.isError);
  const isPending = results.some(result => result.isPending);
  return {
    data: results.every(result => result.data !== undefined) ? results.flatMap(result => result.data ?? []) : undefined,
    error: failed?.error,
    isError: !!failed,
    isPending: isPending && !failed,
  };
}

export function useSkillCatalogVersions(skills: Skill[] | undefined) {
  const versions = useQueries({ queries: (skills ?? []).map(skill => skillVersionsOptions(skill.id)), combine: combineVersions });
  return { ...versions, isPending: !skills || versions.isPending };
}

export function useUploadSkill() {
  return useMutation({
    mutationFn: ({ file, selected }: { file: File; selected: Skill | null }) => selected
      ? apiData(api.POST('/api/skills/{id}/versions', {
        params: { path: { id: selected.id } }, body: { file, base_version_id: selected.current_version_id }, bodySerializer: multipartBody,
      }))
      : apiData(api.POST('/api/skills', { body: { file }, bodySerializer: multipartBody })),
    onSuccess: skill => invalidateSkill(skill.id),
  });
}

export function useDeleteSkill() {
  return useMutation({
    mutationFn: (id: string) => apiData(api.DELETE('/api/skills/{id}', { params: { path: { id } } })),
    onSuccess: (_, id) => invalidateSkill(id),
  });
}

export function useDownloadSkillVersion() {
  return useMutation({ mutationFn: (version: SkillVersion) => downloadFile(version.download_path, `${version.name}-v${version.version}.zip`) });
}
