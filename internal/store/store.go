// Package store 是 agentuse 服务的 SQLite 存储，表结构见 docs/v2/tables/。
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，不需要 cgo
)

// ErrNotFound 表示要找的行不存在(或不属于当前用户)。
var ErrNotFound = errors.New("store: not found")

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  role       TEXT NOT NULL DEFAULT 'user',
  token_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS users_token ON users(token_hash);

CREATE TABLE IF NOT EXISTS harnesses (
  name         TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  display_name TEXT NOT NULL,
  description  TEXT,
  owner_id     TEXT REFERENCES users(id),
  created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS daemons (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id),
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
  daemon_id  TEXT NOT NULL REFERENCES daemons(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id),
  harness    TEXT NOT NULL REFERENCES harnesses(name) ON DELETE CASCADE,
  version    TEXT,
  available  INTEGER NOT NULL DEFAULT 1,
  enabled    INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (daemon_id, harness)
);
CREATE INDEX IF NOT EXISTS daemon_harnesses_user_harness ON daemon_harnesses(user_id, harness);

CREATE TABLE IF NOT EXISTS agents (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id),
  name        TEXT NOT NULL,
  daemon_id   TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  harness     TEXT NOT NULL REFERENCES harnesses(name),
  description TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS agents_user_name ON agents(user_id, name);

CREATE TABLE IF NOT EXISTS sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id),
  created_by         TEXT NOT NULL REFERENCES users(id),
  agent_id           TEXT REFERENCES agents(id) ON DELETE SET NULL,
  work_id            TEXT REFERENCES works(id),
  harness            TEXT NOT NULL REFERENCES harnesses(name),
  daemon_id          TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  harness_session_id TEXT,
  title              TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'pending',
  lease_until        INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_status ON sessions(status, updated_at);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id, updated_at);
CREATE INDEX IF NOT EXISTS sessions_created_by ON sessions(created_by, updated_at);

CREATE TABLE IF NOT EXISTS works (
  id             TEXT PRIMARY KEY,
  user_id        TEXT NOT NULL REFERENCES users(id),
  name           TEXT NOT NULL,
  instructions   TEXT NOT NULL DEFAULT '',
  coordinator_id TEXT NOT NULL UNIQUE,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS works_user ON works(user_id, updated_at);

CREATE TABLE IF NOT EXISTS work_tokens (
  token_hash TEXT PRIMARY KEY,
  work_id    TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS work_tokens_message ON work_tokens(message_id);

CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_session ON messages(session_id, id);

CREATE TABLE IF NOT EXISTS session_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  daemon_id  TEXT NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS session_events_session ON session_events(session_id, id);

CREATE TABLE IF NOT EXISTS steers (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id INTEGER NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(id),
  prompt     TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS steers_message ON steers(message_id, id);
CREATE INDEX IF NOT EXISTS steers_session ON steers(session_id, id);

CREATE TABLE IF NOT EXISTS questions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  message_id  INTEGER NOT NULL,
  tool_use_id TEXT NOT NULL,
  questions   TEXT NOT NULL,
  answers     TEXT,
  answered_by TEXT REFERENCES users(id),
  status      TEXT NOT NULL DEFAULT 'pending',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS questions_session ON questions(session_id, id);
CREATE UNIQUE INDEX IF NOT EXISTS questions_tool_use ON questions(message_id, tool_use_id);
`

// Store 包一个 SQLite 连接。单连接 + BEGIN IMMEDIATE,所有写串行，领活的「挑一条再改」是原子的。
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open 打开(必要时创建)数据库文件。path 为 ":memory:" 时是临时库。
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)&_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + attachmentSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	if err := addWorkColumn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	s := &Store{db: db, now: time.Now}
	if err := s.seedBuiltinHarnesses(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) unix() int64 { return s.now().Unix() }

// HashSecret 把令牌 / 密钥变成存库用的哈希。
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// RandomToken 生成带前缀的随机令牌，如 ut_xxxx。
func RandomToken(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullStr(v sql.NullString) string { return v.String }

// addWorkColumn 给老库的 sessions 补上 work_id(建表语句里已有，新库什么都不做),再建它的索引。
func addWorkColumn(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'work_id'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN work_id TEXT REFERENCES works(id)`); err != nil {
			return err
		}
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS sessions_work ON sessions(work_id, updated_at)`)
	return err
}
