import { useMutation, useQuery } from '@tanstack/react-query';
import { api, apiData } from '@/api/client';
import { queryClient } from '@/lib/query';

export const upgradeChangeQuery = { queryKey: ['upgrades'] as const };

export function useServerUpgrades(cursor: string) {
  return useQuery({
    queryKey: [...upgradeChangeQuery.queryKey, cursor],
    queryFn: ({ signal }) => apiData(api.GET('/api/upgrades', { params: { query: { cursor: cursor || undefined } }, signal })),
    refetchInterval: query => query.state.data?.active || query.state.error ? 1500 : false,
  });
}

export function useCheckServerRelease() {
  return useMutation({ mutationFn: () => apiData(api.GET('/api/upgrades/latest')) });
}

export function useStartServerUpgrade() {
  return useMutation({
    mutationFn: () => apiData(api.POST('/api/upgrades')),
    onSuccess: () => { void queryClient.invalidateQueries(upgradeChangeQuery); },
  });
}
