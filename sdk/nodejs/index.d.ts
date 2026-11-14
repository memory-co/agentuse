export declare const USER_HEADER: "X-Agentuse-User";

export declare class AgentuseError extends Error {
  status: number;
  constructor(status: number, message: string);
}

export interface User { id: string; name: string; role: "admin" | "user"; created_at: number; }
export interface UserToken { user: User; token: string; login_url: string; }

export interface Harness {
  name: string;
  kind: "reported";
  display_name: string;
  description: string;
  owner_id: string;
  created_at: number;
}

export interface Agent {
 current_version_id:string;version:number;model?:string;instructions:string;skills:SkillRef[];
  id: string;
  user_id: string;
  name: string;
  device_id?: string;
  harness: string;
  description: string;
  created_at: number;
  updated_at: number;
}

export interface DeviceHarness {
  harness_revision: string;
  capabilities: string[];
  device_id: string;
  user_id: string;
  harness: string;
  version?: string;
  available: boolean;
  enabled: boolean;
  updated_at: number;
}

export interface Device {
 capabilities: string[];
  id: string;
  user_id: string;
  name: string;
  status: "pending" | "online" | "offline";
  last_seen_at: number | null;
  created_at: number;
  harnesses: DeviceHarness[];
  setup_token?: string;
  setup_command?: string;
}

export interface Conversation {
  id: string;
  user_id: string;
  created_by: string;
  mode: "single" | "collaborative";
  parent_conversation_id?: string;
  parent_agent_id?: string;
  title: string;
  status: "idle" | "pending" | "running" | "waiting" | "resolved";
  created_at: number;
  updated_at: number;
}

export interface Message {
 author_conversation_id?:string; author_round_seq?:number;
 attachments: Attachment[];
 author_agent_version_id?:string;
 seq?:number;author_agent_id?:string;author_name?:string;author_user_id?:string;reply_to_message_id?:string;to_agent_ids:string[];to_user_ids:string[];
  id: string;
  conversation_id: string;
  author_role: "user" | "agent" | "child_conversation";
  content: string;
  created_at: number;
}

export interface ConversationEvent {
 run_id?:string;session_id:string;agent_id:string;event_seq:number;
  id: number;
  conversation_id: string;
  device_id: string;
  kind: string;
  payload: any;
  created_at: number;
}

export interface Question {delivery_error?:string;request_id:string;conversation_id:string;run_id:string;state:"pending"|"replied"|"expired";payload:unknown;answer?:unknown;answered_by?:string;created_at:number}
export interface Run {
 agent_version_id:string;id:string;conversation_id:string;first_message_id:string;agent_id:string;device_id:string;session_id:string;harness:string;harness_revision:string;status:"running"|"waiting"|"completed"|"failed"|"cancelled";result_message_id?:string;output?:string;error?:string;result?:unknown;created_at:number;updated_at:number}
export interface Delivery {
 agent_version_id:string;message_id:string;conversation_id:string;agent_id:string;device_id:string;harness:string;content:string;run_id?:string;input_id?:string;session_id?:string;status:string;error?:string}
export interface RunDetail { run:Run;events:ConversationEvent[] }
export interface Reply {round?:ConversationRound; message:Message;runs:Run[];deliveries:Delivery[] }
export interface WaitOptions { interval?:number;timeout?:number;returnOnWaiting?:boolean }
export interface ConversationRound {conversation_id:string;seq:number;start_message_seq:number;end_message_seq?:number;created_at:number;finished_at?:number;parent_delivery?:"pending"|"delivered"|"failed";parent_message_id?:string;parent_delivery_error?:string}
export interface ConversationDetail {rounds:ConversationRound[]; runs?:Run[];deliveries?:Delivery[];run_counts?:Record<string,number>; conversation: Conversation; messages: Message[]; questions: Question[]; events: ConversationEvent[]; }

// 网站的 user id;null 表示 admin 本身。
export type UserId = string | null;

export interface MessageInput { reply_to_message_id?:string; attachment_ids?:string[]; prompt?: string; to_agent_ids?:string[]; to_user_ids?:string[]; }

export interface ConversationSource {conversation_id:string;agent_id:string;session_id:string;run_id:string}
export interface ClientOptions {
 source?: ConversationSource;
  baseUrl: string;
  token: string;
  fetch?: typeof fetch;
}

export interface Attachment { relative_path:string;
 created_by_run_id?:string;created_by_agent_id?:string;
 id: string; conversation_id: string; filename: string; content_type: string; size_bytes: number; sha256: string;
 created_by: string; source: 'user' | 'device'; device_id?: string; created_at: number; download_path: string;
}
export interface AttachmentPage { attachments: Attachment[]; next_cursor: string | null }
export declare class Client {
 getRun(user:UserId,conversation:string,run:string):Promise<RunDetail>;
 cancelRun(user:UserId,conversation:string,run:string):Promise<Run>;
 replyRunRequest(user:UserId,conversation:string,run:string,request:string,answer:unknown):Promise<void>;
 waitRun(user:UserId,conversation:string,run:string,opts?:WaitOptions):Promise<RunDetail>;
 waitMessageRuns(user:UserId,conversation:string,message:string,opts?:WaitOptions):Promise<ConversationDetail>;

