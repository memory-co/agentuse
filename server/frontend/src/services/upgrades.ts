import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { queryClient } from '@/lib/query';

type UpgradeStatus = 'checking' | 'downloading' | 'prepared' | 'installing' | 'restarting' | 'succeeded' | 'failed' | 'unchanged';
type Upgrade = {
  id: string; from_version: string; target_version: string; status: UpgradeStatus; release_url: string;
  downloaded_bytes: number; total_bytes: number; error: string; requested_by: string;
  created_at: number; updated_at: number; completed_at: number | null;
};
type UpgradeState = {
  current_version: string; platform: string; supported: boolean; reason: string;
  active: Upgrade | null; upgrades: Upgrade[]; next_cursor: string | null;
};
type Release = { version: string; current_version: string; available: boolean; url: string; notes: string; published_at: string };

export const upgradeChangeQuery = { queryKey: ['upgrades'] as const };

export function useServerUpgrades(cursor: string) {
  return useQuery({
    queryKey: [...upgradeChangeQuery.queryKey, cursor],
    queryFn: ({ signal }) => api<UpgradeState>(`/api/upgrades${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`, { signal }),
    refetchInterval: query => query.state.data?.active || query.state.error ? 1500 : false,
  });
}

export function useCheckServerRelease() {
  return useMutation({ mutationFn: () => api<Release>('/api/upgrades/latest') });
}

export function useStartServerUpgrade() {
  return useMutation({
    mutationFn: () => api<Upgrade>('/api/upgrades', { method: 'POST' }),
    onSuccess: () => { void queryClient.invalidateQueries(upgradeChangeQuery); },
  });
}
