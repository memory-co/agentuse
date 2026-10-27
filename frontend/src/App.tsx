import { useCallback, useEffect, useRef, useState, type ComponentType, type ReactNode } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ArrowLeftRight, Bot, Boxes, Check, ChevronRight, Languages, LogOut, Menu, MonitorSmartphone, Plus, Users as UsersIcon, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Loading, StatusBadge } from '@/components/Shared';
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Separator } from '@/components/ui/separator';
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader, SidebarInset,
  SidebarMenu, SidebarMenuAction, SidebarMenuButton, SidebarMenuItem, SidebarMenuSkeleton, SidebarMenuSub, SidebarMenuSubButton, SidebarMenuSubItem,
  SidebarProvider, SidebarRail, SidebarTrigger, useSidebar,
} from '@/components/ui/sidebar';
import { api, ApiError } from '@/lib/api';
import { setActAs, useActAs } from '@/lib/actas';
import { queryClient } from '@/lib/query';
import { cn } from '@/lib/utils';
import type { Session, SessionDetail, User } from '@/lib/types';
import { Login } from '@/pages/Login';
import { Daemons } from '@/pages/Daemons';
import { Conversation, NewSession, STATUS_TONE, statusLabel, useSessions, useWorkSessions } from '@/pages/Sessions';
import { NewWork, WorkPage, useWorks } from '@/pages/Works';
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

// 路由放在 hash 里:#/ 新会话,#/s/<id> 某个会话,#/agents 等是管理页;
// 工作:#/works/new 新建工作,#/w/<id> 工作页(和协调者对话)。线程只能由协调者开，没有手动开的页面。
type Route = { page: 'new' } | { page: 'session'; id: string } | { page: PageKey }
  | { page: 'workNew' } | { page: 'work'; id: string };
