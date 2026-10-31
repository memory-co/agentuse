// 和 docs/v2/apis 里的返回结构对应。时间都是 unix 秒。
export type Role = 'admin' | 'user';
export interface User { id: string; name: string; role: Role; created_at: number }

export interface Harness { name: string; kind: 'builtin' | 'custom'; display_name: string; description: string; owner_id?: string; created_at: number }

export interface Agent { id: string; user_id: string; name: string; daemon_id?: string; harness: string; description: string; created_at: number; updated_at: number }

export interface DaemonHarness { harness: string; version: string; available: boolean; enabled: boolean; updated_at: number }
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
export interface Message { seq?:number; agent_key?:string;agent_name?:string;run_id?:string;target_agent_ids?:string[]; id: number; session_id: string; role: 'user' | 'assistant'; content: any; created_at: number }
export interface SessionEvent { run_id?:string;attempt_id?:string; id: number; session_id: string; message_id: number; daemon_id: string; kind: string; payload?: any; created_at: number }
export interface QuestionItem { question: string; header?: string; multi_select?: boolean; options?: { label: string; description?: string }[] }
export interface Question {
 run_id?:string;attempt_id?:string;
  id: number; session_id: string; message_id: number; tool_use_id: string; questions: QuestionItem[];
  answers?: Record<string, string>; answered_by?: string; status: 'pending' | 'answered' | 'expired'; created_at: number;
}
export interface SessionDetail { runs?:Run[];deliveries?:Delivery[];run_counts?:Record<string,number>; session: Session; messages: Message[]; questions?: Question[]; events: SessionEvent[]; daemon_offline?: boolean }

export interface Attachment {
 created_by_run_id?: string;created_by_agent_key?:string;
 id: string; session_id: string; filename: string; content_type: string; size_bytes: number; sha256: string;
 created_by: string; source: 'user' | 'daemon' | 'work'; daemon_id?: string; created_at: number; download_path: string;
}

export interface Run { id:string;session_id:string;message_id:number;agent_id?:string;agent_key:string;agent_name:string;daemon_id?:string;harness:string;status:'pending'|'running'|'waiting'|'succeeded'|'failed'|'cancelled';attempt_id?:string;context_seq:number;error?:string;result_message_id?:number }
export interface Delivery { id:number;message_id:number;agent_key:string;run_id:string;kind:'new_run'|'steer';status:'pending'|'delivered'|'succeeded'|'failed'|'cancelled';error?:string }
