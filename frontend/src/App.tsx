import { useEffect, useState, type ComponentType } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Bot, Boxes, LogOut, MonitorSmartphone, SquarePen, Users as UsersIcon } from 'lucide-react';
import { Loading, StatusBadge } from '@/components/Shared';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Separator } from '@/components/ui/separator';
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader, SidebarInset,
  SidebarMenu, SidebarMenuAction, SidebarMenuButton, SidebarMenuItem, SidebarMenuSkeleton, SidebarProvider, SidebarRail, SidebarTrigger, useSidebar,
} from '@/components/ui/sidebar';
import { api, ApiError } from '@/lib/api';
import { setActAs, useActAs } from '@/lib/actas';
import { queryClient } from '@/lib/query';
import { cn } from '@/lib/utils';
import type { User } from '@/lib/types';
import { Login } from '@/pages/Login';
import { Daemons } from '@/pages/Daemons';
import { Conversation, NewSession, STATUS, useSessions } from '@/pages/Sessions';
import { Agents } from '@/pages/Agents';
import { Harnesses } from '@/pages/Harnesses';
import { Users } from '@/pages/Users';

// 进门先问 GET /api/me:401 显示登录页，否则是壳。admin 和普通用户共用这一套，只是导航项不同。
export function App() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => api<User>('/api/me').catch(e => { if (e instanceof ApiError && e.status === 401) return null; throw e; }),
    staleTime: Infinity,
  });
  if (me.isPending) return <div className="flex min-h-dvh items-center justify-center"><Loading /></div>;
  if (!me.data) return <Login />;
  return <SidebarProvider><Shell me={me.data} /></SidebarProvider>;
}

type PageKey = 'agents' | 'daemons' | 'harnesses' | 'users';
const PAGES: { key: PageKey; label: string; icon: ComponentType; Page: ComponentType<{ me: User }>; admin?: boolean }[] = [
  { key: 'agents', label: 'Agents', icon: Bot, Page: Agents },
  { key: 'daemons', label: 'Daemons', icon: MonitorSmartphone, Page: Daemons },
  { key: 'harnesses', label: 'Harness', icon: Boxes, Page: Harnesses, admin: true },
  { key: 'users', label: 'Users', icon: UsersIcon, Page: Users, admin: true },
];

