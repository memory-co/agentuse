package store

const attachmentSchema = `
CREATE TABLE IF NOT EXISTS attachments (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
 sha256 TEXT NOT NULL CHECK(length(sha256)=64), created_by TEXT NOT NULL REFERENCES users(id),
 source TEXT NOT NULL CHECK(source IN ('user','daemon','work')),
 daemon_id TEXT REFERENCES daemons(id) ON DELETE SET NULL, created_by_run_id TEXT, created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_session ON attachments(session_id,created_at,id);
CREATE TABLE IF NOT EXISTS daemon_capabilities (
 daemon_id TEXT NOT NULL REFERENCES daemons(id) ON DELETE CASCADE,
 capability TEXT NOT NULL, PRIMARY KEY(daemon_id,capability)
);`
