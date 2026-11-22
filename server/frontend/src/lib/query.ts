import { QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { errText } from '@/lib/utils';

export const currentUserKey = ['me'] as const;
export const usersKey = ['users'] as const;

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: true },
    mutations: { onError: e => { toast.error(errText(e)); } },
  },
});

export function clearCurrentUser() {
  queryClient.setQueryData(currentUserKey, null);
}
