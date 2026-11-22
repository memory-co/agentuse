// 和 docs/server/conversations.md 里的返回结构对应。时间都是 unix 秒。
export type Role = 'admin' | 'user';
export interface User { id: string; name: string; role: Role; created_at: number }

export interface Harness { name: string; kind: 'reported'; display_name: string; description: string; owner_id?: string; created_at: number }

export interface Agent { current_version_id:string;version:number;model?:string;instructions:string;skills:SkillRef[]; id: string; user_id: string; name: string; device_id?: string; harness: string; description: string; created_at: number; updated_at: number }

export interface DeviceHarness { harness: string; version: string; harness_revision: string; capabilities: string[]; available: boolean; enabled: boolean; updated_at: number }
export interface Device {
  id: string; user_id: string; name: string; status: 'pending' | 'online' | 'offline';
  capabilities: string[]; setup_command?: string; setup_token?: string; last_seen_at: number | null; created_at: number; harnesses: DeviceHarness[];
  client_version?: string; client_upgrade?: ClientUpgrade;
}
export interface ClientUpgrade {
  target_version: string; status: 'downloading' | 'restarting' | 'succeeded' | 'failed'; error?: string;
}

export type ConversationStatus = 'idle' | 'pending' | 'running' | 'waiting' | 'resolved';
export interface Conversation {
  id: string; user_id: string; created_by: string; mode: 'single' | 'collaborative'; parent_conversation_id?: string; parent_agent_id?: string;
  title: string; status: ConversationStatus; created_at: number; updated_at: number;
}
export interface Message {agent_errors?:Record<string,MessageError>;author_conversation_id?:string;author_round_seq?:number;attachments:Attachment[];author_user_id?:string;reply_to_message_id?:string;author_agent_version_id?:string; seq?:number; author_agent_id?:string;author_name?:string;to_agent_ids:string[];to_user_ids:string[]; id: string; conversation_id: string; author_role: 'user' | 'agent' | 'child_conversation'; content: string; created_at: number }
export interface RunEventPreview { id: string; kind: string; created_at: number; payload?: unknown; payload_preview?: string }
export interface RunEventPage { events: RunEventPreview[]; next_cursor: string | null }
export interface Question {request_id:string;run_id:string;conversation_id:string;state:'pending'|'replied'|'expired';payload:any;answer?:unknown;answered_by?:string;created_at:number;delivery_error?:string}
export interface ConversationRound {conversation_id:string;seq:number;start_message_seq:number;end_message_seq?:number;created_at:number;finished_at?:number;parent_delivery?:'pending'|'delivered'|'failed';parent_message_id?:string;parent_delivery_error?:string}
export interface ConversationSummary {rounds:ConversationRound[]; runs:Run[]; conversation: Conversation; messages: Message[] }

export interface Attachment {
 relative_path:string;
 created_by_run_id?: string;created_by_agent_id?:string;
 id: string; conversation_id: string; filename: string; content_type: string; size_bytes: number; sha256: string;
 created_by: string; source: 'user' | 'device'; device_id?: string; created_at: number; download_path: string;
}

export interface RunUsage {input_tokens:number|null;output_tokens:number|null;cache_read_tokens:number|null;cache_write_tokens:number|null}
export interface Run {agent_version_id:string;id:string;conversation_id:string;first_message_id:string;agent_id:string;device_id:string;session_id:string;harness:string;harness_revision:string;model:string|null;usage:RunUsage|null;status:'running'|'waiting'|'completed'|'failed'|'cancelled';error?:string;output?:string;result?:any;result_message_id?:string}
export interface MessageError {error:string;uncertain?:boolean}
export interface RunInput {run_id:string;input_id:string;message_id:string;created_at:number}
export interface RunDetail { run: Run; inputs: RunInput[]; questions?: Question[] }

export interface SkillRef {skill_id:string;skill_version_id:string}
export interface SkillVersion {id:string;skill_id:string;version:number;name:string;description:string;sha256:string;size_bytes:number;download_path:string;created_at:number}
export interface Skill {id:string;user_id:string;current_version_id:string;current_version:SkillVersion}
export interface AgentVersion {id:string;agent_id:string;version:number;name:string;description:string;device_id:string;harness:string;model?:string;instructions:string;skills:SkillRef[];created_at:number}
