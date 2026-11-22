import { useMutation, useQuery } from '@tanstack/react-query';
import { api, apiData } from '@/api/client';
import { queryClient, usersKey } from '@/lib/query';
import type { User } from '@/lib/types';

export const userChangeQuery = { queryKey: usersKey, exact: true };

export function useUsers(enabled = true) {
  return useQuery({
    queryKey: usersKey,
    queryFn: ({ signal }) => apiData(api.GET('/api/users', { signal })),
    enabled,
  });
}

export function useCreateUser() {
  return useMutation({
    mutationFn: ({ id, name, role }: Pick<User, 'id' | 'name' | 'role'>) =>
      apiData(api.POST('/api/users', { body: { id: id.trim(), name: name.trim(), role } })),
    onSuccess: () => { void queryClient.invalidateQueries(userChangeQuery); },
    gcTime: 0,
  });
}