 uploadAttachment(user: UserId, conversationId: string, filename: string, stream: AsyncIterable<Uint8Array> | ReadableStream<Uint8Array> | Blob): Promise<Attachment>;
 listAttachments(user: UserId, conversationId: string, opts?: {limit?: number; cursor?: string}): Promise<AttachmentPage>;
 getAttachment(user: UserId, conversationId: string, id: string): Promise<Attachment>;
 downloadAttachment(user: UserId, conversationId: string, id: string): Promise<Response>;
 deleteAttachment(user: UserId, conversationId: string, id: string): Promise<void>;

  baseUrl: string;
  token: string;
  source?: ConversationSource;
  constructor(opts: ClientOptions);

  // 除了用户管理，第一个参数都是网站的 user id;传 null 就是 admin 本身。
  me(user: UserId): Promise<User>;
  listUsers(): Promise<User[]>;
  createUser(user?: { id?: string; name?: string; role?: "admin" | "user" }): Promise<UserToken>;
  resetUserToken(userId: string): Promise<UserToken>;

  listHarnesses(user: UserId): Promise<Harness[]>;

  listAgents(user: UserId): Promise<Agent[]>;
  createAgent(user: UserId, agent: { id?: string; name: string; device_id: string; harness: string; description?: string;model?:string;instructions?:string;skills?:SkillRef[] }): Promise<Agent>;
  getAgent(user: UserId, agentId: string): Promise<Agent>;
  publishAgentVersion(user:UserId,agentId:string,config:AgentConfig & {base_version_id:string}):Promise<AgentVersion>;
  listAgentVersions(user:UserId,agentId:string):Promise<AgentVersion[]>;
  getAgentVersion(user:UserId,agentId:string,versionId:string):Promise<AgentVersion>;
  listSkills(user:UserId):Promise<Skill[]>;
  getSkill(user:UserId,skillId:string):Promise<Skill>;
  listSkillVersions(user:UserId,skillId:string):Promise<SkillVersion[]>;
  getSkillVersion(user:UserId,skillId:string,versionId:string):Promise<SkillVersion>;
  publishSkill(user:UserId,skillId:string|null,baseVersionId:string|null,stream:AsyncIterable<Uint8Array>|Blob):Promise<Skill>;
  downloadSkillPackage(user:UserId,skillId:string,versionId:string):Promise<Response>;
  deleteSkill(user:UserId,skillId:string):Promise<void>;
  deleteAgent(user: UserId, agentId: string): Promise<void>;

  listDevices(user: UserId): Promise<Device[]>;
  createDevice(user: UserId, name?: string): Promise<Device>;
  resetDevice(user: UserId, deviceId: string): Promise<Device>;
  setDeviceHarness(user: UserId, deviceId: string, harness: string, enabled: boolean): Promise<Device>;
  deleteDevice(user: UserId, deviceId: string): Promise<void>;

  listConversations(user: UserId): Promise<Conversation[]>;
  createConversation(user: UserId, req: Omit<MessageInput,"attachment_ids"> & { title?:string; mode?:"single"|"collaborative";parent_conversation_id?:string; }): Promise<{ conversation: Conversation; message?: Message; round?:ConversationRound; runs:Run[];deliveries:Delivery[] }>;
  /** 返回共享消息和各目标的 runs/deliveries。 */
  sendMessage(user: UserId, conversationId: string, req: MessageInput): Promise<Reply>;
  getConversation(user: UserId, conversationId: string): Promise<ConversationDetail>;
  getConversationRound(user:UserId,conversationId:string,round:number):Promise<ConversationDetail>;
  /** 回答 harness 的提问(会话 waiting 时)。answers 是 { 问题: 答案 }。 */
  deleteConversation(user: UserId, conversationId: string): Promise<void>;
  resolveConversation(user: UserId, conversationId: string): Promise<Conversation>;
  reopenConversation(user: UserId, conversationId: string): Promise<Conversation>;
  listChildren(user:UserId,parentId:string):Promise<Conversation[]>;
  deleteConversationTree(user:UserId,conversationId:string):Promise<void>;
  waitConversation(user: UserId, conversationId: string, opts?: WaitOptions): Promise<ConversationDetail>;
}

export interface SkillRef {skill_id:string;skill_version_id:string}
export interface AgentConfig {name:string;description:string;device_id:string;harness:string;model?:string;instructions:string;skills:SkillRef[]}
export interface AgentVersion extends AgentConfig {id:string;agent_id:string;version:number;created_at:number}
export interface SkillVersion {id:string;skill_id:string;version:number;name:string;description:string;sha256:string;size_bytes:number;download_path:string;created_at:number}
export interface Skill {id:string;user_id:string;current_version_id:string;current_version:SkillVersion;created_at:number;updated_at:number}
