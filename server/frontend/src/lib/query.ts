import { QueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { errText } from '@/lib/utils';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: true },
    mutations: { onError: e => { toast.error(errText(e)); } },
  },
});
