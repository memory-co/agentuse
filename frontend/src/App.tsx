import { useCallback, useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ArrowLeftRight, Bot, Boxes, Check, Languages, LogOut, Menu, MonitorSmartphone, SquarePen, Users as UsersIcon, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Loading, StatusBadge } from '@/components/Shared';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
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
import { Conversation, NewSession, STATUS_TONE, statusLabel, useSessions } from '@/pages/Sessions';
import { Agents } from '@/pages/Agents';
import { Harnesses } from '@/pages/Harnesses';
import { Users } from '@/pages/Users';
import { getLocale, setLocale, useT } from '@/lib/i18n';

// 进门先问 GET /api/me:401 显示登录页，否则是壳。admin 和普通用户共用这一套，只是导航项不同。
export function App() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => api<User>('/api/me').catch(e => { if (e instanceof ApiError && e.status === 401) return null; throw e; }),
    staleTime: Infinity,
  });
  if (me.isPending) return <div className="flex min-h-dvh items-center justify-center"><Loading /></div>;
  if (!me.data) return <Login />;
  return <ShellProvider><Shell me={me.data} /></ShellProvider>;
}

type PageKey = 'agents' | 'daemons' | 'harnesses' | 'users';
const PAGES: { key: PageKey; label: `nav.${PageKey}`; icon: ComponentType; Page: ComponentType<{ me: User }>; admin?: boolean }[] = [
  { key: 'agents', label: 'nav.agents', icon: Bot, Page: Agents },
  { key: 'daemons', label: 'nav.daemons', icon: MonitorSmartphone, Page: Daemons },
  { key: 'harnesses', label: 'nav.harnesses', icon: Boxes, Page: Harnesses, admin: true },
  { key: 'users', label: 'nav.users', icon: UsersIcon, Page: Users, admin: true },
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

/* 三种宽度，同一份侧栏(照 memory.talk):
   ≥1024  桌面：展开 / 图标栏由用户切换(记在 localStorage)
   768–1023 平板 / 横屏手机：默认图标栏，点开是挤开内容(不记)
   <768   竖屏手机：侧栏和主内容是横向吸附滚动的两整屏列，左右滑动切换，顶栏按钮兜底 */
const SIDEBAR_KEY = 'agentuse.sidebar';
function useMediaQuery(query: string) {
  const [matches, setMatches] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches);
  useEffect(() => {
    const mql = window.matchMedia(query);
    const onChange = () => setMatches(mql.matches);
    mql.addEventListener('change', onChange); setMatches(mql.matches);
    return () => mql.removeEventListener('change', onChange);
  }, [query]);
  return matches;
}
function ShellProvider({ children }: { children: ReactNode }) {
  const large = useMediaQuery('(min-width: 1024px)');
  const [desktopOpen, setDesktopOpen] = useState(() => { try { return localStorage.getItem(SIDEBAR_KEY) !== 'collapsed'; } catch { return true; } });
  const [tabletOpen, setTabletOpen] = useState(false);
  const onOpenChange = (next: boolean) => {
    if (!large) { setTabletOpen(next); return; }
    setDesktopOpen(next); try { localStorage.setItem(SIDEBAR_KEY, next ? 'expanded' : 'collapsed'); } catch { /* 无痕模式 */ }
  };
  return <SidebarProvider open={large ? desktopOpen : tabletOpen} onOpenChange={onOpenChange}>{children}</SidebarProvider>;
}

function Shell({ me }: { me: User }) {
  const t = useT();
  const route = useRoute();
  const actAs = useActAs();
  const admin = me.role === 'admin';
  // 代为别人时，页面按那个用户的身份看(普通用户权限)。
  const effective: User = admin && actAs ? { id: actAs, name: actAs, role: 'user', created_at: 0 } : me;
  const pages = PAGES.filter(p => !p.admin || admin);
  const sessions = useSessions();
  const { isMobile } = useSidebar();
  const current = route.page === 'session' ? sessions.data?.find(s => s.id === route.id) : undefined;
  const page = pages.find(p => p.key === route.page);
  const title = route.page === 'new' ? t('nav.new') : route.page === 'session' ? current?.title || route.id : page ? t(page.label) : '';
  const heading = <>
    <span className="min-w-0 truncate text-sm font-medium">{title}</span>
    {current && current.status !== 'idle' && <span className="shrink-0 whitespace-nowrap"><StatusBadge tone={STATUS_TONE[current.status]}>{statusLabel(current.status)}</StatusBadge></span>}
    {admin && actAs && <span className="ml-auto shrink-0 truncate text-xs text-muted-foreground">{t('actAs.acting', { id: actAs })}</span>}
  </>;
  const content = route.page === 'session' ? <Conversation key={route.id} id={route.id} onDeleted={() => go({ page: 'new' })} />
    : page ? <div className="mx-auto w-full max-w-5xl px-4 py-6"><page.Page me={effective} /></div>
    : <NewSession onCreated={id => go({ page: 'session', id })} />;
  const body = (onNavigate: () => void, onClose?: () => void) =>
    <SidebarBody me={me} pages={pages} route={route} sessions={sessions} onNavigate={onNavigate} onClose={onClose} />;
  if (isMobile) return <MobileShell route={route} body={body} heading={heading} page={content} />;
  return <>
    <Sidebar collapsible="icon">{body(() => undefined)}<SidebarRail /></Sidebar>
    <SidebarInset className="h-svh min-h-0 overflow-hidden">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <SidebarTrigger className="-ml-1" />
        <Separator orientation="vertical" className="mr-2 h-4" />
        {heading}
      </header>
      <main id="main-content" tabIndex={-1} className="flex min-h-0 flex-1 flex-col overflow-auto outline-none">{content}</main>
    </SidebarInset>
  </>;
}

