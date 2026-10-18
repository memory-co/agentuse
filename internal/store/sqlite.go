package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/models"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动,不需要 cgo
)

// SQLite 是持久化实现:一个文件(默认 ~/.agentuse/hub.db)。
//
// 每张表把整条记录存成 JSON(data 列),另抽出查询要用的几列。
// 单连接 + BEGIN IMMEDIATE:所有写串行,ClaimRun 的「挑一条再改」在一个事务里原子完成。
type SQLite struct {
	db *sql.DB
}

// schemaVersion 记在 PRAGMA user_version 里。发布前的旧库版本不对时直接重建(数据丢弃)。
const schemaVersion = 2

const schema = `
CREATE TABLE users (
	platform TEXT NOT NULL,
	id       TEXT NOT NULL,
	data     TEXT NOT NULL,
	PRIMARY KEY (platform, id)
);
CREATE TABLE agents (
	id            TEXT PRIMARY KEY,
	platform      TEXT NOT NULL,
	handle        TEXT NOT NULL,
	owner         TEXT NOT NULL,
	device_id     TEXT NOT NULL DEFAULT '',
	agent_user_id TEXT NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	data          TEXT NOT NULL,
	UNIQUE (platform, handle)
);
CREATE INDEX agents_device ON agents (device_id);
CREATE INDEX agents_owner ON agents (platform, owner);
CREATE TABLE devices (
	id   TEXT PRIMARY KEY,
	data TEXT NOT NULL
);
CREATE TABLE connect_codes (
	hash       TEXT PRIMARY KEY,
	agent_id   TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	data       TEXT NOT NULL
);
CREATE INDEX connect_codes_agent ON connect_codes (agent_id);
CREATE TABLE sessions (
	id         TEXT PRIMARY KEY,
	platform   TEXT NOT NULL,
	thread     TEXT NOT NULL,
	agent_id   TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	data       TEXT NOT NULL,
	UNIQUE (platform, thread, agent_id)
);
CREATE TABLE runs (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	id          TEXT NOT NULL UNIQUE,
	session_id  TEXT NOT NULL,
	platform    TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	status      TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	lease_until INTEGER,
	data        TEXT NOT NULL
);
CREATE INDEX runs_status ON runs (status, seq);
CREATE INDEX runs_session ON runs (session_id, status);
CREATE INDEX runs_agent ON runs (agent_id, status);
`

var tables = []string{"users", "agents", "devices", "connect_codes", "sessions", "runs"}

// OpenSQLite 打开(必要时建)数据库文件。path 为 ":memory:" 时是临时库。
func OpenSQLite(path string) (*SQLite, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	if path == ":memory:" {
		dsn = "file::memory:?_txlock=immediate"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &SQLite{db: db}, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) Close() error { return s.db.Close() }

func nanos(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

func decode[T any](raw string) (T, error) {
	var v T
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}

func encode(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// one 读一行的 data 列;没有返回 ErrNotFound。
func one[T any](ctx context.Context, q querier, query string, args ...any) (T, error) {
	var raw string
	if err := q.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		var zero T
		if errors.Is(err, sql.ErrNoRows) {
			return zero, ErrNotFound
		}
		return zero, err
	}
	return decode[T](raw)
}

type rowsQuerier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func list[T any](ctx context.Context, db rowsQuerier, q string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		v, err := decode[T](raw)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// tx 在一个 IMMEDIATE 事务里跑 fn。
func (s *SQLite) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- users ----

func (s *SQLite) PutUser(ctx context.Context, u models.User) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if old, err := one[models.User](ctx, tx, `SELECT data FROM users WHERE platform = ? AND id = ?`, u.Platform, u.ID); err == nil {
			u.CreatedAt = old.CreatedAt
		}
		data, err := encode(u)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO users (platform, id, data) VALUES (?, ?, ?)
			ON CONFLICT (platform, id) DO UPDATE SET data = excluded.data`, u.Platform, u.ID, data)
		return err
	})
}

func (s *SQLite) GetUser(ctx context.Context, platform, id string) (models.User, error) {
	return one[models.User](ctx, s.db, `SELECT data FROM users WHERE platform = ? AND id = ?`, platform, id)
}

// ---- agents ----

func (s *SQLite) CreateAgent(ctx context.Context, a models.Agent) error {
	data, err := encode(a)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agents (id, platform, handle, owner, device_id, agent_user_id, created_at, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, a.Platform, a.Handle, a.Owner, a.DeviceID, a.AgentUserID, a.CreatedAt.UnixNano(), data)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

func (s *SQLite) PutAgent(ctx context.Context, a models.Agent) error {
	data, err := encode(a)
	if err != nil {
		return err
	}
	return affected(s.db.ExecContext(ctx, `UPDATE agents SET owner = ?, device_id = ?, agent_user_id = ?, data = ? WHERE id = ?`,
		a.Owner, a.DeviceID, a.AgentUserID, data, a.ID))
}

func (s *SQLite) GetAgent(ctx context.Context, id string) (models.Agent, error) {
	return one[models.Agent](ctx, s.db, `SELECT data FROM agents WHERE id = ?`, id)
}

func (s *SQLite) AgentByHandle(ctx context.Context, platform, handle string) (models.Agent, error) {
	return one[models.Agent](ctx, s.db, `SELECT data FROM agents WHERE platform = ? AND handle = ?`, platform, handle)
}

func (s *SQLite) ListAgents(ctx context.Context, f AgentFilter) ([]models.Agent, error) {
	where, args := filters(map[string]string{
		"platform": f.Platform, "owner": f.Owner, "device_id": f.DeviceID, "agent_user_id": f.AgentUserID,
	})
	return list[models.Agent](ctx, s.db, `SELECT data FROM agents`+where+` ORDER BY created_at, id`, args...)
}

// filters 把非空的「列 = 值」拼成 WHERE 子句。列名按字母序,结果稳定。
func filters(cols map[string]string) (string, []any) {
	names := make([]string, 0, len(cols))
	for c, v := range cols {
		if v != "" {
			names = append(names, c)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	args := make([]any, len(names))
	for i, c := range names {
		args[i] = cols[c]
		names[i] = c + " = ?"
	}
	return " WHERE " + strings.Join(names, " AND "), args
}

func (s *SQLite) DeleteAgent(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := affected(tx.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM connect_codes WHERE agent_id = ?`, id)
		return err
	})
}

