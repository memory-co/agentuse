package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Run struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	MessageID       int64  `json:"message_id"`
	AgentID         string `json:"agent_id,omitempty"`
	AgentKey        string `json:"agent_key"`
	AgentName       string `json:"agent_name"`
	AgentUserID     string `json:"agent_user_id"`
	DaemonID        string `json:"daemon_id,omitempty"`
	Harness         string `json:"harness"`
	Status          string `json:"status"`
	AttemptID       string `json:"attempt_id,omitempty"`
	LeaseUntil      *int64 `json:"lease_until,omitempty"`
	ContextSeq      int64  `json:"context_seq"`
	ResultMessageID *int64 `json:"result_message_id,omitempty"`
	Error           string `json:"error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}
type Delivery struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentKey  string `json:"agent_key"`
	RunID     string `json:"run_id,omitempty"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}
type MultiReply struct {
	Message    Message    `json:"message"`
	Runs       []Run      `json:"runs"`
	Deliveries []Delivery `json:"deliveries"`
}
type RunClaim struct {
	Run                     Run
	Input                   json.RawMessage
	Messages                []Message
	TaskMessageIDs          []int64
	Instructions, WorkToken string
}

const runCols = `SELECT id,session_id,message_id,COALESCE(agent_id,''),agent_key,agent_name,agent_user_id,COALESCE(daemon_id,''),harness,status,attempt_id,lease_until,context_seq,result_message_id,error,created_at,updated_at FROM session_runs`

