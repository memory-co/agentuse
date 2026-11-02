import React, { useEffect, useState, useRef } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryClient,
  QueryClientProvider,
  useInfiniteQuery,
  useQuery,
  useMutation,
} from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import {
  ArrowLeftRight,
  Bot,
  Check,
  ChevronRight,
  Circle,
  FolderOpen,
  Loader2,
  Monitor,
  Plus,
  Send,
  RefreshCw,
  Settings,
  Terminal,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandInput,
  CommandList,
  CommandEmpty,
  CommandGroup,
  CommandItem,
} from "@/components/ui/command";
import {
  Sidebar,
  SidebarProvider,
  SidebarHeader,
  SidebarContent,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton,
  SidebarFooter,
  SidebarInset,
  SidebarTrigger,
  SidebarRail,
} from "@/components/ui/sidebar";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import "./style.css";

type Endpoint = {
  endpoint_id: string;
  server_url: string;
  name: string;
  enabled: boolean;
};
type Conversation = {
  id: string;
  endpoint_id: string;
  workspace: string;
  title?: string;
};
type Session = {
  id: string;
  agent_id: string;
  harness: string;
  harness_revision: string;
  status: string;
  last_context_seq: number;
  created_at: number;
  last_used_at: number;
};
type Usage = {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
};
type Run = {
  run_id: string;
  conversation_id: string;
  session_id: string;
  agent_id: string;
  harness: string;
  status: string;
  summary: string;
  output?: string;
  error?: string;
  received_at: number;
  finished_at?: number;
  duration_ms: number;
  usage: Usage | null;
  cost_usd?: number;
  events?: { kind: string; at: number; payload: unknown }[];
};
type Page<T> = { items: T[]; next_cursor?: string };
const client = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: true } },
});
let token = new URLSearchParams(location.hash.slice(1)).get("token") ?? "";
if (token) history.replaceState(null, "", location.pathname + location.search);
class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}
async function api<T>(path: string, signal?: AbortSignal): Promise<T> {
  const r = await fetch(path, {
    signal,
    credentials: "same-origin",
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  const data = await r.json();
  if (!r.ok) throw new ApiError(r.status, data.error?.message ?? "请求失败");
  token = ""; // The validated token is now held in an HttpOnly cookie.
  return data;
}
async function post<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok)
    throw new ApiError(response.status, data.error?.message ?? "请求失败");
  token = "";
  return data;
}
const enc = encodeURIComponent;
const date = (n?: number) => (n ? new Date(n).toLocaleString() : "—");
const labels: Record<string, string> = {
  creating: "创建中",
  idle: "空闲",
  running: "执行中",
  waiting: "等待回答",
  pending_report: "待回报",
  completed: "已完成",
  done: "已完成",
  failed: "失败",
  unavailable: "不可恢复",
  closed: "已关闭",
  cancelled: "已取消",
  canceled: "已取消",
  interrupted: "已中断",
  rejected: "已拒绝",
};
function Status({ value }: { value: string }) {
  return (
    <Badge variant="outline" className="gap-1.5 font-normal">
      <Circle
        className={`size-2 fill-current ${value === "running" || value === "waiting" ? "text-emerald-500" : value === "failed" ? "text-red-500" : "text-muted-foreground"}`}
      />
      {labels[value] ?? value}
    </Badge>
  );
}
function ErrorNotice({ error }: { error: unknown }) {
  return error ? (
    <div
      role="alert"
      className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive"
    >
      {error instanceof Error ? error.message : String(error)}
      {error instanceof ApiError && error.status === 401 && (
        <p className="mt-2">请运行 agentuse ui 获取新的本机页面链接。</p>
      )}
    </div>
  ) : null;
}
function usePages<T>(key: unknown[], path: string, enabled = true) {
  return useInfiniteQuery({
    queryKey: key,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      api<Page<T>>(
        path + (path.includes("?") ? "&" : "?") + "cursor=" + enc(pageParam),
        signal,
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled,
    refetchInterval: 3000,
  });
}
function App() {
  const endpoints = useQuery({
    queryKey: ["endpoints"],
    queryFn: ({ signal }) => api<Page<Endpoint>>("/api/endpoints", signal),
    refetchInterval: 5000,
  });
  const [endpoint, setEndpoint] = useState(() => {
    try {
      return localStorage.getItem("agentuse.client.endpoint") ?? "";
    } catch {
      return "";
    }
  });
  const [conversation, setConversation] = useState("");
  const [session, setSession] = useState("");
  const [selected, setSelected] = useState<Run | null>(null);
  const [switcher, setSwitcher] = useState(false);
  const [settings, setSettings] = useState(false);
  const routeRef = useRef({ endpoint, conversation });
  routeRef.current = { endpoint, conversation };
  const items = endpoints.data?.items ?? [];
  useEffect(() => {
    if (endpoints.data && !items.some((e) => e.endpoint_id === endpoint)) {
      setEndpoint(items[0]?.endpoint_id ?? "");
      setConversation("");
      setSession("");
      setSelected(null);
    }
  }, [endpoints.data, endpoint]);
  const current = items.find((e) => e.endpoint_id === endpoint);
  const local = endpoint === "local";
  const [newTitle, setNewTitle] = useState("");
  const createConversation = useMutation({
    mutationFn: (title: string) =>
      post<Conversation>("/api/local/conversations", { title }),
    onSuccess: async (created) => {
      await client.invalidateQueries({ queryKey: ["conversations", "local"] });
      if (routeRef.current.endpoint !== "local") return;
      setConversation(created.id);
      setSession("");
      setSelected(null);
      setNewTitle("");
    },
  });
  const conversations = usePages<Conversation>(
    ["conversations", endpoint],
    `/api/conversations?endpoint_id=${enc(endpoint)}`,
    !!current,
  );
  const base = `/api/conversations/${enc(endpoint)}/${enc(conversation)}`;
  const sessions = usePages<Session>(
    ["sessions", endpoint, conversation],
    base + "/sessions",
    !!current && !!conversation,
  );
  const sessionItems = sessions.data?.pages.flatMap((p) => p.items) ?? [];
  useEffect(() => {
    if (sessions.data && !sessionItems.some((s) => s.id === session)) {
      setSession(sessionItems[0]?.id ?? "");
      setSelected(null);
    }
  }, [sessions.data, session]);
  const activeSession = sessionItems.find((s) => s.id === session);
  const runs = usePages<Run>(
    ["runs", endpoint, conversation, session],
    base + `/sessions/${enc(session)}/runs`,
    !!activeSession,
  );
  function changeEndpoint(id: string) {
    setEndpoint(id);
    try {
      localStorage.setItem("agentuse.client.endpoint", id);
    } catch {}
    setConversation("");
    setSession("");
    setSelected(null);
    setSwitcher(false);
  }
  const conversationItems =
    conversations.data?.pages.flatMap((p) => p.items) ?? [];
  const currentTitle =
    conversationItems.find((c) => c.id === conversation)?.title || conversation;
  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader className="gap-3 p-3">
          <div className="flex h-8 items-center gap-2 px-1 group-data-[collapsible=icon]:hidden">
            <Terminal className="size-5" />
            <span className="font-semibold tracking-tight">agentuse</span>
            <Badge variant="secondary" className="ml-auto text-[10px]">
              Client
            </Badge>
          </div>
          <Popover open={switcher} onOpenChange={setSwitcher}>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                className="h-auto min-h-11 w-full justify-between px-2 text-left"
                aria-label="切换 endpoint"
              >
                <div className="min-w-0 group-data-[collapsible=icon]:hidden">
                  <div className="truncate text-sm font-medium">
                    {current?.name || "选择 Endpoint"}
                  </div>
                  <div className="truncate text-xs font-normal text-muted-foreground">
                    {local
                      ? "无需注册 · 调试本机 Harness"
                      : current?.server_url || "尚未注册平台"}
                  </div>
                </div>
                <ArrowLeftRight className="size-4 shrink-0" />
              </Button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-80 p-0">
              <Command>
                <CommandInput placeholder="搜索 endpoint…" />
                <CommandList>
                  <CommandEmpty>没有已注册的 endpoint</CommandEmpty>
                  <CommandGroup heading="Endpoints">
                    {items.map((e) => (
                      <CommandItem
                        key={e.endpoint_id}
                        value={e.name + " " + e.server_url}
                        onSelect={() => changeEndpoint(e.endpoint_id)}
                      >
                        <Monitor className="mr-2 size-4" />
                        <div className="min-w-0 flex-1">
                          <div>
                            {e.name || e.server_url}
                            {!e.enabled && " · 已停用"}
                          </div>
                          <div className="truncate text-xs text-muted-foreground">
                            {e.endpoint_id === "local"
                              ? "无需注册 · 调试本机 Harness"
                              : e.server_url}
                          </div>
                        </div>
                        {endpoint === e.endpoint_id && (
                          <Check className="size-4" />
                        )}
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>
          {local && (
            <form
              className="flex gap-1 group-data-[collapsible=icon]:hidden"
              onSubmit={(event) => {
                event.preventDefault();
                if (newTitle.trim()) createConversation.mutate(newTitle);
              }}
            >
              <Input
                aria-label="新建本地 Conversation"
                placeholder="输入名称，新建 Conversation"
                value={newTitle}
                maxLength={4096}
                onChange={(e) => setNewTitle(e.target.value)}
              />
              <Button
                size="icon"
                variant="outline"
                aria-label="创建本地 Conversation"
                disabled={!newTitle.trim() || createConversation.isPending}
              >
                <Plus className="size-4" />
              </Button>
            </form>
          )}
          {local && <ErrorNotice error={createConversation.error} />}
        </SidebarHeader>
        <SidebarContent>
          <SidebarGroup>
            <SidebarGroupLabel>Conversations</SidebarGroupLabel>
            <SidebarMenu>
              {conversations.isPending && current && (
                <li className="p-3 text-xs text-muted-foreground">加载中…</li>
              )}
              {conversationItems.map((c) => (
                <SidebarMenuItem key={c.id}>
                  <SidebarMenuButton
                    isActive={conversation === c.id}
                    onClick={() => {
                      setConversation(c.id);
                      setSession("");
                      setSelected(null);
                    }}
                    tooltip={c.title || c.id}
                  >
                    <FolderOpen className="size-4" />
                    <span className="truncate">{c.title || c.id}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
              {!conversations.isPending &&
                current &&
                !conversationItems.length && (
                  <li className="px-2 py-4 text-xs leading-relaxed text-muted-foreground">
                    {local
                      ? "输入任意名称，创建一个本地 Conversation 开始调试。"
                      : "暂无本机会话。Client 从此平台领取任务后会在这里显示。"}
                  </li>
                )}
              {conversations.hasNextPage && (
                <li>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={conversations.isFetchingNextPage}
                    onClick={() => void conversations.fetchNextPage()}
                  >
                    加载更多
                  </Button>
                </li>
              )}
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
        <SidebarFooter>
          <a
            href="/api/docs"
            target="_blank"
            rel="noreferrer"
            className="px-3 py-2 text-sm text-muted-foreground hover:text-foreground group-data-[collapsible=icon]:hidden"
          >
            API 文档
          </a>
          <Button
            variant="ghost"
            className="justify-start"
            onClick={() => setSettings(true)}
          >
            <Settings className="size-4" />
            <span className="group-data-[collapsible=icon]:hidden">
              本机设置
            </span>
          </Button>
          <div className="px-2 pb-2 text-xs text-muted-foreground group-data-[collapsible=icon]:hidden">
            {local ? "本地执行 · 文件存储" : "本机文件 · 只读"}
          </div>
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
          <SidebarTrigger />
          <Separator orientation="vertical" className="h-4" />
          <span className="truncate text-sm text-muted-foreground">
            {current?.name || "Client"}
          </span>
          {conversation && (
            <>
              <ChevronRight className="size-3 text-muted-foreground" />
              <span className="truncate text-sm">{currentTitle}</span>
            </>
          )}
          <Badge variant="outline" className="ml-auto shrink-0">
            {local ? "本地调试" : "只读"}
          </Badge>
        </header>
        <main
          id="main-content"
          tabIndex={-1}
          className="flex flex-1 flex-col gap-5 p-4 outline-none md:p-8"
        >
          <ErrorNotice
            error={
              endpoints.error ||
              conversations.error ||
              sessions.error ||
              runs.error
            }
          />
          {!current ? (
            <Empty
              title="连接你的平台"
              text="运行 agentuse register -server URL -token 安装令牌，注册后启动 client。"
            />
          ) : !conversation ? (
            <Empty
              title="选择一个 Conversation"
              text={
                local
                  ? "在左侧输入任意名称创建 Conversation，再选择本机 Harness 执行任务。"
                  : "从左侧选择会话，查看本机 Session 与执行记录。"
              }
            />
          ) : (
            <>
              <div>
                <div className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                  Conversation
                </div>
                <h1 className="break-all text-xl font-semibold tracking-tight">
                  {currentTitle}
                </h1>
                <p className="mt-2 text-sm text-muted-foreground">
                  原生 Session 各自保留上下文，共享此会话的 workspace。
                </p>
              </div>
              {local && (
                <LocalTaskPanel
                  conversation={conversation}
                  onStarted={async (run) => {
                    await client.invalidateQueries({
                      queryKey: ["sessions", "local", conversation],
                    });
                    await client.invalidateQueries({
                      queryKey: ["runs", "local", conversation],
                    });
                    if (
                      routeRef.current.endpoint === "local" &&
                      routeRef.current.conversation === conversation
                    )
                      setSession(run.session_id);
                  }}
                />
              )}
              <section aria-label="Sessions" className="space-y-3">
                <h2 className="text-sm font-medium">Sessions</h2>
                <div className="grid gap-3 lg:grid-cols-2">
                  {sessionItems.map((s) => (
                    <button
                      key={s.id}
                      onClick={() => {
                        setSession(s.id);
                        setSelected(null);
                      }}
                      aria-pressed={session === s.id}
                      className={`rounded-xl border p-4 text-left transition-colors hover:bg-accent/50 ${session === s.id ? "border-foreground/40 bg-accent/40" : "bg-background"}`}
                    >
                      <div className="flex items-center gap-2">
                        <Bot className="size-4" />
                        <strong className="min-w-0 flex-1 truncate text-sm">
                          @{s.agent_id}
                        </strong>
                        <Status value={s.status} />
                      </div>
                      <div className="mt-3 flex items-center justify-between gap-2 text-xs text-muted-foreground">
                        <span>{s.harness}</span>
                        <time>{date(s.last_used_at)}</time>
                      </div>
                      <div className="mt-2 truncate font-mono text-[11px] text-muted-foreground">
                        {s.id}
                      </div>
                    </button>
                  ))}
                </div>
                {sessions.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  !sessionItems.length && (
                    <p className="text-sm text-muted-foreground">
                      此 conversation 没有可查看的 Session。
                    </p>
                  )
                )}
                {sessions.hasNextPage && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void sessions.fetchNextPage()}
                  >
                    更多 Sessions
                  </Button>
                )}
              </section>
              {activeSession && (
                <section className="mt-2 space-y-3" aria-label="Runs">
                  <div className="flex items-center gap-2">
                    <h2 className="text-sm font-medium">Runs</h2>
                    <ChevronRight className="size-3 text-muted-foreground" />
                    <span className="text-xs text-muted-foreground">
                      @{activeSession.agent_id} · {activeSession.harness}
                    </span>
                  </div>
                  {(runs.data?.pages.flatMap((p) => p.items) ?? []).map((r) => (
                    <button
                      key={r.run_id}
                      onClick={() => setSelected(r)}
                      className="block w-full rounded-xl border bg-background p-4 text-left transition-colors hover:bg-accent/40"
                    >
                      <div className="flex flex-wrap items-center gap-3">
                        <Status value={r.status} />
                        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">
                          {r.run_id}
                        </span>
                        <time className="text-xs text-muted-foreground">
                          {date(r.received_at)}
                        </time>
                        <ChevronRight className="size-4 text-muted-foreground" />
                      </div>
                      <p className="mt-3 line-clamp-2 whitespace-pre-wrap break-words text-sm leading-relaxed">
                        {r.summary || "无文本摘要"}
                      </p>
                      <div className="mt-3 flex flex-wrap gap-4 text-xs text-muted-foreground">
                        <span>{Math.round(r.duration_ms / 1000)} 秒</span>
                        <span>
                          {r.usage
                            ? `${(r.usage.input_tokens + r.usage.output_tokens).toLocaleString()} tokens`
                            : "用量未知"}
                        </span>

                      </div>
                    </button>
                  ))}
                  {runs.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    !runs.data?.pages[0]?.items.length && (
                      <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
                        暂无执行记录。
                      </p>
                    )
                  )}
                  {runs.hasNextPage && (
                    <Button
                      variant="outline"
                      onClick={() => void runs.fetchNextPage()}
                    >
                      更多 Runs
                    </Button>
                  )}
                </section>
              )}
            </>
          )}
        </main>
      </SidebarInset>
      {selected && activeSession && (
        <RunDetail
          local={local}
          base={base + `/sessions/${enc(session)}`}
          run={selected}
          close={() => setSelected(null)}
        />
      )}
      <SettingsDialog open={settings} close={() => setSettings(false)} />
    </SidebarProvider>
  );
}
function LocalTaskPanel({
  conversation,
  onStarted,
}: {
  conversation: string;
  onStarted: (run: Run) => Promise<void>;
}) {
  const harnesses = useQuery({
    queryKey: ["local-harnesses"],
    queryFn: ({ signal }) =>
      api<
        Page<{
          name: string;
          found: boolean;
          disabled: boolean;
          revision?: string;
          version?: string;
        }>
      >("/api/local/harnesses", signal),
    refetchOnWindowFocus: false,
  });
  const [harness, setHarness] = useState("");
  const [prompt, setPrompt] = useState("");
  const [raw, setRaw] = useState(false);
  const [inputError, setInputError] = useState("");
  const available = harnesses.data?.items ?? [];
  useEffect(() => {
    if (!available.some((h) => h.name === harness && h.found && !h.disabled))
      setHarness(available.find((h) => h.found && !h.disabled)?.name ?? "");
  }, [harnesses.data, harness]);
  const start = useMutation({
    mutationFn: (body: unknown) =>
      post<Run>(`/api/local/conversations/${enc(conversation)}/runs`, body),
    onSuccess: async (run) => {
      await onStarted(run);
      setPrompt("");
    },
  });
  return (
    <section
      className="space-y-3 rounded-xl border p-4"
      aria-label="本地 Harness 调试"
    >
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">本地 Harness 调试</h2>

      </div>
      <ErrorNotice error={harnesses.error || start.error || inputError} />
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          setInputError("");
          let body: unknown = { harness, prompt };
          if (raw) {
            try {
              body = { harness, input: JSON.parse(prompt) };
            } catch {
              setInputError("请输入合法 JSON");
              return;
            }
          }
          start.mutate(body);
        }}
      >
        <label className="grid gap-2 text-xs text-muted-foreground">
          Harness
          <select
            aria-label="选择本地 Harness"
            className="h-9 rounded-md border bg-background px-3 text-sm text-foreground"
            value={harness}
            onChange={(e) => setHarness(e.target.value)}
          >
            <option value="" disabled>
              选择已安装的 Harness
            </option>
            {available.map((h) => (
              <option
                key={h.name}
                value={h.name}
                disabled={!h.found || h.disabled}
              >
                {h.name}
                {h.disabled ? " · 已禁用" : !h.found ? " · 程序未找到" : ""}
              </option>
            ))}
          </select>
        </label>
        <label className="grid gap-2 text-xs text-muted-foreground">
          任务输入
          <textarea
            aria-label="本地任务输入"
            className="min-h-28 w-full resize-y rounded-md border bg-background p-3 font-mono text-sm text-foreground"
            placeholder={
              raw
                ? '例如 {"prompt":"检查当前工作目录"}'
                : "输入任务，直接在本机运行所选 Harness"
            }
            maxLength={250000}
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
          />
        </label>
        <div className="flex items-center justify-between gap-3">
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={raw}
              onChange={(e) => setRaw(e.target.checked)}
            />
            JSON 输入
          </label>
          <Button disabled={!harness || !prompt.trim() || start.isPending}>
            <Send className="mr-2 size-4" />
            {start.isPending ? "正在启动…" : "运行 Harness"}
          </Button>
        </div>
      </form>
      <p className="text-xs text-muted-foreground">
        从 ~/.agentuse/harness/
        在启动时加载适配器。新增、修改或禁用适配器后需重启 client 生效。
      </p>
    </section>
  );
}
function SessionControls({
  base,
  run,
  refresh,
}: {
  base: string;
  run: Run;
  refresh: () => void;
}) {
  const [prompt, setPrompt] = useState("");
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const mutation = useMutation({
    mutationFn: ({ action, body }: { action: string; body: unknown }) =>
      post(
        `${base}/${action}`,
        body,
      ),
    onSuccess: () => {
      setPrompt("");
      refresh();
    },
  });
  const active = run.status === "running" || run.status === "waiting";
  if (!active) return null;
  const latest=[...(run.events??[])].reverse().find(event=>event.kind==="request");
  const question=latest?.payload as {request_id?:string;kind?:string;message?:string;questions?:{id:string;text:string;header?:string}[]}|undefined;
  const questions=question?.kind==="permission"?[{id:"permission",text:question.message??"权限请求"}]:(question?.questions??[]);
  return (
    <section className="my-5 space-y-3 rounded-lg border p-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">本地会话控制</h3>
        <Button
          variant="outline"
          size="sm"
          disabled={mutation.isPending}
          onClick={() => mutation.mutate({ action: "cancel", body: {} })}
        >
          取消会话执行
        </Button>
      </div>
      <ErrorNotice error={mutation.error} />
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          mutation.mutate({ action: "send", body: { prompt } });
        }}
      >
        <Input
          aria-label="追加输入"
          placeholder="追加输入到当前执行"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
        />
        <Button size="sm" disabled={!prompt.trim() || mutation.isPending}>
          发送
        </Button>
      </form>
      {run.status === "waiting" && question?.request_id && (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            mutation.mutate({
              action: "reply",
              body: { request_id: question.request_id, answer:question.kind==="permission"?answers.permission:answers },
            });
          }}
        >
          {questions.map((q) => (
            <label key={q.id} className="grid gap-1 text-xs">
              {q.text}
              <Input
                value={answers[q.id] ?? ""}
                onChange={(e) =>
                  setAnswers({ ...answers, [q.id]: e.target.value })
                }
              />
            </label>
          ))}
          <Button size="sm" disabled={mutation.isPending}>
            提交答案
          </Button>
        </form>
      )}
    </section>
  );
}