// ---- devices ----

func (s *SQLite) PutDevice(ctx context.Context, d models.Device) error {
	data, err := encode(d)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO devices (id, data) VALUES (?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data`, d.ID, data)
	return err
}

func (s *SQLite) GetDevice(ctx context.Context, id string) (models.Device, error) {
	return one[models.Device](ctx, s.db, `SELECT data FROM devices WHERE id = ?`, id)
}

// ---- connect codes ----

func (s *SQLite) PutConnectCode(ctx context.Context, c models.ConnectCode) error {
	data, err := encode(c)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM connect_codes WHERE agent_id = ?`, c.AgentID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO connect_codes (hash, agent_id, expires_at, data) VALUES (?, ?, ?, ?)`,
			c.Hash, c.AgentID, c.ExpiresAt.UnixNano(), data)
		return err
	})
}

func (s *SQLite) TakeConnectCode(ctx context.Context, hash string, now time.Time) (models.ConnectCode, error) {
	var c models.ConnectCode
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if c, err = one[models.ConnectCode](ctx, tx, `SELECT data FROM connect_codes WHERE hash = ?`, hash); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM connect_codes WHERE hash = ?`, hash)
		return err
	})
	if err == nil && !now.Before(c.ExpiresAt) {
		return models.ConnectCode{}, ErrNotFound
	}
	return c, err
}

// ---- sessions ----

func (s *SQLite) SessionByThread(ctx context.Context, platform, thread, agentID string) (models.Session, error) {
	return one[models.Session](ctx, s.db, `SELECT data FROM sessions WHERE platform = ? AND thread = ? AND agent_id = ?`, platform, thread, agentID)
}

func (s *SQLite) GetSession(ctx context.Context, id string) (models.Session, error) {
	return one[models.Session](ctx, s.db, `SELECT data FROM sessions WHERE id = ?`, id)
}

func (s *SQLite) PutSession(ctx context.Context, sess models.Session) error {
	data, err := encode(sess)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, platform, thread, agent_id, updated_at, data) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET updated_at = excluded.updated_at, data = excluded.data`,
		sess.ID, sess.Platform, sess.Thread, sess.AgentID, sess.UpdatedAt.UnixNano(), data)
	return err
}

func (s *SQLite) ListSessions(ctx context.Context, platform string, limit int) ([]models.Session, error) {
	where, args := filters(map[string]string{"platform": platform})
	return list[models.Session](ctx, s.db, `SELECT data FROM sessions`+where+` ORDER BY updated_at DESC LIMIT ?`,
		append(args, limitOr(limit, 100))...)
}

// ---- runs ----

func (s *SQLite) CreateRun(ctx context.Context, r models.Run) error {
	data, err := encode(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, platform, agent_id, status, created_at, lease_until, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SessionID, r.Platform, r.AgentID, r.Status, r.CreatedAt.UnixNano(), nanos(r.LeaseUntil), data)
	return err
}

func (s *SQLite) GetRun(ctx context.Context, id string) (models.Run, error) {
	return getRun(ctx, s.db, id)
}

func getRun(ctx context.Context, q querier, id string) (models.Run, error) {
	return one[models.Run](ctx, q, `SELECT data FROM runs WHERE id = ?`, id)
}

func (s *SQLite) ListRuns(ctx context.Context, f RunFilter) ([]models.Run, error) {
	where, args := filters(map[string]string{
		"platform": f.Platform, "session_id": f.SessionID, "agent_id": f.AgentID, "status": string(f.Status),
	})
	return list[models.Run](ctx, s.db, `SELECT data FROM runs`+where+` ORDER BY seq DESC LIMIT ?`,
		append(args, limitOr(f.Limit, 100))...)
}

func updateRun(ctx context.Context, tx *sql.Tx, r models.Run) error {
	data, err := encode(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET status = ?, lease_until = ?, data = ? WHERE id = ?`,
		r.Status, nanos(r.LeaseUntil), data, r.ID)
	return err
}

