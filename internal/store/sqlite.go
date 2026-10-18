package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id         TEXT PRIMARY KEY,
	platform   TEXT NOT NULL,
	thread     TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	data       TEXT NOT NULL,
	UNIQUE (platform, thread)
);
CREATE TABLE IF NOT EXISTS runs (
	seq         INTEGER PRIMARY KEY AUTOINCREMENT,
	id          TEXT NOT NULL UNIQUE,
	session_id  TEXT NOT NULL,
	platform    TEXT NOT NULL,
	status      TEXT NOT NULL,
	lease_until INTEGER,
	data        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS runs_status ON runs (status, seq);
CREATE INDEX IF NOT EXISTS runs_session ON runs (session_id, status);
`

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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &SQLite{db: db}, nil
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

func (s *SQLite) SessionByThread(ctx context.Context, platform, thread string) (models.Session, error) {
	return s.oneSession(ctx, `SELECT data FROM sessions WHERE platform = ? AND thread = ?`, platform, thread)
}

func (s *SQLite) GetSession(ctx context.Context, id string) (models.Session, error) {
	return s.oneSession(ctx, `SELECT data FROM sessions WHERE id = ?`, id)
}

func (s *SQLite) oneSession(ctx context.Context, q string, args ...any) (models.Session, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Session{}, ErrNotFound
		}
		return models.Session{}, err
	}
	return decode[models.Session](raw)
}

func (s *SQLite) PutSession(ctx context.Context, sess models.Session) error {
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, platform, thread, updated_at, data) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET updated_at = excluded.updated_at, data = excluded.data`,
		sess.ID, sess.Platform, sess.Thread, sess.UpdatedAt.UnixNano(), string(data))
	return err
}

func (s *SQLite) ListSessions(ctx context.Context, platform string, limit int) ([]models.Session, error) {
	q, args := `SELECT data FROM sessions`, []any{}
	if platform != "" {
		q, args = q+` WHERE platform = ?`, append(args, platform)
	}
	q, args = q+` ORDER BY updated_at DESC LIMIT ?`, append(args, limitOr(limit, 100))
	return list[models.Session](ctx, s.db, q, args...)
}

func list[T any](ctx context.Context, db *sql.DB, q string, args ...any) ([]T, error) {
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

func (s *SQLite) CreateRun(ctx context.Context, r models.Run) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO runs (id, session_id, platform, status, lease_until, data) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.SessionID, r.Platform, r.Status, nanos(r.LeaseUntil), string(data))
	return err
}

func (s *SQLite) GetRun(ctx context.Context, id string) (models.Run, error) {
	return getRun(ctx, s.db, id)
}

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func getRun(ctx context.Context, q querier, id string) (models.Run, error) {
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT data FROM runs WHERE id = ?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Run{}, ErrNotFound
		}
		return models.Run{}, err
	}
	return decode[models.Run](raw)
}

func (s *SQLite) ListRuns(ctx context.Context, f RunFilter) ([]models.Run, error) {
	var where []string
	var args []any
	if f.Platform != "" {
		where, args = append(where, "platform = ?"), append(args, f.Platform)
	}
	if f.SessionID != "" {
		where, args = append(where, "session_id = ?"), append(args, f.SessionID)
	}
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, f.Status)
	}
	q := `SELECT data FROM runs`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	q, args = q+` ORDER BY seq DESC LIMIT ?`, append(args, limitOr(f.Limit, 100))
	return list[models.Run](ctx, s.db, q, args...)
}

func updateRun(ctx context.Context, tx *sql.Tx, r models.Run) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET status = ?, lease_until = ?, data = ? WHERE id = ?`,
		r.Status, nanos(r.LeaseUntil), string(data), r.ID)
	return err
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

func (s *SQLite) ClaimRun(ctx context.Context, c Claim) (models.Run, error) {
	var r models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM runs
			WHERE status = 'queued'
			  AND session_id NOT IN (SELECT session_id FROM runs WHERE status = 'claimed')
			ORDER BY seq LIMIT 1`).Scan(&id)
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
		r.Status, r.DaemonID, r.ClaimedAt, r.LeaseUntil = models.RunClaimed, c.DaemonID, &now, &lease
		r.Attempts++
		return updateRun(ctx, tx, r)
	})
	return r, err
}

// held 在事务里改一条仍被 daemonID 第 attempt 次持有的运行。
func (s *SQLite) held(ctx context.Context, runID, daemonID string, attempt int, fn func(*models.Run)) (models.Run, error) {
	var r models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = getRun(ctx, tx, runID); err != nil {
			return err
		}
		if r.Status != models.RunClaimed || r.DaemonID != daemonID || r.Attempts != attempt {
			return ErrConflict
		}
		fn(&r)
		return updateRun(ctx, tx, r)
	})
	return r, err
}

func (s *SQLite) ExtendLease(ctx context.Context, runID, daemonID string, attempt int, until time.Time) (models.Run, error) {
	return s.held(ctx, runID, daemonID, attempt, func(r *models.Run) { r.LeaseUntil = &until })
}

func (s *SQLite) FinishRun(ctx context.Context, runID, daemonID string, attempt int, status models.RunStatus, errMsg string, now time.Time) (models.Run, error) {
	return s.held(ctx, runID, daemonID, attempt, func(r *models.Run) {
		r.Status, r.Error, r.FinishedAt, r.LeaseUntil = status, errMsg, &now, nil
	})
}

func (s *SQLite) ReapExpired(ctx context.Context, now time.Time, maxAttempts int) ([]models.Run, error) {
	var out []models.Run
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT data FROM runs WHERE status = 'claimed' AND lease_until IS NOT NULL AND lease_until <= ? ORDER BY seq`,
			now.UnixNano())
		if err != nil {
			return err
		}
		var expired []models.Run
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			r, err := decode[models.Run](raw)
			if err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range expired {
			reap(&r, now, maxAttempts)
			if err := updateRun(ctx, tx, r); err != nil {
				return err
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}