function Empty({ title, text }: { title: string; text: string }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center px-4 py-24 text-center">
      <Terminal className="mb-5 size-10 stroke-1 text-muted-foreground" />
      <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
      <p className="mt-3 max-w-lg text-sm leading-relaxed text-muted-foreground">
        {text}
      </p>
    </div>
  );
}
function RunDetail({
  base,
  run,
  close,
  local,
}: {
  base: string;
  run: Run;
  close: () => void;
  local: boolean;
}) {
  const q = useQuery({
    queryKey: ["run", base, run.run_id],
    queryFn: ({ signal }) =>
      api<Run>(
        `${base}/runs/${enc(run.run_id)}`,
        signal,
      ),
    refetchInterval: 2000,
  });
  const r = q.data ?? run;
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[85dvh] w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border bg-background p-6 shadow-lg">
          <div className="flex items-start justify-between gap-4">
            <div>
              <Dialog.Title className="font-semibold">
                @{r.agent_id} · Run details
              </Dialog.Title>
              <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                {r.harness} · {labels[r.status] ?? r.status}
              </Dialog.Description>
            </div>
            <Dialog.Close asChild>
              <Button variant="ghost" size="icon" aria-label="关闭详情">
                <X className="size-4" />
              </Button>
            </Dialog.Close>
          </div>
          <div className="mt-5">
            <ErrorNotice error={q.error} />
          </div>
          <p className="my-5 whitespace-pre-wrap break-words text-sm leading-relaxed">
            {r.summary}
          </p>
          <dl className="grid grid-cols-[90px_minmax(0,1fr)] gap-x-4 gap-y-3 text-xs">
            {[
              ["Run", r.run_id],
              ["Session", r.session_id],
              ["收到时间", date(r.received_at)],
              ["结束时间", date(r.finished_at)],
              ["耗时", `${r.duration_ms} ms`],
              [
                "输入 / 输出",
                r.usage
                  ? `${r.usage.input_tokens} / ${r.usage.output_tokens}`
                  : "未知",
              ],
            ].map(([k, v]) => (
              <React.Fragment key={k}>
                <dt className="text-muted-foreground">{k}</dt>
                <dd className="break-all">{v}</dd>
              </React.Fragment>
            ))}
          </dl>
          {local && (
            <SessionControls
              base={base.replace(
                "/api/conversations/local/",
                "/api/local/conversations/",
              )}
              run={r}
              refresh={() => void q.refetch()}
            />
          )}
          {r.error && (
            <pre className="mt-5 whitespace-pre-wrap break-words rounded-lg bg-destructive/5 p-3 text-xs text-destructive">
              {r.error}
            </pre>
          )}
          {r.output && (
            <section className="mt-6">
              <h3 className="mb-2 text-sm font-medium">结果</h3>
              <pre className="whitespace-pre-wrap break-words rounded-lg bg-muted/50 p-4 text-sm leading-relaxed">
                {r.output}
              </pre>
            </section>
          )}
          {!!r.events?.length && (
            <section className="mt-6 space-y-3">
              <h3 className="text-sm font-medium">执行事件</h3>
              {r.events.map((e, i) => (
                <div key={i} className="rounded-lg border p-3">
                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>{e.kind}</span>
                    <time>{date(e.at)}</time>
                  </div>
                  <pre className="mt-2 whitespace-pre-wrap break-words text-xs">
                    {JSON.stringify(e.payload, null, 2)}
                  </pre>
                </div>
              ))}
            </section>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
function SettingsDialog({ open, close }: { open: boolean; close: () => void }) {
  const q = useQuery({
    queryKey: ["settings"],
    queryFn: ({ signal }) => api<unknown>("/api/settings", signal),
    enabled: open,
  });
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(v) => {
        if (!v) close();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-xl -translate-x-1/2 -translate-y-1/2 rounded-xl border bg-background p-6 shadow-lg">
          <Dialog.Title className="font-semibold">本机设置</Dialog.Title>
          <Dialog.Description className="mt-2 text-sm text-muted-foreground">
            当前进程启动时加载的配置。修改配置文件后需重启 client 生效。
          </Dialog.Description>
          <ErrorNotice error={q.error} />
          <pre className="my-5 overflow-auto rounded-lg bg-muted p-4 text-xs">
            {JSON.stringify(q.data, null, 2)}
          </pre>
          <Dialog.Close asChild>
            <Button variant="outline">关闭</Button>
          </Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={client}>
    <App />
  </QueryClientProvider>,
);