/** 侧栏的内容：桌面放进 <Sidebar>,手机放进吸附列，同一份。 */
function SidebarBody({ me, pages, route, sessions, onNavigate, onClose }: {
  me: User; pages: typeof PAGES; route: Route; sessions: ReturnType<typeof useSessions>; onNavigate: () => void; onClose?: () => void;
}) {
  const t = useT();
  const actAs = useActAs();
  const admin = me.role === 'admin';
  const nav = (r: Route) => { go(r); onNavigate(); };
  const logout = useMutation({
    mutationFn: () => api('/api/logout', { method: 'POST' }),
    onSuccess: () => { setActAs(''); queryClient.clear(); queryClient.setQueryData(['me'], null); },
  });
  return <>
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem className="flex items-center gap-1">
          <SidebarMenuButton size="lg" onClick={() => nav({ page: 'new' })} tooltip="agentuse">
            <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-primary text-sm font-bold text-sidebar-primary-foreground">a</div>
            <span className="truncate font-semibold">agentuse</span>
          </SidebarMenuButton>
          {onClose && <Button variant="ghost" size="icon" className="size-8 shrink-0" onClick={onClose} aria-label={t('nav.closeNav')}><X /></Button>}
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>
    <SidebarContent>
      <SidebarGroup><SidebarGroupContent><SidebarMenu>
        <SidebarMenuItem><SidebarMenuButton isActive={route.page === 'new'} onClick={() => nav({ page: 'new' })} tooltip={t('nav.new')}><SquarePen /><span>{t('nav.new')}</span></SidebarMenuButton></SidebarMenuItem>
        {pages.map(p => <SidebarMenuItem key={p.key}>
          <SidebarMenuButton isActive={route.page === p.key} onClick={() => nav({ page: p.key })} tooltip={t(p.label)}><p.icon /><span>{t(p.label)}</span></SidebarMenuButton>
        </SidebarMenuItem>)}
      </SidebarMenu></SidebarGroupContent></SidebarGroup>
      <ActAsPicker me={me} value={admin ? actAs : ''} />
      <SidebarGroup className="group-data-[collapsible=icon]:hidden">
        <SidebarGroupLabel>{t('nav.sessions')}<span className="ml-auto">{sessions.data?.length || ''}</span></SidebarGroupLabel>
        <SidebarGroupContent><SidebarMenu>
          {sessions.isPending ? [1, 2, 3].map(i => <SidebarMenuItem key={i}><SidebarMenuSkeleton /></SidebarMenuItem>)
            : sessions.isError ? <p className="px-2 text-xs text-destructive">{t('nav.sessionsFailed')}</p>
            : sessions.data.length ? sessions.data.map(s => <SidebarMenuItem key={s.id}>
              <SidebarMenuButton isActive={route.page === 'session' && route.id === s.id} onClick={() => nav({ page: 'session', id: s.id })} title={s.title || s.id}>
                <span className="truncate pr-3">{s.title || s.id}</span>
              </SidebarMenuButton>
              {s.status !== 'idle' && <span className={cn('pointer-events-none absolute right-2 top-1/2 size-2 -translate-y-1/2 rounded-full', s.status === 'running' ? 'animate-pulse bg-sky-500' : s.status === 'waiting' ? 'animate-pulse bg-amber-500' : 'bg-amber-500')} title={statusLabel(s.status)} />}
            </SidebarMenuItem>)
            : <p className="px-2 text-xs text-muted-foreground">{t('nav.noSessions')}</p>}
        </SidebarMenu></SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
    <SidebarFooter>
      <SidebarMenu><LanguageItem /><SidebarMenuItem className="flex items-center gap-1">
        <SidebarMenuButton size="lg" className="flex-1" tooltip={me.name}>
          <div className="flex size-8 items-center justify-center rounded-full bg-muted text-sm font-medium">{me.name.slice(0, 1).toUpperCase()}</div>
          <div className="grid flex-1 text-left text-sm leading-tight"><span className="truncate font-semibold">{me.name}</span><span className="truncate text-xs text-muted-foreground">{admin ? t('common.admin') : t('common.normalUser')}</span></div>
        </SidebarMenuButton>
        <SidebarMenuAction onClick={() => logout.mutate()} title={t('nav.logout')} className="group-data-[collapsible=icon]:hidden"><LogOut /></SidebarMenuAction>
      </SidebarMenuItem></SidebarMenu>
    </SidebarFooter>
  </>;
}

