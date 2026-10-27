// 和 docs/v2/apis 里的返回结构对应。时间都是 unix 秒。
export type Role = 'admin' | 'user';
export interface User { id: string; name: string; role: Role; created_at: number }

export interface Harness { name: string; kind: 'builtin' | 'custom'; display_name: string; description: string; owner_id?: string; created_at: number }

export interface Agent { id: string; user_id: string; name: string; daemon_id?: string; harness: string; description: string; created_at: number; updated_at: number }

export interface DaemonHarness { harness: string; version: string; available: boolean; enabled: boolean; updated_at: number }
export interface Daemon {
  id: string; user_id: string; name: string; status: 'pending' | 'online' | 'offline';
  setup_command?: string; last_seen_at: number | null; created_at: number; harnesses: DaemonHarness[];
}

export type SessionStatus = 'idle' | 'pending' | 'running' | 'waiting' | 'resolved';
export interface Session {
  id: string; user_id: string; created_by: string; agent_id?: string; work_id?: string; work_role?: 'coordinator' | 'thread';
  harness: string; daemon_id?: string; harness_session_id?: string;
  title: string; status: SessionStatus; created_at: number; updated_at: number;
}
// 工作：协调者 + 线程 + 共用说明(docs/v2/designs/work.md)
export interface Work { id: string; user_id: string; name: string; instructions: string; coordinator_id: string; thread_count: number; created_at: number; updated_at: number }
export interface Message { id: number; session_id: string; role: 'user' | 'assistant'; content: any; created_at: number }
export interface SessionEvent { id: number; session_id: string; message_id: number; daemon_id: string; kind: string; payload?: any; created_at: number }
export interface Steer { id: number; session_id: string; message_id: number; created_by: string; prompt: string; status: 'pending' | 'delivered' | 'queued'; created_at: number }
export interface QuestionItem { question: string; header?: string; multi_select?: boolean; options?: { label: string; description?: string }[] }
export interface Question {
  id: number; session_id: string; message_id: number; tool_use_id: string; questions: QuestionItem[];
  answers?: Record<string, string>; answered_by?: string; status: 'pending' | 'answered' | 'expired'; created_at: number;
}
export interface SessionDetail { session: Session; messages: Message[]; steers: Steer[]; questions?: Question[]; events: SessionEvent[]; daemon_offline?: boolean }
