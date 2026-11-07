-- Released schema 3, retained as an upgrade fixture.

CREATE TABLE IF NOT EXISTS users (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  role       TEXT NOT NULL DEFAULT 'user',
  token_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS users_token ON users(token_hash);

CREATE TABLE IF NOT EXISTS daemons (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL,
  name         TEXT NOT NULL,
  key_hash     TEXT,
  setup_token  TEXT,
  status       TEXT NOT NULL DEFAULT 'pending',
  last_seen_at INTEGER,
  created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS daemons_key ON daemons(key_hash);
CREATE UNIQUE INDEX IF NOT EXISTS daemons_setup_token ON daemons(setup_token);

CREATE TABLE IF NOT EXISTS daemon_harnesses (
  daemon_id  TEXT NOT NULL,
  user_id    TEXT NOT NULL,
  harness    TEXT NOT NULL,
  version    TEXT,
  revision   TEXT NOT NULL DEFAULT '',
  available  INTEGER NOT NULL DEFAULT 1,
  enabled    INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (daemon_id, harness)
);
CREATE INDEX IF NOT EXISTS daemon_harnesses_user_harness ON daemon_harnesses(user_id, harness);

CREATE TABLE IF NOT EXISTS sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL,
  created_by         TEXT NOT NULL,
  work_id            TEXT,
  title              TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'idle',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_work ON sessions(work_id,updated_at);
CREATE INDEX IF NOT EXISTS sessions_status ON sessions(status, updated_at);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id, updated_at);
CREATE INDEX IF NOT EXISTS sessions_created_by ON sessions(created_by, updated_at);

CREATE TABLE IF NOT EXISTS works (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL,
  name           TEXT NOT NULL,
  instructions   TEXT NOT NULL DEFAULT '',
  coordinator_id TEXT NOT NULL UNIQUE,
  coordinator_agent_id TEXT,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS works_user ON works(user_id, updated_at);

CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  author_user_id TEXT, author_agent_id TEXT, run_agent_ids TEXT NOT NULL DEFAULT '[]', reply_to_message_id INTEGER, author_agent_version_id TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, id);



CREATE TABLE IF NOT EXISTS agents(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,current_version_id TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,deleted_at INTEGER);
CREATE INDEX IF NOT EXISTS agents_user ON agents(user_id,updated_at);
CREATE TABLE IF NOT EXISTS agent_versions(id TEXT PRIMARY KEY,agent_id TEXT NOT NULL,version INTEGER NOT NULL CHECK(version>0),name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL,harness TEXT NOT NULL,model TEXT,instructions TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,UNIQUE(agent_id,version));
CREATE INDEX IF NOT EXISTS agent_versions_agent ON agent_versions(agent_id,version);
CREATE TABLE IF NOT EXISTS agent_version_skills(agent_version_id TEXT NOT NULL,skill_id TEXT NOT NULL,skill_version_id TEXT NOT NULL,position INTEGER NOT NULL CHECK(position>=0),PRIMARY KEY(agent_version_id,skill_id),UNIQUE(agent_version_id,position));
CREATE INDEX IF NOT EXISTS agent_version_skills_version ON agent_version_skills(skill_version_id);
CREATE TABLE IF NOT EXISTS skills(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,current_version_id TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,deleted_at INTEGER);
CREATE INDEX IF NOT EXISTS skills_user ON skills(user_id,updated_at);
CREATE TABLE IF NOT EXISTS skill_versions(id TEXT PRIMARY KEY,skill_id TEXT NOT NULL,version INTEGER NOT NULL CHECK(version>0),name TEXT NOT NULL,description TEXT NOT NULL,package_path TEXT NOT NULL UNIQUE,sha256 TEXT NOT NULL,size_bytes INTEGER NOT NULL CHECK(size_bytes>0),created_at INTEGER NOT NULL,UNIQUE(skill_id,version));
CREATE INDEX IF NOT EXISTS skill_versions_skill ON skill_versions(skill_id,version);


CREATE TABLE IF NOT EXISTS attachments (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
 filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
 sha256 TEXT NOT NULL CHECK(length(sha256)=64), created_by TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('user','daemon','work')),
 daemon_id TEXT, created_by_run_id TEXT, created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_session ON attachments(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS daemon_capabilities (
 daemon_id TEXT NOT NULL,
 capability TEXT NOT NULL, PRIMARY KEY(daemon_id,capability)
);

-- A prompt is a server-side message delivery, not an execution.
CREATE TABLE IF NOT EXISTS run_prompts (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL, message_id INTEGER NOT NULL,
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL, harness TEXT NOT NULL,
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
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL, client_session_id TEXT NOT NULL,
 harness TEXT NOT NULL, harness_revision TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'running', result_message_id INTEGER,
 output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 result TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_conversation ON runs(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS run_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
 client_session_id TEXT NOT NULL, run_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL, agent_version_id TEXT NOT NULL, daemon_id TEXT NOT NULL,
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

PRAGMA user_version=3;
