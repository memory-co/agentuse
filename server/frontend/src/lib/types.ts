// 和 docs/v2/apis 里的返回结构对应。时间都是 unix 秒。
export type Role = 'admin' | 'user';
export interface User { id: string; name: string; role: Role; created_at: number }

export interface Harness { name: string; kind: 'reported'; display_name: string; description: string; owner_id?: string; created_at: number }

export interface Agent { current_version_id:string;version:number;model?:string;instructions:string;skills:SkillRef[]; id: string; user_id: string; name: string; daemon_id?: string; harness: string; description: string; created_at: number; updated_at: number }

export interface DaemonHarness { harness: string; version: string; revision: string; available: boolean; enabled: boolean; updated_at: number }
export interface Daemon {
  id: string; user_id: string; name: string; status: 'pending' | 'online' | 'offline';
  capabilities: string[]; setup_command?: string; last_seen_at: number | null; created_at: number; harnesses: DaemonHarness[];
}

export type SessionStatus = 'idle' | 'pending' | 'running' | 'waiting' | 'resolved';
export interface Session {
 mode?: "multi_agent";
  id: string; user_id: string; created_by: string; work_id?: string; work_role?: 'coordinator' | 'thread';
  title: string; status: SessionStatus; created_at: number; updated_at: number;
}
// 工作：协调者 + 线程 + 共用说明(docs/v2/designs/work.md)
export interface Work { coordinator_agent_id?: string; id: string; user_id: string; name: string; instructions: string; coordinator_id: string; thread_count: number; created_at: number; updated_at: number }
export interface Message {author_agent_version_id?:string; seq?:number; agent_key?:string;agent_name?:string;run_id?:string;target_agent_ids?:string[]; id: number; session_id: string; role: 'user' | 'assistant'; content: any; created_at: number }
export interface SessionEvent { run_id?:string;client_session_id:string;agent_id:string;event_seq:number; id: number; session_id: string; daemon_id: string; kind: string; payload?: any; created_at: number }
export interface Question {request_id:string;run_id:string;session_id:string;state:'pending'|'replied'|'expired';payload:any;answer?:unknown;answered_by?:string;created_at:number;delivery_error?:string}
export interface SessionDetail { runs?:Run[];deliveries?:Delivery[];run_counts?:Record<string,number>; session: Session; messages: Message[]; questions?: Question[]; events: SessionEvent[]; daemon_offline?: boolean }

export interface Attachment {
 created_by_run_id?: string;created_by_agent_key?:string;
 id: string; session_id: string; filename: string; content_type: string; size_bytes: number; sha256: string;
 created_by: string; source: 'user' | 'daemon' | 'work'; daemon_id?: string; created_at: number; download_path: string;
}

export interface Run {agent_version_id:string;id:string;session_id:string;first_message_id:number;agent_id:string;daemon_id:string;client_session_id:string;harness:string;harness_revision:string;status:'running'|'waiting'|'completed'|'failed'|'cancelled';error?:string;output?:string;result?:any;result_message_id?:number}
export interface Delivery {agent_version_id:string;id:number;message_id:number;session_id:string;agent_id:string;daemon_id:string;run_id?:string;input_id?:string;client_session_id?:string;harness:string;content:any;status:'pending'|'sent'|'accepted'|'completed'|'failed'|'cancelled';error?:string}

export interface SkillRef {skill_id:string;skill_version_id:string}
export interface SkillVersion {id:string;skill_id:string;version:number;name:string;description:string;sha256:string;size_bytes:number;download_path:string;created_at:number}
export interface Skill {id:string;user_id:string;current_version_id:string;current_version:SkillVersion}
export interface AgentVersion {id:string;agent_id:string;version:number;name:string;description:string;daemon_id:string;harness:string;model?:string;instructions:string;skills:SkillRef[];created_at:number}