// 路由放在 hash 里:#/ 新会话,#/s/<id> 某个会话,#/agents 等是管理页。
type Route = { page: 'new' } | { page: 'session'; id: string } | { page: PageKey };
function parse(): Route {
  const h = decodeURIComponent(location.hash.replace(/^#\/?/, ''));
  if (h.startsWith('s/')) return { page: 'session', id: h.slice(2) };
  const p = PAGES.find(p => p.key === h);
  return p ? { page: p.key } : { page: 'new' };
}
const go = (r: Route) => { location.hash = r.page === 'new' ? '/' : r.page === 'session' ? `/s/${encodeURIComponent(r.id)}` : `/${r.page}`; };
function useRoute() {
  const [route, setRoute] = useState(parse);
  useEffect(() => { const on = () => setRoute(parse()); addEventListener('hashchange', on); return () => removeEventListener('hashchange', on); }, []);
  return route;
}

function Shell({ me }: { me: User }) {
  const route = useRoute();
  const actAs = useActAs();
  const admin = me.role === 'admin';
  // 代为别人时，页面按那个用户的身份看(普通用户权限)。
  const effective: User = admin && actAs ? { id: actAs, name: actAs, role: 'user', created_at: 0 } : me;
  const pages = PAGES.filter(p => !p.admin || admin);
  const sessions = useSessions();
  const { isMobile, setOpenMobile } = useSidebar();
  const nav = (r: Route) => { go(r); if (isMobile) setOpenMobile(false); };
  const logout = useMutation({
    mutationFn: () => api('/api/logout', { method: 'POST' }),
    onSuccess: () => { setActAs(''); queryClient.clear(); queryClient.setQueryData(['me'], null); },
  });
  const current = route.page === 'session' ? sessions.data?.find(s => s.id === route.id) : undefined;
  const page = pages.find(p => p.key === route.page);
  const title = route.page === 'new' ? '新会话' : route.page === 'session' ? current?.title || route.id : page?.label ?? '';

  return <>
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" onClick={() => nav({ page: 'new' })} tooltip="agentuse">
              <div className="flex size-8 items-center justify-center rounded-lg bg-sidebar-primary text-sm font-bold text-sidebar-primary-foreground">a</div>
              <span className="truncate font-semibold">agentuse</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup><SidebarGroupContent><SidebarMenu>
          <SidebarMenuItem><SidebarMenuButton isActive={route.page === 'new'} onClick={() => nav({ page: 'new' })} tooltip="新会话"><SquarePen /><span>新会话</span></SidebarMenuButton></SidebarMenuItem>
          {pages.map(p => <SidebarMenuItem key={p.key}>
            <SidebarMenuButton isActive={route.page === p.key} onClick={() => nav({ page: p.key })} tooltip={p.label}><p.icon /><span>{p.label}</span></SidebarMenuButton>
          </SidebarMenuItem>)}
        </SidebarMenu></SidebarGroupContent></SidebarGroup>
        {admin && <ActAsPicker me={me} value={actAs} />}
        <SidebarGroup className="group-data-[collapsible=icon]:hidden">
          <SidebarGroupLabel>会话<span className="ml-auto">{sessions.data?.length || ''}</span></SidebarGroupLabel>
          <SidebarGroupContent><SidebarMenu>
            {sessions.isPending ? [1, 2, 3].map(i => <SidebarMenuItem key={i}><SidebarMenuSkeleton /></SidebarMenuItem>)
              : sessions.isError ? <p className="px-2 text-xs text-destructive">会话加载失败</p>
              : sessions.data.length ? sessions.data.map(s => <SidebarMenuItem key={s.id}>
                <SidebarMenuButton isActive={route.page === 'session' && route.id === s.id} onClick={() => nav({ page: 'session', id: s.id })} title={s.title || s.id}>
                  <span className="truncate pr-3">{s.title || s.id}</span>
                </SidebarMenuButton>
                {s.status !== 'idle' && <span className={cn('pointer-events-none absolute right-2 top-1/2 size-2 -translate-y-1/2 rounded-full', s.status === 'running' ? 'animate-pulse bg-sky-500' : 'bg-amber-500')} title={STATUS[s.status][0]} />}
              </SidebarMenuItem>)
              : <p className="px-2 text-xs text-muted-foreground">还没有会话</p>}
          </SidebarMenu></SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu><SidebarMenuItem className="flex items-center gap-1">
          <SidebarMenuButton size="lg" className="flex-1" tooltip={me.name}>
            <div className="flex size-8 items-center justify-center rounded-full bg-muted text-sm font-medium">{me.name.slice(0, 1).toUpperCase()}</div>
            <div className="grid flex-1 text-left text-sm leading-tight"><span className="truncate font-semibold">{me.name}</span><span className="truncate text-xs text-muted-foreground">{admin ? '管理员' : '用户'}</span></div>
          </SidebarMenuButton>
          <SidebarMenuAction onClick={() => logout.mutate()} title="退出" className="group-data-[collapsible=icon]:hidden"><LogOut /></SidebarMenuAction>
        </SidebarMenuItem></SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
    <SidebarInset className="h-svh min-h-0 overflow-hidden">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <SidebarTrigger className="-ml-1" />
        <Separator orientation="vertical" className="mr-2 h-4" />
        <span className="min-w-0 truncate text-sm font-medium">{title}</span>
        {current && current.status !== 'idle' && <span className="shrink-0 whitespace-nowrap"><StatusBadge tone={STATUS[current.status][1]}>{STATUS[current.status][0]}</StatusBadge></span>}
        {admin && actAs && <span className="ml-auto shrink-0 text-xs text-muted-foreground">代为 <b className="font-mono">{actAs}</b></span>}
      </header>
      <main id="main-content" tabIndex={-1} className="flex min-h-0 flex-1 flex-col overflow-auto outline-none">
        {route.page === 'new' ? <NewSession onCreated={id => go({ page: 'session', id })} />
          : route.page === 'session' ? <Conversation key={route.id} id={route.id} onDeleted={() => go({ page: 'new' })} />
          : page ? <div className="mx-auto w-full max-w-5xl px-4 py-6"><page.Page me={effective} /></div>
          : <NewSession onCreated={id => go({ page: 'session', id })} />}
      </main>
    </SidebarInset>
  </>;
}

/** 管理员切换身份：自己(看全部)或代为某个用户(和那个用户看到的一样)。 */
function ActAsPicker({ me, value }: { me: User; value: string }) {
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/api/users') });
  const SELF = '__self__';
  return <SidebarGroup className="py-0 group-data-[collapsible=icon]:hidden">
    <SidebarGroupLabel>身份</SidebarGroupLabel>
    <Select value={value || SELF} onValueChange={v => { setActAs(v === SELF ? '' : v); go({ page: 'new' }); }}>
      <SelectTrigger className="h-8 bg-background text-xs"><SelectValue /></SelectTrigger>
      <SelectContent>
        <SelectItem value={SELF}>{me.name}(管理员，看全部)</SelectItem>
        {users.data?.filter(u => u.id !== me.id).map(u => <SelectItem key={u.id} value={u.id}>{u.name}{u.name !== u.id && <span className="ml-1 font-mono text-muted-foreground">{u.id}</span>}</SelectItem>)}
        {value && !users.data?.some(u => u.id === value) && <SelectItem value={value}>{value}</SelectItem>}
      </SelectContent>
    </Select>
  </SidebarGroup>;
}
