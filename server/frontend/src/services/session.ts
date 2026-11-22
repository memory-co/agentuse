import { useMutation, useQuery } from '@tanstack/react-query';
import { setActAs, useActAs } from '@/lib/actas';
import { api, ApiError, apiData } from '@/api/client';
import { clearCurrentUser, currentUserKey, queryClient } from '@/lib/query';

async function readCurrentUser(signal?: AbortSignal) {
  try {
    return await apiData(api.GET('/api/me', { signal }));
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) return null;
    throw error;
  }
}

export function useCurrentUser() {
  return useQuery({
    queryKey: currentUserKey,
    queryFn: ({ signal }) => readCurrentUser(signal),
    staleTime: Infinity,
  });
}

export function useActiveUserId() {
  const actingAs = useActAs();
  const current = useCurrentUser();
  return actingAs || current.data?.id;
}

export function useLogin(token: string, onLogin: () => void) {
  return useMutation({
    mutationFn: () => apiData(api.POST('/api/login', { body: { token: token.trim() } })),
    onSuccess: () => {
      onLogin();
      void queryClient.invalidateQueries({ queryKey: currentUserKey, exact: true });
    },
    onError: () => {},
    gcTime: 0,
  });
}

export function useLogout() {
  return useMutation({
    mutationFn: () => apiData(api.POST('/api/logout')),
    onSuccess: async () => {
      await queryClient.cancelQueries();
      // Keep the query observed by App so it sees the signed-out state.
      clearCurrentUser();
      setActAs('');
      queryClient.removeQueries({ predicate: query => query.queryKey[0] !== currentUserKey[0] });
      queryClient.getMutationCache().clear();
    },
  });
}

export function verifyBrowserSession(stillCurrent: () => boolean) {
  void readCurrentUser().then(user => {
    if (user === null && stillCurrent()) clearCurrentUser();
  }).catch(() => undefined);
}
