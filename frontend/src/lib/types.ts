// 和 docs/v2/apis 里的返回结构对应。时间都是 unix 秒。
export type Role = 'admin' | 'user';
export interface User { id: string; name: string; role: Role; created_at: number }

export interface Harness { name: string; kind: 'builtin' | 'custom'; display_name: string; description: string; owner_id?: string; created_at: number }

export interface DaemonHarness { harness: string; version: string; available: boolean; enabled: boolean; updated_at: number }
export interface Daemon {
  id: string; user_id: string; name: string; status: 'pending' | 'online' | 'offline';
  setup_command?: string; last_seen_at: number | null; created_at: number; harnesses: DaemonHarness[];
}

export type SessionStatus = 'idle' | 'pending' | 'running';
export interface Session {
  id: string; user_id: string; harness: string; daemon_id?: string; cwd?: string; harness_session_id?: string;
  title: string; status: SessionStatus; created_at: number; updated_at: number;
}
export interface Message { id: number; session_id: string; role: 'user' | 'assistant'; content: any; created_at: number }
export interface SessionEvent { id: number; session_id: string; message_id: number; daemon_id: string; kind: string; payload?: any; created_at: number }
export interface SessionDetail { session: Session; messages: Message[]; events: SessionEvent[] }
