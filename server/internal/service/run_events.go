package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/server/internal/store"
	"strings"
)

func (s *Service) reportRunEvent(ctx context.Context, daemon, run, attempt string, seq int64, kind string, payload json.RawMessage, lease int64) error {
	if seq <= 0 || !json.Valid(payload) {
		return fmt.Errorf("event_seq 和 payload 不合法")
	}
	switch kind {
	case "started", EventProgress, EventPlan, EventSteered, EventQuestion, EventAnswered, EventQuestionExpired, EventDone, EventFailed:
	default:
		return fmt.Errorf("未知事件")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+store.ReportRunEventClause, run, daemon))
	if err != nil {
		return err
	}
	if r.AttemptID != attempt {
		return ErrStaleMessage
	}
	var oldKind, oldPayload string
	err = tx.QueryRowContext(ctx, store.ReportRunEventSelect, run, attempt, seq).Scan(&oldKind, &oldPayload)
	if err == nil {
		if oldKind != kind || oldPayload != string(payload) {
			return ErrStaleMessage
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = s.CheckRun(ctx, tx, daemon, run, attempt); err != nil {
		return err
	}
	now := s.unix()
	if _, err = tx.ExecContext(ctx, store.ReportRunEventInsert, r.SessionID, run, attempt, r.MessageID, daemon, seq, kind, string(payload), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, store.ReportRunEventUpdate, now+lease, run); err != nil {
		return err
	}
	switch kind {
	case "started":
		var p struct {
			ContextSeq int64 `json:"context_seq"`
		}
		_ = json.Unmarshal(payload, &p)
		if p.ContextSeq > 0 {
			if _, err = tx.ExecContext(ctx, store.ReportRunEventUpdate2, p.ContextSeq, r.SessionID, run); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, store.ReportRunEventUpdate3, run, attempt)
	case EventSteered:
		var p struct {
			SteerID int64 `json:"steer_id"`
		}
		if json.Unmarshal(payload, &p) != nil || p.SteerID <= 0 {
			return fmt.Errorf("steer_id 必填")
		}
		_, err = tx.ExecContext(ctx, store.ReportRunEventUpdate4, p.SteerID, run)
	case EventQuestion:
		var p struct {
			ToolUseID string          `json:"tool_use_id"`
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(payload, &p) != nil || p.ToolUseID == "" || len(p.Questions) == 0 || p.Questions[0] != '[' {
			return fmt.Errorf("提问格式错误")
		}
		_, err = tx.ExecContext(ctx, store.ReportRunEventInsert2, r.SessionID, run, attempt, r.MessageID, p.ToolUseID, string(p.Questions), now, now)
		if err == nil {
			_, err = tx.ExecContext(ctx, store.ReportRunEventUpdate5, run)
		}
	case EventAnswered, EventQuestionExpired:
		var p struct {
			ToolUseID string `json:"tool_use_id"`
		}
		_ = json.Unmarshal(payload, &p)
		status := "answered"
		if kind == EventQuestionExpired {
			status = "expired"
		}
		if kind == EventQuestionExpired {
			if err = s.recoverRunAnswers(ctx, tx, r, `tool_use_id=?`, p.ToolUseID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, store.ReportRunEventUpdate6, status, now, run, attempt, p.ToolUseID)
		if err == nil {
			_, err = tx.ExecContext(ctx, store.ReportRunEventUpdate7, run)
		}
	case EventDone, EventFailed:
		if err = s.recoverRunAnswers(ctx, tx, r, `1=1`); err != nil {
			return err
		}
		status := "succeeded"
		if kind == EventFailed {
			status = "failed"
		}
		err = s.finishRun(ctx, tx, r, status, payload, true)
	}
	if err != nil {
		return err
	}
	if err = multiStatus(ctx, tx, r.SessionID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) recoverRunAnswers(ctx context.Context, tx *sql.Tx, r Run, where string, args ...any) error {
	rows, err := tx.QueryContext(ctx, store.RecoverRunAnswersSelect+where, append([]any{r.ID, r.AttemptID}, args...)...)
	if err != nil {
		return err
	}
	var texts []string
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var answers map[string]string
		_ = json.Unmarshal([]byte(raw), &answers)
		texts = append(texts, answerText(answers))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, text := range texts {
		b, _ := json.Marshal(map[string]string{"prompt": text})
		m, e := insertMessage(ctx, tx, r.SessionID, RoleUserMsg, b, s.unix())
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, store.RecoverRunAnswersInsert, m.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, store.RecoverRunAnswersInsert2, m.ID, r.SessionID, nullIfEmpty(r.AgentID), r.AgentKey, r.ID); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) RunEvents(ctx context.Context, session string) ([]SessionEvent, error) {
	return runEvents(ctx, s.db, session)
}

func runEvents(ctx context.Context, q rowsQuerier, session string) ([]SessionEvent, error) {
	rows, err := q.QueryContext(ctx, store.RunEventsSelect, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionEvent{}
	for rows.Next() {
		var e SessionEvent
		var raw string
		if err = rows.Scan(&e.ID, &e.SessionID, &e.RunID, &e.AttemptID, &e.MessageID, &e.DaemonID, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) RunQuestions(ctx context.Context, session string) ([]Question, error) {
	return s.runQuestions(ctx, `session_id=?`, session)
}

func (s *Service) runQuestions(ctx context.Context, where string, args ...any) ([]Question, error) {
	return queryRunQuestions(ctx, s.db, where, args...)
}

func queryRunQuestions(ctx context.Context, q rowsQuerier, where string, args ...any) ([]Question, error) {
	rows, err := q.QueryContext(ctx, store.QueryRunQuestionsSelect+where+store.QueryRunQuestionsClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		var q Question
		var raw string
		var answers sql.NullString
		if err = rows.Scan(&q.ID, &q.SessionID, &q.RunID, &q.AttemptID, &q.MessageID, &q.ToolUseID, &raw, &answers, &q.AnsweredBy, &q.Status, &q.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &q.Questions)
		if answers.Valid {
			_ = json.Unmarshal([]byte(answers.String), &q.Answers)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Service) answerRunQuestion(ctx context.Context, u User, session, run string, qid int64, answers map[string]string) (Run, Question, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, Question{}, err
	}
	defer tx.Rollback()
	if _, err = s.getSession(ctx, tx, u, session); err != nil {
		return Run{}, Question{}, err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runCols+store.AnswerRunQuestionClause, run, session))
	if err != nil {
		return r, Question{}, err
	}
	if _, err = s.CheckRun(ctx, tx, r.DaemonID, run, r.AttemptID); err != nil {
		return r, Question{}, err
	}
	var raw string
	var q Question
	var stored sql.NullString
	err = tx.QueryRowContext(ctx, store.AnswerRunQuestionSelect, qid, run, r.AttemptID).Scan(&raw, &stored, &q.Status, &q.ToolUseID, &q.MessageID, &q.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, q, ErrNotFound
	}
	if err != nil {
		return r, q, err
	}
	if q.Status != "pending" || stored.Valid {
		return r, q, ErrQuestionClosed
	}
	var items []struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal([]byte(raw), &items)
	clean := map[string]string{}
	for _, item := range items {
		if strings.TrimSpace(answers[item.Question]) == "" {
			return r, q, fmt.Errorf("每个问题都需要答案")
		}
		clean[item.Question] = answers[item.Question]
	}
	b, _ := json.Marshal(clean)
	_, err = tx.ExecContext(ctx, store.AnswerRunQuestionUpdate, string(b), u.ID, s.unix(), qid)
	if err != nil {
		return r, q, err
	}
	q.ID = qid
	q.SessionID = session
	q.RunID = run
	q.AttemptID = r.AttemptID
	_ = json.Unmarshal([]byte(raw), &q.Questions)
	q.Answers = clean
	q.AnsweredBy = u.ID
	return r, q, tx.Commit()
}

func (s *Service) PendingRunAnswers(ctx context.Context, daemon string) ([]Question, error) {
	return s.runQuestions(ctx, store.PendingRunAnswersClause, daemon, s.unix())
}