func (s *SQLite) ClaimRun(ctx context.Context, c Claim) (models.Run, error) {
	var r models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM runs
			WHERE status = 'queued'
			  AND agent_id IN (SELECT id FROM agents WHERE device_id = ? AND device_id != '')
			  AND session_id NOT IN (SELECT session_id FROM runs WHERE status = 'claimed')
			ORDER BY seq LIMIT 1`, c.DeviceID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if r, err = getRun(ctx, tx, id); err != nil {
			return err
		}
		now, lease := c.Now, c.LeaseUntil
		r.Status, r.DeviceID, r.ClaimedAt, r.LeaseUntil = models.RunClaimed, c.DeviceID, &now, &lease
		r.Attempts++
		return updateRun(ctx, tx, r)
	})
	return r, err
}

// held 在事务里改一条仍被 deviceID 第 attempt 次持有的运行。
func (s *SQLite) held(ctx context.Context, runID, deviceID string, attempt int, fn func(*models.Run)) (models.Run, error) {
	var r models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = getRun(ctx, tx, runID); err != nil {
			return err
		}
		if r.Status != models.RunClaimed || r.DeviceID != deviceID || r.Attempts != attempt {
			return ErrConflict
		}
		fn(&r)
		return updateRun(ctx, tx, r)
	})
	return r, err
}

func (s *SQLite) ExtendLease(ctx context.Context, runID, deviceID string, attempt int, until time.Time) (models.Run, error) {
	return s.held(ctx, runID, deviceID, attempt, func(r *models.Run) { r.LeaseUntil = &until })
}

func (s *SQLite) FinishRun(ctx context.Context, runID, deviceID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error) {
	return s.held(ctx, runID, deviceID, attempt, func(r *models.Run) {
		r.Status, r.Error, r.FinishedAt, r.LeaseUntil = status, errMsg, &now, nil
	})
}

// update 在一个事务里把 query 选出的运行逐条用 fn 改掉并写回。
func (s *SQLite) update(ctx context.Context, fn func(*models.Run) bool, query string, args ...any) ([]models.Run, error) {
	var out []models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		runs, err := list[models.Run](ctx, tx, query, args...)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if !fn(&r) {
				continue
			}
			if err := updateRun(ctx, tx, r); err != nil {
				return err
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}

func (s *SQLite) ReapExpired(ctx context.Context, now time.Time, maxAttempts int) ([]models.Run, error) {
	return s.update(ctx, func(r *models.Run) bool { reap(r, now, maxAttempts); return true },
		`SELECT data FROM runs WHERE status = 'claimed' AND lease_until IS NOT NULL AND lease_until <= ? ORDER BY seq`,
		now.UnixNano())
}

func (s *SQLite) FailRuns(ctx context.Context, f FailFilter, errMsg string, now time.Time) ([]models.Run, error) {
	if len(f.Statuses) == 0 {
		return nil, nil
	}
	where, args := filters(map[string]string{"platform": f.Platform, "agent_id": f.AgentID})
	if where == "" {
		where = " WHERE 1"
	}
	where += " AND status IN (?" + strings.Repeat(", ?", len(f.Statuses)-1) + ")"
	for _, st := range f.Statuses {
		args = append(args, string(st))
	}
	if !f.CreatedBefore.IsZero() {
		where, args = where+" AND created_at < ?", append(args, f.CreatedBefore.UnixNano())
	}
	return s.update(ctx, func(r *models.Run) bool {
		if !failMatches(*r, f) {
			return false
		}
		fail(r, errMsg, now)
		return true
	}, `SELECT data FROM runs`+where+` ORDER BY seq`, args...)
}
