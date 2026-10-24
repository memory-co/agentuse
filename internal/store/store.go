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

CREATE TABLE IF NOT EXISTS agents (
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

CREATE TABLE IF NOT EXISTS daemon_agents (
  daemon_id  TEXT NOT NULL REFERENCES daemons(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id),
  agent      TEXT NOT NULL REFERENCES agents(name) ON DELETE CASCADE,
  version    TEXT,
  available  INTEGER NOT NULL DEFAULT 1,
  enabled    INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (daemon_id, agent)
);
CREATE INDEX IF NOT EXISTS daemon_agents_user_agent ON daemon_agents(user_id, agent);

CREATE TABLE IF NOT EXISTS sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT NOT NULL REFERENCES users(id),
  agent              TEXT NOT NULL REFERENCES agents(name),
  daemon_id          TEXT REFERENCES daemons(id) ON DELETE SET NULL,
  cwd                TEXT,
  harness_session_id TEXT,
  title              TEXT NOT NULL DEFAULT '',
  status             TEXT NOT NULL DEFAULT 'pending',
  lease_until        INTEGER,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_status ON sessions(status, updated_at);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id, updated_at);

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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	s := &Store{db: db, now: time.Now}
	if err := s.seedBuiltinAgents(context.Background()); err != nil {
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
