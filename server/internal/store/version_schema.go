package store

const versionSchema = `
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
`
