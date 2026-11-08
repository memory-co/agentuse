package store

const RunCapability = "agentuse-client/2"
const runSchema = `
-- A message target has no independent task/input identity.
CREATE TABLE IF NOT EXISTS message_agents (
 conversation_id TEXT NOT NULL, message_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, device_id TEXT NOT NULL, harness TEXT NOT NULL,
 dispatch TEXT, status TEXT NOT NULL DEFAULT 'pending',
 run_id TEXT NOT NULL DEFAULT '', input_id TEXT NOT NULL DEFAULT '',
 session_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 PRIMARY KEY(message_id,agent_id)
);
CREATE INDEX IF NOT EXISTS message_agents_dispatch ON message_agents(device_id,status,created_at,message_id);
CREATE UNIQUE INDEX IF NOT EXISTS message_agents_input ON message_agents(device_id,session_id,input_id) WHERE input_id<>'';
-- IDs and terminal facts come exclusively from authenticated client reports.
CREATE TABLE IF NOT EXISTS runs (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, first_message_id TEXT NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, device_id TEXT NOT NULL, session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id TEXT,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_conversation ON runs(conversation_id,created_at,id);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, conversation_id TEXT NOT NULL,
 session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, device_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, message_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL, payload TEXT NOT NULL,
 created_at INTEGER NOT NULL, UNIQUE(device_id,session_id,event_seq)
);
CREATE TABLE IF NOT EXISTS run_requests (
 request_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, run_id TEXT NOT NULL,
 session_id TEXT NOT NULL, device_id TEXT NOT NULL,
 payload TEXT NOT NULL, state TEXT NOT NULL, answer TEXT,
 answered_by TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS run_controls (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, session_id TEXT NOT NULL,
 run_id TEXT NOT NULL, device_id TEXT NOT NULL, kind TEXT NOT NULL, request_id TEXT NOT NULL DEFAULT '',
 answer TEXT, state TEXT NOT NULL DEFAULT 'pending', error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS work_run_tokens (
 token_hash TEXT PRIMARY KEY, work_id TEXT NOT NULL, message_id TEXT NOT NULL, agent_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS run_notifications (message_id TEXT PRIMARY KEY);
`