/** 竖屏手机：侧栏 | 主内容 两个整屏列横向吸附，左右滑动切换。 */
function MobileShell({ route, body, heading, page }: {
  route: Route; body: (onNavigate: () => void, onClose?: () => void) => ReactNode; heading: ReactNode; page: ReactNode;
}) {
  const t = useT();
  const track = useRef<HTMLDivElement>(null);
  const [col, setCol] = useState(1);
  const goTo = useCallback((index: number, smooth = true) => {
    const el = track.current; const target = el?.children[index] as HTMLElement | undefined;
    if (el && target) el.scrollTo({ left: target.offsetLeft, behavior: smooth ? 'smooth' : 'auto' });
  }, []);
  useEffect(() => { goTo(1, false); }, [goTo]);                                        // 初始停在主内容
  const key = route.page === 'session' ? `s/${route.id}` : route.page;
  useEffect(() => { goTo(1); }, [key, goTo]);                                            // 导航后回到主内容
  const onScroll = () => { const el = track.current; if (el) setCol(Math.round(el.scrollLeft / el.clientWidth)); };   // 两列等宽：一列一页
  const safe = 'pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)]';
  return <div ref={track} onScroll={onScroll} className="flex h-dvh w-full snap-x snap-mandatory overflow-x-auto overflow-y-hidden overscroll-x-contain bg-background [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
    <div className={cn('group flex h-full w-full shrink-0 snap-start flex-col bg-sidebar text-sidebar-foreground', safe)} data-state="expanded" aria-label={t('nav.main')} aria-hidden={col !== 0}>
      {body(() => goTo(1), () => goTo(1))}
    </div>
    <div className={cn('flex h-full w-full shrink-0 snap-start flex-col', safe)} aria-hidden={col !== 1}>
      <header className="flex h-12 shrink-0 items-center gap-1 border-b px-2">
        <Button variant="ghost" size="icon" aria-label={col === 0 ? t('nav.closeNav') : t('nav.openNav')} aria-expanded={col === 0} onClick={() => goTo(col === 0 ? 1 : 0)}><Menu /></Button>
        {heading}
      </header>
      <main id="main-content" tabIndex={-1} className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto outline-none">{page}</main>
    </div>
  </div>;
}

/** 当前身份：灰色的名字;管理员旁边多一个切换按钮，点开是可搜索的用户列表。 */
function ActAsPicker({ me, value }: { me: User; value: string }) {
  const t = useT();
  const admin = me.role === 'admin';
  const [open, setOpen] = useState(false);
  const users = useQuery({ queryKey: ['users'], queryFn: () => api<User[]>('/api/users'), enabled: admin });
  const acting = value ? users.data?.find(u => u.id === value) : undefined;
  const name = value ? acting?.name || value : admin ? t('actAs.self', { name: me.name }) : me.name;
  const pick = (id: string) => { setOpen(false); if (id !== value) { setActAs(id); go({ page: 'new' }); } };
  return <SidebarGroup className="py-0 group-data-[collapsible=icon]:hidden">
    <SidebarGroupLabel>{t('actAs.label')}</SidebarGroupLabel>
    <div className="flex h-8 items-center gap-1 px-2">
      <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={value || me.id}>{name}</span>
      {admin && <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="icon" className="size-7 shrink-0 text-muted-foreground" title={t('actAs.switch')} aria-label={t('actAs.switch')}><ArrowLeftRight /></Button>
        </PopoverTrigger>
        <PopoverContent align="start" side="bottom" className="w-64 p-0">
          <Command>
            <CommandInput placeholder={t('actAs.search')} />
            <CommandList>
              <CommandEmpty>{users.isPending ? t('common.loading') : t('actAs.empty')}</CommandEmpty>
              <CommandGroup>
                <CommandItem value={`__self__ ${me.name} ${me.id}`} onSelect={() => pick('')}>
                  <Check className={cn(value && 'invisible')} /><span className="truncate">{t('actAs.self', { name: me.name })}</span>
                </CommandItem>
                {users.data?.filter(u => u.id !== me.id).map(u => <CommandItem key={u.id} value={`${u.name} ${u.id}`} onSelect={() => pick(u.id)}>
                  <Check className={cn(value !== u.id && 'invisible')} />
                  <span className="truncate">{u.name}</span>
                  {u.name !== u.id && <span className="ml-auto truncate font-mono text-xs text-muted-foreground">{u.id}</span>}
                </CommandItem>)}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>}
    </div>
  </SidebarGroup>;
}

/** 中英文切换：显示的是要切过去的那种语言。 */
export function LanguageItem() {
  const t = useT();
  return <SidebarMenuItem>
    <SidebarMenuButton onClick={() => setLocale(getLocale() === 'zh' ? 'en' : 'zh')} tooltip={t('nav.language')}><Languages /><span>{t('nav.language')}</span></SidebarMenuButton>
  </SidebarMenuItem>;
}