function parse(): Route {
  const h = decodeURIComponent(location.hash.replace(/^#\/?/, ''));
  if (h.startsWith('s/')) return { page: 'session', id: h.slice(2) };
  if (h === 'works/new') return { page: 'workNew' };
  if (h.startsWith('w/')) return { page: 'work', id: h.slice(2) };
  const p = PAGES.find(p => p.key === h);
  return p ? { page: p.key } : { page: 'new' };
}
function href(r: Route) {
  switch (r.page) {
    case 'new': return '/';
    case 'session': return `/s/${encodeURIComponent(r.id)}`;
    case 'workNew': return '/works/new';
    case 'work': return `/w/${encodeURIComponent(r.id)}`;
    default: return `/${r.page}`;
  }
}
const go = (r: Route) => { location.hash = href(r); };
/** 路由落在哪个工作里(工作页);会话页要看会话自己的 work_id。 */
const routeWork = (r: Route) => r.page === 'work' ? r.id : '';
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
  const works = useWorks();
  const { isMobile } = useSidebar();
  // 和会话页共用一份缓存：线程不在「会话」列表里，标题和所属工作从这里拿
  const detail = useQuery({
    queryKey: ['sessions', route.page === 'session' ? route.id : ''],
    queryFn: () => api<SessionDetail>(`/api/sessions/${route.page === 'session' ? route.id : ''}`),
    enabled: route.page === 'session', refetchInterval: 3000,
  });
  const current = route.page === 'session' ? detail.data?.session : undefined;
  const workId = routeWork(route) || current?.work_id || '';
  const work = workId ? works.data?.find(w => w.id === workId) : undefined;
  const page = pages.find(p => p.key === route.page);
  const title = route.page === 'new' ? t('nav.new') : route.page === 'session' ? current?.title || route.id
    : route.page === 'workNew' ? t('work.newTitle') : route.page === 'work' ? work?.name || route.id
    : page ? t(page.label) : '';
  const heading = <>
    {work && route.page !== 'work' && <a href={`#${href({ page: 'work', id: work.id })}`} className="min-w-0 max-w-[40%] shrink truncate text-sm text-muted-foreground hover:text-foreground">{work.name} /</a>}
    <span className="min-w-0 truncate text-sm font-medium">{title}</span>
    {current && current.status !== 'idle' && <span className="shrink-0 whitespace-nowrap"><StatusBadge tone={STATUS_TONE[current.status]}>{statusLabel(current.status)}</StatusBadge></span>}
    {admin && actAs && <span className="ml-auto shrink-0 truncate text-xs text-muted-foreground">{t('actAs.acting', { id: actAs })}</span>}
  </>;
  const content = route.page === 'session' ? <Conversation key={route.id} id={route.id} onDeleted={() => go(current?.work_id ? { page: 'work', id: current.work_id } : { page: 'new' })} />
    : route.page === 'workNew' ? <NewWork me={effective} onCreated={id => go({ page: 'work', id })} />
    : route.page === 'work' ? <WorkPage key={route.id} id={route.id} onDeleted={() => go({ page: 'new' })} />
    : page ? <div className="mx-auto w-full max-w-5xl px-4 py-6"><page.Page me={effective} /></div>
    : <NewSession onCreated={id => go({ page: 'session', id })} />;
  const body = (onNavigate: () => void, onClose?: () => void) =>
    <SidebarBody me={me} pages={pages} route={route} workId={workId} sessions={sessions} works={works} onNavigate={onNavigate} onClose={onClose} />;
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
function SidebarBody({ me, pages, route, workId, sessions, works, onNavigate, onClose }: {
  me: User; pages: typeof PAGES; route: Route; workId: string; sessions: ReturnType<typeof useSessions>; works: ReturnType<typeof useWorks>;
  onNavigate: () => void; onClose?: () => void;
}) {
  const t = useT();
  const actAs = useActAs();
  const admin = me.role === 'admin';
  const nav = (r: Route) => { go(r); onNavigate(); };
  const [tab, setTab] = useSidebarTab();
  // 进了某个工作(工作页、线程)就切到「工作」tab,新会话页切到「会话」tab
  useEffect(() => { if (workId || route.page === 'workNew') setTab('works'); else if (route.page === 'new') setTab('sessions'); }, [workId, route.page, setTab]);
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
        {pages.map(p => <SidebarMenuItem key={p.key}>
          <SidebarMenuButton isActive={route.page === p.key} onClick={() => nav({ page: p.key })} tooltip={t(p.label)}><p.icon /><span>{t(p.label)}</span></SidebarMenuButton>
        </SidebarMenuItem>)}
      </SidebarMenu></SidebarGroupContent></SidebarGroup>
      <ActAsPicker me={me} value={admin ? actAs : ''} />
      <SidebarGroup className="group-data-[collapsible=icon]:hidden">
        <div className="mb-1 flex items-end gap-4 border-b px-2" role="tablist">
          {(['sessions', 'works'] as const).map(k => <button key={k} type="button" role="tab" aria-selected={tab === k} onClick={() => setTab(k)}
            className={cn('-mb-px border-b-2 pb-1.5 text-sm transition-colors', tab === k ? 'border-foreground font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}>
            {t(k === 'sessions' ? 'nav.tabSessions' : 'nav.tabWorks')}
          </button>)}
        </div>
        {tab === 'sessions'
          ? <SessionList sessions={sessions} route={route} nav={nav} />
          : <WorkList works={works} route={route} workId={workId} nav={nav} />}
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

type SidebarTab = 'sessions' | 'works';
const TAB_KEY = 'agentuse.sidebarTab';
function useSidebarTab() {
  const [tab, setTabState] = useState<SidebarTab>(() => { try { return localStorage.getItem(TAB_KEY) === 'works' ? 'works' : 'sessions'; } catch { return 'sessions'; } });
  const setTab = useCallback((v: SidebarTab) => { setTabState(v); try { localStorage.setItem(TAB_KEY, v); } catch { /* 无痕模式 */ } }, []);
  return [tab, setTab] as const;
}

const dotClass = (s: Session['status']) => s === 'running' ? 'animate-pulse bg-sky-500' : s === 'waiting' ? 'animate-pulse bg-amber-500' : 'bg-amber-500';

/** 「会话」tab:新会话 + 会话列表，和「工作」tab 一样：一个都没有时是虚线框空状态。 */
function SessionList({ sessions, route, nav }: { sessions: ReturnType<typeof useSessions>; route: Route; nav: (r: Route) => void }) {
  const t = useT();
  if (sessions.isPending) return <SidebarMenu>{[1, 2, 3].map(i => <SidebarMenuItem key={i}><SidebarMenuSkeleton /></SidebarMenuItem>)}</SidebarMenu>;
  if (sessions.isError) return <p className="px-2 text-xs text-destructive">{t('nav.sessionsFailed')}</p>;
  if (!sessions.data.length) return <div className="m-2 grid justify-items-center gap-3 rounded-xl border border-dashed px-3 py-6 text-center">
    <p className="text-sm text-muted-foreground">{t('nav.noSessions')}</p>
    <Button variant="secondary" size="sm" className="rounded-full" onClick={() => nav({ page: 'new' })}><Plus />{t('nav.new')}</Button>
  </div>;
  return <SidebarMenu>
    <SidebarMenuItem><SidebarMenuButton isActive={route.page === 'new'} onClick={() => nav({ page: 'new' })} className="text-muted-foreground"><Plus /><span>{t('nav.new')}</span></SidebarMenuButton></SidebarMenuItem>
    <SessionItems list={sessions.data} route={route} nav={nav} />
  </SidebarMenu>;
}

/** 会话列表：没完成的在上面，已完成的收在最下面的「已完成 (n)」里，默认折叠。 */
function SessionItems({ list, route, nav }: { list: Session[]; route: Route; nav: (r: Route) => void }) {
  const t = useT();
  const open = list.filter(s => s.status !== 'resolved');
  const done = list.filter(s => s.status === 'resolved');
  const item = (s: Session) => <SidebarMenuItem key={s.id}>
    <SidebarMenuButton isActive={route.page === 'session' && route.id === s.id} onClick={() => nav({ page: 'session', id: s.id })} title={s.title || s.id}
      className={cn(s.status === 'resolved' && 'text-muted-foreground')}>
      <span className="truncate pr-3">{s.title || s.id}</span>
    </SidebarMenuButton>
    {s.status !== 'idle' && s.status !== 'resolved' && <span className={cn('pointer-events-none absolute right-2 top-1/2 size-2 -translate-y-1/2 rounded-full', dotClass(s.status))} title={statusLabel(s.status)} />}
  </SidebarMenuItem>;
  return <>
    {open.map(item)}
    {done.length > 0 && <details className="group/done" open={done.some(s => route.page === 'session' && route.id === s.id) || undefined}>
      <summary className="flex cursor-pointer list-none items-center gap-1 px-2 py-1.5 text-xs text-muted-foreground hover:text-foreground">
        <ChevronRight className="size-3.5 transition-transform group-open/done:rotate-90" />{t('session.resolved', { n: done.length })}
      </summary>
      {done.map(item)}
    </details>}
  </>;
}

/** 「工作」tab:新建工作 + 工作列表，展开的工作下面列它的线程。 */
function WorkList({ works, route, workId, nav }: { works: ReturnType<typeof useWorks>; route: Route; workId: string; nav: (r: Route) => void }) {
  const t = useT();
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const isOpen = (id: string) => expanded[id] ?? id === workId;
  if (works.isPending) return <SidebarMenu>{[1, 2].map(i => <SidebarMenuItem key={i}><SidebarMenuSkeleton /></SidebarMenuItem>)}</SidebarMenu>;
  if (works.isError) return <p className="px-2 text-xs text-destructive">{t('nav.worksFailed')}</p>;
  if (!works.data.length) return <div className="m-2 grid justify-items-center gap-3 rounded-xl border border-dashed px-3 py-6 text-center">
    <p className="text-sm text-muted-foreground">{t('nav.noWorks')}</p>
    <Button variant="secondary" size="sm" className="rounded-full" onClick={() => nav({ page: 'workNew' })}><Plus />{t('nav.newWork')}</Button>
  </div>;
  return <SidebarMenu>
    <SidebarMenuItem><SidebarMenuButton isActive={route.page === 'workNew'} onClick={() => nav({ page: 'workNew' })} className="text-muted-foreground"><Plus /><span>{t('nav.newWork')}</span></SidebarMenuButton></SidebarMenuItem>
    {works.data.map(w => <SidebarMenuItem key={w.id}>
      <SidebarMenuButton isActive={route.page === 'work' && route.id === w.id} onClick={() => nav({ page: 'work', id: w.id })} title={w.name}>
        <span className="truncate pr-5">{w.name}</span>
      </SidebarMenuButton>
      <SidebarMenuAction onClick={() => setExpanded(e => ({ ...e, [w.id]: !isOpen(w.id) }))} aria-label={w.name} aria-expanded={isOpen(w.id)}>
        <ChevronRight className={cn('transition-transform', isOpen(w.id) && 'rotate-90')} />
      </SidebarMenuAction>
      {isOpen(w.id) && <WorkThreads workId={w.id} route={route} nav={nav} />}
    </SidebarMenuItem>)}
  </SidebarMenu>;
}

function WorkThreads({ workId, route, nav }: { workId: string; route: Route; nav: (r: Route) => void }) {
  const t = useT();
  const threads = useWorkSessions(workId);
  if (!threads.data) return null;
  const open = threads.data.filter(s => s.status !== 'resolved');
  const done = threads.data.filter(s => s.status === 'resolved');
  const item = (s: Session) => <SidebarMenuSubItem key={s.id} className="relative">
    <SidebarMenuSubButton isActive={route.page === 'session' && route.id === s.id} onClick={() => nav({ page: 'session', id: s.id })} title={s.title || s.id}
      className={cn('cursor-pointer', s.status === 'resolved' && 'text-muted-foreground')}>
      <span className="truncate pr-3">{s.title || s.id}</span>
    </SidebarMenuSubButton>
    {s.status !== 'idle' && s.status !== 'resolved' && <span className={cn('pointer-events-none absolute right-1 top-1/2 size-2 -translate-y-1/2 rounded-full', dotClass(s.status))} title={statusLabel(s.status)} />}
  </SidebarMenuSubItem>;
  return <SidebarMenuSub>
    {!threads.data.length && <li className="px-2 py-1 text-xs text-muted-foreground">{t('nav.noThreads')}</li>}
    {open.map(item)}
    {done.length > 0 && <details className="group/done" open={done.some(s => route.page === 'session' && route.id === s.id) || undefined}>
      <summary className="flex cursor-pointer list-none items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-foreground">
        <ChevronRight className="size-3.5 transition-transform group-open/done:rotate-90" />{t('session.resolved', { n: done.length })}
      </summary>
      {done.map(item)}
    </details>}
  </SidebarMenuSub>;
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
  const key = href(route);
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
