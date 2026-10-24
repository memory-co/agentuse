import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { LogOut } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Loading } from '@/components/Shared';
import { api, ApiError } from '@/lib/api';
import { queryClient } from '@/lib/query';
import type { User } from '@/lib/types';
import { Login } from '@/pages/Login';
import { Daemons } from '@/pages/Daemons';
import { Sessions } from '@/pages/Sessions';
import { Harnesses } from '@/pages/Harnesses';
import { Users } from '@/pages/Users';

// 进门先问 GET /api/me:401 显示登录页，否则是壳。
export function App() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => api<User>('/api/me').catch(e => { if (e instanceof ApiError && e.status === 401) return null; throw e; }),
    staleTime: Infinity,
  });
  if (me.isPending) return <div className="flex min-h-dvh items-center justify-center"><Loading /></div>;
  if (!me.data) return <Login />;
  return <Shell me={me.data} />;
}

const TABS = [
  { key: 'daemons', label: '机器', Page: Daemons },
  { key: 'sessions', label: '会话', Page: Sessions },
  { key: 'harnesses', label: 'Harness', Page: Harnesses },
  { key: 'users', label: '用户', Page: Users, admin: true },
] as const;

// 当前 tab 放在 hash 里(#/sessions),刷新不丢。
function useHashTab(keys: string[]) {
  const read = () => { const k = location.hash.replace(/^#\/?/, ''); return keys.includes(k) ? k : keys[0]; };
  const [tab, setTab] = useState(read);
  useEffect(() => { const on = () => setTab(read()); addEventListener('hashchange', on); return () => removeEventListener('hashchange', on); });
  return [tab, (k: string) => { location.hash = '/' + k; setTab(k); }] as const;
}

function Shell({ me }: { me: User }) {
  const tabs = TABS.filter(t => !('admin' in t) || me.role === 'admin');
  const [tab, setTab] = useHashTab(tabs.map(t => t.key));
  const logout = useMutation({
    mutationFn: () => api('/api/logout', { method: 'POST' }),
    onSuccess: () => { queryClient.clear(); queryClient.setQueryData(['me'], null); },
  });
  return <Tabs value={tab} onValueChange={setTab} className="min-h-dvh">
    <header className="sticky top-0 z-10 border-b bg-background/95 backdrop-blur">
      <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
        <span className="font-semibold">agentuse</span>
        <TabsList className="order-last w-full justify-start overflow-x-auto sm:order-none sm:w-auto">
          {tabs.map(t => <TabsTrigger key={t.key} value={t.key}>{t.label}</TabsTrigger>)}
        </TabsList>
        <div className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
          <span>{me.name}{me.role === 'admin' && '(管理员)'}</span>
          <Button variant="ghost" size="sm" onClick={() => logout.mutate()}><LogOut />退出</Button>
        </div>
      </div>
    </header>
    <main className="mx-auto max-w-5xl px-4 pb-10">
      {tabs.map(({ key, Page }) => <TabsContent key={key} value={key} className="mt-6"><Page me={me} /></TabsContent>)}
    </main>
  </Tabs>;
}
