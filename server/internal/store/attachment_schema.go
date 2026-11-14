package store

const attachmentSchema = `
CREATE TABLE IF NOT EXISTS attachments (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL,
 relative_path TEXT NOT NULL, filename TEXT NOT NULL, content_type TEXT NOT NULL, size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
 sha256 TEXT NOT NULL CHECK(length(sha256)=64), created_by TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('user','device')),
 device_id TEXT, created_by_run_id TEXT, created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS attachments_path ON attachments(conversation_id,relative_path);
CREATE INDEX IF NOT EXISTS attachments_conversation ON attachments(conversation_id,created_at,id);
CREATE TABLE IF NOT EXISTS device_capabilities (
 device_id TEXT NOT NULL,
 capability TEXT NOT NULL, PRIMARY KEY(device_id,capability)
);`