func scanRun(row scanner) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.SessionID, &r.MessageID, &r.AgentID, &r.AgentKey, &r.AgentName, &r.AgentUserID, &r.DaemonID, &r.Harness, &r.Status, &r.AttemptID, &r.LeaseUntil, &r.ContextSeq, &r.ResultMessageID, &r.Error, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}
func (s *Store) GetRun(ctx context.Context, u User, session, id string) (Run, error) {
	if _, err := s.GetSession(ctx, u, session); err != nil {
		return Run{}, err
	}
	return scanRun(s.db.QueryRowContext(ctx, runCols+` WHERE session_id=? AND id=?`, session, id))
}
func (s *Store) Runs(ctx context.Context, session string) ([]Run, error) {
	return runs(ctx, s.db, session)
}
func runs(ctx context.Context, q rowsQuerier, session string) ([]Run, error) {
	rows, err := q.QueryContext(ctx, runCols+` WHERE session_id=? ORDER BY message_id,created_at,id`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const deliveryCols = `SELECT id,message_id,session_id,COALESCE(agent_id,''),agent_key,COALESCE(run_id,''),kind,status,error FROM message_deliveries`

func deliveries(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, where string, args ...any) ([]Delivery, error) {
	rows, err := q.QueryContext(ctx, deliveryCols+` WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.MessageID, &d.SessionID, &d.AgentID, &d.AgentKey, &d.RunID, &d.Kind, &d.Status, &d.Error); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Deliveries(ctx context.Context, session string) ([]Delivery, error) {
	return deliveries(ctx, s.db, `session_id=?`, session)
}
func multiStatus(ctx context.Context, tx *sql.Tx, session string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE sessions SET status=CASE
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='waiting') THEN 'waiting'
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='running') THEN 'running'
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='pending') THEN 'pending'
 WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=? AND mode='multi_agent'`, session, session, session, now, session)
	return err
}
func (s *Store) insertMulti(ctx context.Context, tx *sql.Tx, u User, workID, title string) (Session, error) {
	if workID != "" {
		if _, err := getWork(ctx, tx, u, workID); err != nil {
			return Session{}, err
		}
	}
	// Sessions own shared history; runtime selection belongs to each run.
	now := s.unix()
	id := randomID("ss_")
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,created_by,work_id,title,status,created_at,updated_at,mode) VALUES(?,?,?,?,?,'idle',?,?,'multi_agent')`, id, u.ID, u.ID, nullIfEmpty(workID), title, now, now)
	if err != nil {
		return Session{}, err
	}
	return s.getSession(ctx, tx, u, id)
}
func (s *Store) CreateMultiSession(ctx context.Context, u User, workID, title string, content json.RawMessage, targets []string, key string, promptMode ...bool) (Session, *MultiReply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, nil, err
	}
	defer tx.Rollback()
	ss, err := s.insertMulti(ctx, tx, u, workID, title)
	if err != nil {
		return ss, nil, err
	}
	var reply *MultiReply
	if content != nil {
		r, e := s.sendMultiTx(ctx, tx, u, ss, content, targets, key, promptMode...)
		if e != nil {
			return ss, nil, e
		}
		reply = &r
		ss, e = s.getSession(ctx, tx, u, ss.ID)
		if e != nil {
			return ss, nil, e
		}
	}
	return ss, reply, tx.Commit()
}
func (s *Store) SendMultiMessage(ctx context.Context, u User, session string, content json.RawMessage, targets []string, key string, promptMode ...bool) (MultiReply, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MultiReply{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, session)
	if err != nil {
		return MultiReply{}, err
	}
	r, err := s.sendMultiTx(ctx, tx, u, ss, content, targets, key, promptMode...)
	if err != nil {
		return r, err
	}
	return r, tx.Commit()
}
func (s *Store) replyFor(ctx context.Context, tx *sql.Tx, messageID int64) (MultiReply, error) {
	out := MultiReply{Runs: []Run{}}
	var err error
	out.Message, err = scanMessage(tx.QueryRowContext(ctx, messageCols+` WHERE id=?`, messageID))
	if err != nil {
		return out, err
	}
	out.Deliveries, err = deliveries(ctx, tx, `message_id=?`, messageID)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, d := range out.Deliveries {
		if d.Kind == "new_run" && !seen[d.RunID] && d.RunID != "" {
			r, e := scanRun(tx.QueryRowContext(ctx, runCols+` WHERE id=?`, d.RunID))
			if e != nil {
				return out, e
			}
			out.Runs = append(out.Runs, r)
			seen[d.RunID] = true
		}
	}
	return out, nil
}
func (s *Store) runAgent(ctx context.Context, q querier, u User, ss Session, id string) (Agent, error) {
	a, err := scanAgent(q.QueryRowContext(ctx, agentCols+` WHERE id=?`, id))
	if err != nil {
		return a, fmt.Errorf("agent %q 不存在", id)
	}
	if ss.WorkID != "" && a.UserID != ss.UserID {
		return a, fmt.Errorf("工作只能使用主人自己的 agent")
	}
	return a, nil
}
func runRuntime(ctx context.Context, q querier, a Agent) error {
	if a.DaemonID == "" {
		return ErrSessionOrphaned
	}
	if err := runtimeErr(ctx, q, a.DaemonID, a.Harness); err != nil {
		return err
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM daemon_capabilities WHERE daemon_id=? AND capability=?`, a.DaemonID, RunCapability).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("daemon 尚不支持多 agent 会话，请升级并重连")
	}
	return nil
}
func (s *Store) newRun(ctx context.Context, tx *sql.Tx, ss Session, a Agent, messageID int64) (Run, error) {
	if err := runRuntime(ctx, tx, a); err != nil {
		return Run{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE session_id=?`, ss.ID).Scan(&count); err != nil {
		return Run{}, err
	}
	if count > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM daemon_capabilities WHERE daemon_id=? AND capability='attachments-v1'`, a.DaemonID).Scan(&count); err != nil {
			return Run{}, err
		}
		if count == 0 {
			return Run{}, ErrAttachmentCapability
		}
	}
	now := s.unix()
	r := Run{ID: randomID("sr_"), SessionID: ss.ID, MessageID: messageID, AgentID: a.ID, AgentKey: a.ID, AgentName: a.Name, AgentUserID: a.UserID, DaemonID: a.DaemonID, Harness: a.Harness, Status: "pending", CreatedAt: now, UpdatedAt: now}
	_, err := tx.ExecContext(ctx, `INSERT INTO session_runs(id,session_id,message_id,agent_id,agent_key,agent_name,agent_user_id,daemon_id,harness,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'pending',?,?)`, r.ID, ss.ID, messageID, a.ID, a.ID, a.Name, a.UserID, a.DaemonID, a.Harness, now, now)
	return r, err
}
func (s *Store) sendMultiTx(ctx context.Context, tx *sql.Tx, u User, ss Session, content json.RawMessage, targets []string, key string, promptMode ...bool) (MultiReply, error) {
	if ss.Mode != MultiAgent || !json.Valid(content) {
		return MultiReply{}, fmt.Errorf("需要 multi_agent 会话和合法 input")
	}
	uniq := []string{}
	seen := map[string]bool{}
	for _, id := range targets {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) > 8 || len(key) > 256 {
		return MultiReply{}, fmt.Errorf("每条消息最多 8 个目标，幂等 key 最长 256 字节")
	}
	isPrompt := promptContent(content)
	if len(promptMode) > 0 {
		isPrompt = promptMode[0]
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return MultiReply{}, err
	}
	request, _ := json.Marshal(struct {
		Input      any
		Targets    []string
		PromptMode bool
	}{normalized, uniq, isPrompt})
	digest := HashSecret(string(request))
	if key != "" {
		var old string
		var messageID int64
		err := tx.QueryRowContext(ctx, `SELECT digest,message_id FROM session_requests WHERE session_id=? AND user_id=? AND request_key=?`, ss.ID, u.ID, key).Scan(&old, &messageID)
		if err == nil {
			if old != digest {
				return MultiReply{}, ErrSessionState
			}
			return s.replyFor(ctx, tx, messageID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return MultiReply{}, err
		}
	}
	m, err := insertMessage(ctx, tx, ss.ID, RoleUserMsg, content, s.unix())
	if err != nil {
		return MultiReply{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO message_formats VALUES(?,?)`, m.ID, isPrompt); err != nil {
		return MultiReply{}, err
	}
	for _, id := range uniq {
		a, e := s.runAgent(ctx, tx, u, ss, id)
		if e != nil {
			return MultiReply{}, e
		}
		r, e := scanRun(tx.QueryRowContext(ctx, runCols+` WHERE session_id=? AND agent_id=? AND status IN ('pending','running','waiting')`, ss.ID, id))
		kind := "steer"
		if errors.Is(e, ErrNotFound) {
			r, e = s.newRun(ctx, tx, ss, a, m.ID)
			kind = "new_run"
		}
		if e != nil {
			return MultiReply{}, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO message_deliveries(message_id,session_id,agent_id,agent_key,run_id,kind,status) VALUES(?,?,?,?,?,?,'pending')`, m.ID, ss.ID, id, id, r.ID, kind); e != nil {
			return MultiReply{}, e
		}
	}
	var prompt struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(content, &prompt)
	title := []rune(strings.TrimSpace(prompt.Prompt))
	if len(title) > 50 {
		title = title[:50]
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET status='idle',title=CASE WHEN title='' THEN ? ELSE title END,updated_at=? WHERE id=?`, string(title), s.unix(), ss.ID); err != nil {
		return MultiReply{}, err
	}
	if err = multiStatus(ctx, tx, ss.ID, s.unix()); err != nil {
		return MultiReply{}, err
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_requests VALUES(?,?,?,?,?)`, ss.ID, u.ID, key, digest, m.ID); err != nil {
			return MultiReply{}, err
		}
	}
	return s.replyFor(ctx, tx, m.ID)
}
