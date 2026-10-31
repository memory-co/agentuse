// Package store owns the machine-local execution ledger. HTTP code only receives
// Reader, backed by a separate SQLite mode=ro/query_only connection.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	_ "modernc.org/sqlite"
)

type Run struct {
	RunID           string          `json:"run_id"`
	AttemptID       string          `json:"attempt_id"`
	Server          string          `json:"server"`
	DaemonID        string          `json:"daemon_id"`
	Sender          agentuse.Sender `json:"sender"`
	AgentID         string          `json:"agent_id"`
	SessionID       string          `json:"session_id"`
	WorkID          string          `json:"work_id,omitempty"`
	Harness         string          `json:"harness"`
	Summary         string          `json:"summary"`
	Directory       string          `json:"directory,omitempty"`
	ReceivedAt      int64           `json:"received_at"`
	FinishedAt      *int64          `json:"finished_at,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
	Status          string          `json:"status"`
	Error           string          `json:"error,omitempty"`
	SteerCount      int             `json:"steer_count"`
	AttachmentCount int             `json:"attachment_count"`
	Usage           *agentuse.Usage `json:"usage,omitempty"`
	CostUSD         float64         `json:"cost_usd"`
}
type Finish struct {
	Status, Error   string
	AttachmentCount int
	Usage           *agentuse.Usage
	CostUSD         float64
}
type Filter struct {
	Sender, Agent, Status string
	After, Before         int64
	Limit, Offset         int
}
type Page struct {
	Runs   []Run `json:"runs"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
type Statistic struct {
	Key       string         `json:"key"`
	Runs      int            `json:"runs"`
	UsageRuns int            `json:"usage_runs"`
	Usage     agentuse.Usage `json:"usage"`
	CostUSD   float64        `json:"cost_usd"`
}
type Writer interface {
	Receive(context.Context, Run) error
	Complete(context.Context, string, string, Finish) error
	Steer(context.Context, string, string, int64) error
	Reconcile(context.Context) error
	Purge(context.Context, time.Time) error
}
type Reader interface {
	List(context.Context, Filter) (Page, error)
	Get(context.Context, string, string) (Run, error)
	Stats(context.Context, string, Filter) ([]Statistic, error)
}
type Ledger struct {
	writer *writeStore
	reader *readStore
}
type writeStore struct{ db *sql.DB }
type readStore struct{ db *sql.DB }

func (l *Ledger) Writer() Writer { return l.writer }
func (l *Ledger) Reader() Reader { return l.reader }
func (l *Ledger) Close() error   { return errors.Join(l.reader.db.Close(), l.writer.db.Close()) }

const schema = `CREATE TABLE IF NOT EXISTS runs (
 run_id TEXT NOT NULL,attempt_id TEXT NOT NULL,server TEXT NOT NULL,daemon_id TEXT NOT NULL,
 sender_id TEXT NOT NULL,sender_name TEXT NOT NULL,agent_id TEXT NOT NULL,session_id TEXT NOT NULL,work_id TEXT NOT NULL,harness TEXT NOT NULL,
 summary TEXT NOT NULL,directory TEXT NOT NULL,received_at INTEGER NOT NULL,finished_at INTEGER,duration_ms INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',steer_count INTEGER NOT NULL DEFAULT 0,steer_ids TEXT NOT NULL DEFAULT '[]',attachment_count INTEGER NOT NULL DEFAULT 0,
 input_tokens INTEGER,output_tokens INTEGER,cache_read_tokens INTEGER,cache_write_tokens INTEGER,cost_usd REAL NOT NULL DEFAULT 0,
 PRIMARY KEY(run_id,attempt_id));
 CREATE INDEX IF NOT EXISTS runs_received ON runs(received_at DESC);
 CREATE INDEX IF NOT EXISTS runs_agent ON runs(agent_id,received_at DESC);
 CREATE INDEX IF NOT EXISTS runs_sender ON runs(sender_id,received_at DESC);`

func Open(path string) (*Ledger, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	w, err := sql.Open("sqlite", uri+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err = w.Exec(schema); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	if err = r.Ping(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	return &Ledger{writer: &writeStore{w}, reader: &readStore{r}}, nil
}
func (w *writeStore) Receive(ctx context.Context, r Run) error {
	if r.RunID == "" || r.AttemptID == "" {
		return fmt.Errorf("ledger run_id and attempt_id required")
	}
	if r.ReceivedAt == 0 {
		r.ReceivedAt = time.Now().UnixMilli()
	}
	summary := []rune(r.Summary)
	if len(summary) > 200 {
		summary = summary[:200]
	}
	_, err := w.db.ExecContext(ctx, `INSERT INTO runs(run_id,attempt_id,server,daemon_id,sender_id,sender_name,agent_id,session_id,work_id,harness,summary,directory,received_at,status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'running') ON CONFLICT DO NOTHING`, r.RunID, r.AttemptID, r.Server, r.DaemonID, r.Sender.ID, r.Sender.Name, r.AgentID, r.SessionID, r.WorkID, r.Harness, string(summary), r.Directory, r.ReceivedAt)
	return err
}
func (w *writeStore) Complete(ctx context.Context, run, attempt string, f Finish) error {
	var input, output, read, write any
	if f.Usage != nil {
		input = f.Usage.InputTokens
		output = f.Usage.OutputTokens
		read = f.Usage.CacheReadTokens
		write = f.Usage.CacheWriteTokens
	}
	now := time.Now().UnixMilli()
	_, err := w.db.ExecContext(ctx, `UPDATE runs SET status=?,error=?,finished_at=?,duration_ms=MAX(0,?-received_at),attachment_count=?,input_tokens=?,output_tokens=?,cache_read_tokens=?,cache_write_tokens=?,cost_usd=? WHERE run_id=? AND attempt_id=? AND status='running'`, f.Status, f.Error, now, now, f.AttachmentCount, input, output, read, write, f.CostUSD, run, attempt)
	return err
}
func (w *writeStore) Steer(ctx context.Context, run, attempt string, id int64) error {
	_, err := w.db.ExecContext(ctx, `UPDATE runs SET steer_count=steer_count+1,steer_ids=json_insert(steer_ids,'$[#]',?) WHERE run_id=? AND attempt_id=? AND NOT EXISTS(SELECT 1 FROM json_each(steer_ids) WHERE value=?)`, id, run, attempt, id)
	return err
}
func (w *writeStore) Reconcile(ctx context.Context) error {
	now := time.Now().UnixMilli()
	_, err := w.db.ExecContext(ctx, `UPDATE runs SET status='failed',error='主循环在执行完成前退出',finished_at=?,duration_ms=MAX(0,?-received_at) WHERE status='running'`, now, now)
	return err
}
func (w *writeStore) Purge(ctx context.Context, before time.Time) error {
	_, err := w.db.ExecContext(ctx, `DELETE FROM runs WHERE received_at<? AND status!='running'`, before.UnixMilli())
	return err
}
func where(f Filter) (string, []any) {
	q := " WHERE 1=1"
	args := []any{}
	if f.Sender != "" {
		q += " AND (sender_id=? OR sender_name LIKE ?)"
		args = append(args, f.Sender, "%"+f.Sender+"%")
	}
	if f.Agent != "" {
		q += " AND agent_id=?"
		args = append(args, f.Agent)
	}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.After > 0 {
		q += " AND received_at>=?"
		args = append(args, f.After)
	}
	if f.Before > 0 {
		q += " AND received_at<?"
		args = append(args, f.Before)
	}
	return q, args
}

const columns = `SELECT run_id,attempt_id,server,daemon_id,sender_id,sender_name,agent_id,session_id,work_id,harness,summary,directory,received_at,finished_at,duration_ms,status,error,steer_count,attachment_count,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd FROM runs`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Run, error) {
	var r Run
	var end, input, output, read, write sql.NullInt64
	err := row.Scan(&r.RunID, &r.AttemptID, &r.Server, &r.DaemonID, &r.Sender.ID, &r.Sender.Name, &r.AgentID, &r.SessionID, &r.WorkID, &r.Harness, &r.Summary, &r.Directory, &r.ReceivedAt, &end, &r.DurationMS, &r.Status, &r.Error, &r.SteerCount, &r.AttachmentCount, &input, &output, &read, &write, &r.CostUSD)
	if end.Valid {
		r.FinishedAt = &end.Int64
	}
	if input.Valid {
		r.Usage = &agentuse.Usage{InputTokens: input.Int64, OutputTokens: output.Int64, CacheReadTokens: read.Int64, CacheWriteTokens: write.Int64}
	}
	return r, err
}
func (r *readStore) List(ctx context.Context, f Filter) (Page, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	p := Page{Runs: []Run{}, Limit: f.Limit, Offset: f.Offset}
	q, args := where(f)
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs"+q, args...).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := r.db.QueryContext(ctx, columns+q+" ORDER BY received_at DESC,run_id,attempt_id LIMIT ? OFFSET ?", append(args, f.Limit, f.Offset)...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return p, e
		}
		p.Runs = append(p.Runs, v)
	}
	return p, rows.Err()
}
func (r *readStore) Get(ctx context.Context, run, attempt string) (Run, error) {
	return scan(r.db.QueryRowContext(ctx, columns+" WHERE run_id=? AND attempt_id=?", run, attempt))
}
func (r *readStore) Stats(ctx context.Context, group string, f Filter) ([]Statistic, error) {
	groups := map[string]string{"day": "strftime('%Y-%m-%d',received_at/1000,'unixepoch')", "sender": "sender_id", "agent": "agent_id", "harness": "harness"}
	key, ok := groups[group]
	if !ok {
		return nil, fmt.Errorf("invalid statistics group")
	}
	q, args := where(f)
	rows, err := r.db.QueryContext(ctx, "SELECT "+key+",COUNT(*),COUNT(input_tokens),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cache_read_tokens),0),COALESCE(SUM(cache_write_tokens),0),COALESCE(SUM(cost_usd),0) FROM runs"+q+" GROUP BY "+key+" ORDER BY "+key+" DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Statistic{}
	for rows.Next() {
		var s Statistic
		if err = rows.Scan(&s.Key, &s.Runs, &s.UsageRuns, &s.Usage.InputTokens, &s.Usage.OutputTokens, &s.Usage.CacheReadTokens, &s.Usage.CacheWriteTokens, &s.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

var _ Writer = (*writeStore)(nil)
var _ Reader = (*readStore)(nil)
