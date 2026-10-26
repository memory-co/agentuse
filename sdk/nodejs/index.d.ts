export declare const USER_HEADER: "X-Agentuse-User";

export declare class AgentuseError extends Error {
  status: number;
  constructor(status: number, message: string);
}

export interface User { id: string; name: string; role: "admin" | "user"; created_at: number; }
export interface UserToken { user: User; token: string; login_url: string; }

export interface Harness {
  name: string;
  kind: "builtin" | "custom";
  display_name: string;
  description: string;
  owner_id: string;
  created_at: number;
}

export interface Agent {
  id: string;
  user_id: string;
  name: string;
  daemon_id?: string;
  harness: string;
  description: string;
  created_at: number;
  updated_at: number;
}

export interface DaemonHarness {
  daemon_id: string;
  user_id: string;
  harness: string;
  version?: string;
  available: boolean;
  enabled: boolean;
  updated_at: number;
}

export interface Daemon {
  id: string;
  user_id: string;
  name: string;
  status: "pending" | "online" | "offline";
  last_seen_at: number | null;
  created_at: number;
  harnesses: DaemonHarness[];
  setup_token?: string;
  setup_command?: string;
}

export interface Session {
  id: string;
  user_id: string;
  created_by: string;
  agent_id?: string;
  harness: string;
  daemon_id?: string;
  harness_session_id?: string;
  title: string;
  status: "idle" | "pending" | "running";
  lease_until?: number;
  created_at: number;
  updated_at: number;
}

export interface Message {
  id: number;
  session_id: string;
  role: "user" | "assistant";
  content: any;
  created_at: number;
}

export interface SessionEvent {
  id: number;
  session_id: string;
  message_id: number;
  daemon_id: string;
  kind: string;
  payload: any;
  created_at: number;
}

export interface SessionDetail { session: Session; messages: Message[]; events: SessionEvent[]; }

// 网站的 user id;null 表示 admin 本身。
export type UserId = string | null;

export interface MessageInput { prompt?: string; input?: any; }

export interface ClientOptions {
  baseUrl: string;
  token: string;
  fetch?: typeof fetch;
}

export declare class Client {
  baseUrl: string;
  token: string;
  constructor(opts: ClientOptions);

  // 除了用户管理，第一个参数都是网站的 user id;传 null 就是 admin 本身。
  me(user: UserId): Promise<User>;
  listUsers(): Promise<User[]>;
  createUser(user?: { id?: string; name?: string; role?: "admin" | "user" }): Promise<UserToken>;
  resetUserToken(userId: string): Promise<UserToken>;

  listHarnesses(user: UserId): Promise<Harness[]>;
  createHarness(user: UserId, harness: { name: string; display_name?: string; description?: string }): Promise<Harness>;
  deleteHarness(user: UserId, name: string): Promise<void>;

  listAgents(user: UserId): Promise<Agent[]>;
  createAgent(user: UserId, agent: { id?: string; name: string; daemon_id: string; harness: string; description?: string }): Promise<Agent>;
  getAgent(user: UserId, agentId: string): Promise<Agent>;
  updateAgent(user: UserId, agentId: string, patch: { name?: string; daemon_id?: string; harness?: string; description?: string }): Promise<Agent>;
  deleteAgent(user: UserId, agentId: string): Promise<void>;

  listDaemons(user: UserId): Promise<Daemon[]>;
  createDaemon(user: UserId, name?: string): Promise<Daemon>;
  resetDaemon(user: UserId, daemonId: string): Promise<Daemon>;
  setDaemonHarness(user: UserId, daemonId: string, harness: string, enabled: boolean): Promise<Daemon>;
  deleteDaemon(user: UserId, daemonId: string): Promise<void>;

  listSessions(user: UserId): Promise<Session[]>;
  createSession(user: UserId, req: MessageInput & { agent_id: string }): Promise<{ session: Session; message: Message }>;
  sendMessage(user: UserId, sessionId: string, req: MessageInput): Promise<Message>;
  getSession(user: UserId, sessionId: string): Promise<SessionDetail>;
  deleteSession(user: UserId, sessionId: string): Promise<void>;
  waitSession(user: UserId, sessionId: string, opts?: { interval?: number; timeout?: number }): Promise<SessionDetail>;
}
