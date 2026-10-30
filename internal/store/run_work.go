package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) CreateMultiWork(ctx context.Context, u User, name, agentID, instructions string) (Work, Session, error) {
	name, err := cleanWorkName(name)
	if err != nil {
		return Work{}, Session{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, Session{}, err
	}
	defer tx.Rollback()
	a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, agentID, u.ID))
	if err != nil {
		return Work{}, Session{}, err
	}
	if err = runRuntime(ctx, tx, a); err != nil {
		return Work{}, Session{}, err
	}
	now := s.unix()
	w := Work{ID: randomID("wk_"), UserID: u.ID, Name: name, Instructions: instructions, CoordinatorID: randomID("ss_"), CoordinatorAgentID: agentID, CreatedAt: now, UpdatedAt: now}
	if _, err = tx.ExecContext(ctx, `INSERT INTO works(id,user_id,name,instructions,coordinator_id,coordinator_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, w.ID, u.ID, name, instructions, w.CoordinatorID, agentID, now, now); err != nil {
		return w, Session{}, err
	}
	ss, err := s.insertMulti(ctx, tx, u, w.ID, name)
	if err != nil {
		return w, ss, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE works SET coordinator_id=? WHERE id=?`, ss.ID, w.ID); err != nil {
		return w, ss, err
	}
	w.CoordinatorID = ss.ID
	ss.WorkRole = WorkCoordinator
	return w, ss, tx.Commit()
}
func (s *Store) WorkRunByToken(ctx context.Context, token string) (Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, runCols+` WHERE id=(SELECT run_id FROM work_run_tokens t WHERE t.token_hash=? AND t.attempt_id=session_runs.attempt_id) AND status IN ('running','waiting') AND lease_until>?`, HashSecret(token), s.unix()))
}
func (s *Store) runWorkToken(ctx context.Context, token string) (Work, User, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT t.work_id FROM work_run_tokens t JOIN session_runs r ON r.id=t.run_id WHERE t.token_hash=? AND t.attempt_id=r.attempt_id AND r.status IN ('running','waiting') AND r.lease_until>?`, HashSecret(token), s.unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, User{}, ErrNotFound
	}
	if err != nil {
		return Work{}, User{}, err
	}
	w, err := scanWork(s.db.QueryRowContext(ctx, workCols+` WHERE w.id=?`, id))
	if err != nil {
		return w, User{}, err
	}
	u, err := s.GetUser(ctx, w.UserID)
	return w, u, err
}
func (s *Store) notifyRunMessages(ctx context.Context, tx *sql.Tx, session string) error {
	ss, err := s.getSession(ctx, tx, User{Role: RoleAdmin}, session)
	if err != nil {
		return err
	}
	if ss.WorkRole != WorkThread {
		return nil
	}
	ds, err := deliveries(ctx, tx, `session_id=?`, session)
	if err != nil {
		return err
	}
	groups := map[int64][]Delivery{}
	var ids []int64
	for _, d := range ds {
		if _, ok := groups[d.MessageID]; !ok {
			ids = append(ids, d.MessageID)
		}
		groups[d.MessageID] = append(groups[d.MessageID], d)
	}
	for _, id := range ids {
		ready := true
		for _, d := range groups[id] {
			if d.Status != "succeeded" && d.Status != "failed" && d.Status != "cancelled" {
				ready = false
			}
		}
		if !ready {
			continue
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO run_notifications VALUES(?) ON CONFLICT DO NOTHING`, id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		var summary strings.Builder
		for _, d := range groups[id] {
			fmt.Fprintf(&summary, "\n@%s: %s", d.AgentKey, d.Status)
			if d.RunID != "" {
				var raw sql.NullString
				if e = tx.QueryRowContext(ctx, `SELECT m.content FROM session_runs r LEFT JOIN messages m ON m.id=r.result_message_id WHERE r.id=?`, d.RunID).Scan(&raw); e != nil {
					return e
				}
				if raw.Valid {
					var v struct{ Output, Error string }
					_ = json.Unmarshal([]byte(raw.String), &v)
					text := v.Output
					if text == "" {
						text = v.Error
					}
					runes := []rune(text)
					if len(runes) > 4000 {
						runes = runes[:4000]
					}
					summary.WriteString("\n" + string(runes))
				}
			}
			if d.Error != "" {
				summary.WriteString("\n" + d.Error)
			}
		}
		wk, e := getWork(ctx, tx, User{Role: RoleAdmin}, ss.WorkID)
		if e != nil {
			return e
		}
		coord, e := s.getSession(ctx, tx, User{Role: RoleAdmin}, wk.CoordinatorID)
		if e != nil {
			return e
		}
		if coord.Mode != MultiAgent {
			b, _ := json.Marshal(map[string]any{"ok": true, "output": summary.String()})
			if _, e = s.notifyCoordinator(ctx, tx, ss.ID, b, s.unix()); e != nil {
				return e
			}
			continue
		}
		b, _ := json.Marshal(map[string]string{"prompt": fmt.Sprintf("[线程 %s「%s」回复了]\n%s", ss.ID, ss.Title, summary.String()), "from_session": ss.ID})
		a, e := s.runAgent(ctx, tx, User{ID: wk.UserID}, coord, wk.CoordinatorAgentID)
		if e == nil {
			e = runRuntime(ctx, tx, a)
		}
		if e != nil {
			if _, e = insertMessage(ctx, tx, coord.ID, RoleUserMsg, b, s.unix()); e != nil {
				return e
			}
			continue
		}
		if _, e = s.sendMultiTx(ctx, tx, User{ID: wk.UserID}, coord, b, []string{wk.CoordinatorAgentID}, "", true); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) UpgradeSession(ctx context.Context, u User, id string) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, id)
	if err != nil {
		return ss, err
	}
	ids := []string{id}
	if ss.WorkID != "" {
		if _, err = getWork(ctx, tx, u, ss.WorkID); err != nil {
			return ss, err
		}
		rows, e := tx.QueryContext(ctx, `SELECT id FROM sessions WHERE work_id=?`, ss.WorkID)
		if e != nil {
			return ss, e
		}
		ids = nil
		for rows.Next() {
			var sid string
			if e = rows.Scan(&sid); e != nil {
				rows.Close()
				return ss, e
			}
			ids = append(ids, sid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ss, err
		}
	}
	for _, sid := range ids {
		old, e := s.getSession(ctx, tx, User{Role: RoleAdmin}, sid)
		if e != nil {
			return ss, e
		}
		if old.Mode == MultiAgent {
			continue
		}
		if e = s.upgradeLegacySession(ctx, tx, old); e != nil {
			return ss, e
		}
	}
	ss, err = s.getSession(ctx, tx, u, id)
	if err != nil {
		return ss, err
	}
	return ss, tx.Commit()
}
func (s *Store) upgradeLegacySession(ctx context.Context, tx *sql.Tx, old Session) error {
	if old.Status != "idle" && old.Status != "resolved" {
		return ErrSessionBusy
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM steers WHERE session_id=? AND status='pending')+(SELECT COUNT(*) FROM questions WHERE session_id=? AND status='pending')`, old.ID, old.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return ErrSessionBusy
	}
	if old.WorkRole == WorkCoordinator {
		var selected sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT coordinator_agent_id FROM works WHERE id=?`, old.WorkID).Scan(&selected); err != nil {
			return err
		}
		agent := selected.String
		if agent == "" {
			agent = old.AgentID
		}
		a, err := scanAgent(tx.QueryRowContext(ctx, agentCols+` WHERE id=? AND user_id=?`, agent, old.UserID))
		if err != nil {
			return fmt.Errorf("协调 agent 已删除，请先配置有效 agent")
		}
		if err = runRuntime(ctx, tx, a); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE works SET coordinator_agent_id=? WHERE id=?`, a.ID, old.WorkID); err != nil {
			return err
		}
	}
	for _, owner := range []string{old.UserID, old.CreatedBy} {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_access VALUES(?,?)`, old.ID, owner); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, messageCols+` WHERE session_id=? ORDER BY id`, old.ID)
	if err != nil {
		return err
	}
	var history []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		history = append(history, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	key := old.AgentID
	if key == "" {
		key = "deleted-agent"
	}
	name := key
	_ = tx.QueryRowContext(ctx, `SELECT name FROM agents WHERE id=?`, old.AgentID).Scan(&name)
	var userMessage int64
	for _, m := range history {
		if m.Role == RoleUserMsg {
			userMessage = m.ID
			continue
		}
		if userMessage == 0 {
			continue
		}
		var result struct {
			OK    bool
			Error string
		}
		_ = json.Unmarshal(m.Content, &result)
		status := "failed"
		if result.OK {
			status = "succeeded"
		}
		rid := randomID("sr_")
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_runs(id,session_id,message_id,agent_id,agent_key,agent_name,agent_user_id,daemon_id,harness,status,result_message_id,error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, rid, old.ID, userMessage, nullIfEmpty(old.AgentID), key, name, old.UserID, nullIfEmpty(old.DaemonID), old.Harness, status, m.ID, result.Error, m.CreatedAt, m.CreatedAt); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_authors VALUES(?,?,?,?)`, m.ID, rid, key, name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_deliveries(message_id,session_id,agent_id,agent_key,run_id,kind,status) VALUES(?,?,?,?,?,'new_run',?)`, userMessage, old.ID, nullIfEmpty(old.AgentID), key, rid, status); err != nil {
			return err
		}
	}
	if err = s.importLegacyRunDetails(ctx, tx, old); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sessions SET mode='multi_agent',user_id=created_by,agent_id=NULL,daemon_id=NULL,harness_session_id=NULL,lease_until=NULL WHERE id=?`, old.ID)
	return err
}

// Existing message IDs remain stable; legacy supplements are appended with explicit source references.
func (s *Store) importLegacyRunDetails(ctx context.Context, tx *sql.Tx, ss Session) error {
	type steer struct {
		id, message, at int64
		prompt          string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,message_id,prompt,created_at FROM steers WHERE session_id=? AND status='delivered' ORDER BY id`, ss.ID)
	if err != nil {
		return err
	}
	var steers []steer
	for rows.Next() {
		var v steer
		if err = rows.Scan(&v.id, &v.message, &v.prompt, &v.at); err != nil {
			rows.Close()
			return err
		}
		steers = append(steers, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range steers {
		b, _ := json.Marshal(map[string]any{"prompt": fmt.Sprintf("[历史插话，关联消息 %d] %s", v.message, v.prompt), "legacy_steer_id": v.id, "from_message_id": v.message})
		m, e := insertMessage(ctx, tx, ss.ID, RoleUserMsg, b, v.at)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO message_formats VALUES(?,1)`, m.ID); e != nil {
			return e
		}
	}
	type question struct {
		id, message, created, updated int64
		tool, raw, by, status         string
		answers                       sql.NullString
	}
	qs, err := tx.QueryContext(ctx, `SELECT id,message_id,tool_use_id,questions,answers,COALESCE(answered_by,''),status,created_at,updated_at FROM questions WHERE session_id=? ORDER BY id`, ss.ID)
	if err != nil {
		return err
	}
	var questions []question
	for qs.Next() {
		var v question
		if err = qs.Scan(&v.id, &v.message, &v.tool, &v.raw, &v.answers, &v.by, &v.status, &v.created, &v.updated); err != nil {
			qs.Close()
			return err
		}
		questions = append(questions, v)
	}
	err = qs.Err()
	qs.Close()
	if err != nil {
		return err
	}
	for _, v := range questions {
		var run string
		e := tx.QueryRowContext(ctx, `SELECT id FROM session_runs WHERE session_id=? AND message_id=? ORDER BY created_at,id LIMIT 1`, ss.ID, v.message).Scan(&run)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO run_questions(session_id,run_id,attempt_id,message_id,tool_use_id,questions,answers,answered_by,status,created_at,updated_at) VALUES(?,?,'legacy',?,?,?,?,?,?,?,?)`, ss.ID, run, v.message, v.tool, v.raw, v.answers, nullIfEmpty(v.by), v.status, v.created, v.updated); e != nil {
			return e
		}
		if v.answers.Valid {
			var answers map[string]string
			_ = json.Unmarshal([]byte(v.answers.String), &answers)
			b, _ := json.Marshal(map[string]any{"prompt": fmt.Sprintf("[历史问答，关联消息 %d]\n%s", v.message, answerText(answers)), "legacy_question_id": v.id, "from_message_id": v.message})
			if _, e = insertMessage(ctx, tx, ss.ID, RoleUserMsg, b, v.updated); e != nil {
				return e
			}
		}
	}
	type event struct {
		id, message, at       int64
		daemon, kind, payload string
	}
	ev, err := tx.QueryContext(ctx, `SELECT id,message_id,daemon_id,kind,COALESCE(payload,'null'),created_at FROM session_events WHERE session_id=? ORDER BY id`, ss.ID)
	if err != nil {
		return err
	}
	var events []event
	for ev.Next() {
		var v event
		if err = ev.Scan(&v.id, &v.message, &v.daemon, &v.kind, &v.payload, &v.at); err != nil {
			ev.Close()
			return err
		}
		events = append(events, v)
	}
	err = ev.Err()
	ev.Close()
	if err != nil {
		return err
	}
	for _, v := range events {
		var run string
		e := tx.QueryRowContext(ctx, `SELECT id FROM session_runs WHERE session_id=? AND message_id=? ORDER BY created_at,id LIMIT 1`, ss.ID, v.message).Scan(&run)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO run_events(session_id,run_id,attempt_id,message_id,daemon_id,event_seq,kind,payload,created_at) VALUES(?,?,'legacy',?,?,?,?,?,?)`, ss.ID, run, v.message, v.daemon, v.id, v.kind, v.payload, v.at); e != nil {
			return e
		}
	}
	return nil
}
