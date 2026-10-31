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
 capabilities: string[];
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
 mode?: "multi_agent";
  id: string;
  user_id: string;
  created_by: string;
  work_id?: string;
  work_role?: "coordinator" | "thread";
  harness?: string;
  title: string;
  status: "idle" | "pending" | "running" | "waiting" | "resolved";
  created_at: number;
  updated_at: number;
}

export interface Message {
 seq?:number;run_id?:string;agent_key?:string;agent_name?:string;target_agent_ids?:string[];
  id: number;
  session_id: string;
  role: "user" | "assistant";
  content: any;
  created_at: number;
}

export interface Work {
 coordinator_agent_id?:string;
  id: string;
  user_id: string;
  name: string;
  instructions: string;
  coordinator_id: string;
  thread_count: number;
  created_at: number;
  updated_at: number;
}

export interface SessionEvent {
 run_id?:string;attempt_id?:string;
  id: number;
  session_id: string;
  message_id: number;
  daemon_id: string;
  kind: string;
  payload: any;
  created_at: number;
}

export interface Question {
 run_id?:string;attempt_id?:string;
  id: number;
  session_id: string;
  message_id: number;
  tool_use_id: string;
  questions: { question: string; header?: string; multi_select?: boolean; options?: { label: string; description?: string }[] }[];
  answers?: Record<string, string>;
  answered_by?: string;
  status: "pending" | "answered" | "expired";
  created_at: number;
}

export interface Run { id:string;session_id:string;message_id:number;agent_id?:string;agent_key:string;agent_name:string;daemon_id?:string;harness:string;status:"pending"|"running"|"waiting"|"succeeded"|"failed"|"cancelled";attempt_id?:string;context_seq:number;result_message_id?:number;error?:string }
export interface Delivery { id:number;message_id:number;agent_key:string;run_id:string;kind:"new_run"|"steer";status:string;error?:string }
export interface RunDetail { run:Run;events:SessionEvent[] }
export interface MultiReply { message:Message;runs:Run[];deliveries:Delivery[] }
export interface WaitOptions { interval?:number;timeout?:number;returnOnWaiting?:boolean }
export interface SessionDetail { runs?:Run[];deliveries?:Delivery[];run_counts?:Record<string,number>; session: Session; messages: Message[]; questions: Question[]; events: SessionEvent[]; }

// 网站的 user id;null 表示 admin 本身。
export type UserId = string | null;

export interface MessageInput { prompt?: string; input?: any; target_agent_ids?:string[]; }

export interface ClientOptions {
  baseUrl: string;
  token: string;
  fetch?: typeof fetch;
}

export interface Attachment {
 created_by_run_id?:string;created_by_agent_key?:string;
 id: string; session_id: string; filename: string; content_type: string; size_bytes: number; sha256: string;
 created_by: string; source: 'user' | 'daemon' | 'work'; daemon_id?: string; created_at: number; download_path: string;
}
export interface AttachmentPage { attachments: Attachment[]; next_cursor: string | null }
export declare class Client {
 getRun(user:UserId,session:string,run:string):Promise<RunDetail>;
 cancelRun(user:UserId,session:string,run:string):Promise<Run>;
 answerRunQuestion(user:UserId,session:string,run:string,question:number,answers:Record<string,string>):Promise<Question>;
 waitRun(user:UserId,session:string,run:string,opts?:WaitOptions):Promise<RunDetail>;
 waitMessageRuns(user:UserId,session:string,message:number,opts?:WaitOptions):Promise<SessionDetail>;

 uploadAttachment(user: UserId, sessionId: string, filename: string, stream: AsyncIterable<Uint8Array> | ReadableStream<Uint8Array> | Blob): Promise<Attachment>;
 listAttachments(user: UserId, sessionId: string, opts?: {limit?: number; cursor?: string}): Promise<AttachmentPage>;
 getAttachment(user: UserId, sessionId: string, id: string): Promise<Attachment>;
 downloadAttachment(user: UserId, sessionId: string, id: string): Promise<Response>;
 deleteAttachment(user: UserId, sessionId: string, id: string): Promise<void>;

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
  createSession(user: UserId, req: MessageInput & { mode?:"multi_agent"; title?:string; defer_start?: boolean }): Promise<{ session: Session; message?: Message; runs?:Run[];deliveries?:Delivery[] }>;
  /** 返回共享消息和各目标的 runs/deliveries。 */
  sendMessage(user: UserId, sessionId: string, req: MessageInput, opts?:{idempotencyKey?:string}): Promise<MultiReply>;
  getSession(user: UserId, sessionId: string): Promise<SessionDetail>;
  /** 回答 harness 的提问(会话 waiting 时)。answers 是 { 问题: 答案 }。 */
  deleteSession(user: UserId, sessionId: string): Promise<void>;
  resolveSession(user: UserId, sessionId: string): Promise<Session>;
  reopenSession(user: UserId, sessionId: string): Promise<Session>;
  listWorks(user: UserId): Promise<Work[]>;
  createWork(user: UserId, req: { name: string; agent_id: string; instructions?: string; mode?:"multi_agent" }): Promise<{ work: Work; coordinator: Session }>;
  getWork(user: UserId, workId: string): Promise<Work>;
  updateWork(user: UserId, workId: string, patch: { name?: string; instructions?: string;coordinator_agent_id?:string }): Promise<Work>;
  deleteWork(user: UserId, workId: string): Promise<void>;
  listWorkSessions(user: UserId, workId: string): Promise<Session[]>;
  waitSession(user: UserId, sessionId: string, opts?: WaitOptions): Promise<SessionDetail>;
}
