package store

const MultiAgent = "multi_agent"
const RunCapability = "harnessctl-events-v1"
const runSchema = `
-- A prompt is a server-side message delivery, not an execution.
CREATE TABLE IF NOT EXISTS run_prompts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL, message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL, harness TEXT NOT NULL,
 content TEXT NOT NULL, dispatch TEXT, status TEXT NOT NULL DEFAULT 'pending',
 run_id TEXT NOT NULL DEFAULT '', input_id TEXT NOT NULL DEFAULT '',
 client_session_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 UNIQUE(message_id,agent_id)
);
CREATE INDEX IF NOT EXISTS prompts_dispatch ON run_prompts(daemon_id,status,id);
CREATE INDEX IF NOT EXISTS prompts_run ON run_prompts(run_id,input_id);
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, first_message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id INTEGER,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_conversation ON runs(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
 created_at INTEGER NOT NULL, UNIQUE(daemon_id,client_session_id,event_seq)
);
CREATE TABLE IF NOT EXISTS run_requests (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, run_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
 payload TEXT NOT NULL, state TEXT NOT NULL, answer TEXT,
 answered_by TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_controls (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 run_id TEXT NOT NULL, daemon_id TEXT NOT NULL, kind TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
 answer TEXT, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS work_run_tokens (
 token_hash TEXT PRIMARY KEY, work_id TEXT NOT NULL, prompt_id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_notifications (message_id INTEGER PRIMARY KEY);
`
