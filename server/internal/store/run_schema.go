package store

const MultiAgent = "multi_agent"
const RunCapability = "session-runs-v1"
const runSchema = `
CREATE TABLE IF NOT EXISTS session_runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL, agent_key TEXT NOT NULL, agent_name TEXT NOT NULL,
 agent_user_id TEXT NOT NULL REFERENCES users(id), daemon_id TEXT REFERENCES daemons(id) ON DELETE SET NULL,
 harness TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', attempt_id TEXT NOT NULL DEFAULT '',
 lease_until INTEGER, context_seq INTEGER NOT NULL DEFAULT 0, result_message_id INTEGER REFERENCES messages(id) ON DELETE SET NULL,
 error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS runs_active_agent ON session_runs(session_id,agent_id) WHERE agent_id IS NOT NULL AND status IN ('pending','running','waiting');
CREATE INDEX IF NOT EXISTS runs_status ON session_runs(status,created_at);
CREATE INDEX IF NOT EXISTS runs_dispatch ON session_runs(daemon_id,status,created_at);
CREATE TABLE IF NOT EXISTS message_deliveries (
 id INTEGER PRIMARY KEY AUTOINCREMENT, message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL, agent_key TEXT NOT NULL,
 run_id TEXT REFERENCES session_runs(id) ON DELETE CASCADE, kind TEXT NOT NULL, status TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '', prepared_attempt TEXT NOT NULL DEFAULT '', UNIQUE(message_id,agent_key)
);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE, attempt_id TEXT NOT NULL,
 message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE, daemon_id TEXT NOT NULL,
 event_seq INTEGER NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(run_id,attempt_id,event_seq)
);
CREATE TABLE IF NOT EXISTS run_questions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE, attempt_id TEXT NOT NULL,
 message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE, tool_use_id TEXT NOT NULL,
 questions TEXT NOT NULL, answers TEXT, answered_by TEXT REFERENCES users(id), status TEXT NOT NULL DEFAULT 'pending',
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE(run_id,attempt_id,tool_use_id)
);
CREATE TABLE IF NOT EXISTS message_formats (message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,is_prompt INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS message_authors (
 message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
 run_id TEXT REFERENCES session_runs(id) ON DELETE SET NULL, agent_key TEXT NOT NULL, agent_name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_requests (
 session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, user_id TEXT NOT NULL REFERENCES users(id),
 request_key TEXT NOT NULL, digest TEXT NOT NULL, message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 PRIMARY KEY(session_id,user_id,request_key)
);
CREATE TABLE IF NOT EXISTS work_run_tokens (
 token_hash TEXT PRIMARY KEY, work_id TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE, attempt_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS run_notifications (
 message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE
);`
