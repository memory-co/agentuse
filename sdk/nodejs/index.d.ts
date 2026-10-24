export declare const USER_HEADER: "X-Agentuse-User";

export declare class AgentuseError extends Error {
  status: number;
  constructor(status: number, message: string);
}

export interface User { id: string; name: string; role: "admin" | "user"; created_at: number; }
export interface UserToken { user: User; token: string; login_url: string; }

export interface Agent {
  name: string;
  kind: "builtin" | "custom";
  display_name: string;
  description: string;
  owner_id: string;
  created_at: number;
}

export interface DaemonAgent {
  daemon_id: string;
  user_id: string;
  agent: string;
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
  agents: DaemonAgent[];
  setup_token?: string;
  setup_command?: string;
}

export interface Session {
  id: string;
  user_id: string;
  agent: string;
  daemon_id?: string;
  cwd?: string;
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

export interface MessageInput { prompt?: string; input?: any; }

export interface ClientOptions {
  baseUrl: string;
  token: string;
  user?: string;
  fetch?: typeof fetch;
}

export declare class Client {
  baseUrl: string;
  token: string;
  user?: string;
  constructor(opts: ClientOptions);
  asUser(userId: string): Client;

  me(): Promise<User>;
  listUsers(): Promise<User[]>;
  createUser(user?: { id?: string; name?: string; role?: "admin" | "user" }): Promise<UserToken>;
  resetUserToken(userId: string): Promise<UserToken>;

  listAgents(): Promise<Agent[]>;
  createAgent(agent: { name: string; display_name?: string; description?: string }): Promise<Agent>;
  deleteAgent(name: string): Promise<void>;

  listDaemons(): Promise<Daemon[]>;
  createDaemon(name?: string): Promise<Daemon>;
  resetDaemon(daemonId: string): Promise<Daemon>;
  setDaemonAgent(daemonId: string, agent: string, enabled: boolean): Promise<Daemon>;
  deleteDaemon(daemonId: string): Promise<void>;

  listSessions(): Promise<Session[]>;
  createSession(req: MessageInput & { agent: string; cwd?: string }): Promise<{ session: Session; message: Message }>;
  sendMessage(sessionId: string, req: MessageInput): Promise<Message>;
  getSession(sessionId: string): Promise<SessionDetail>;
  deleteSession(sessionId: string): Promise<void>;
  waitSession(sessionId: string, opts?: { interval?: number; timeout?: number }): Promise<SessionDetail>;
}
