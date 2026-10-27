package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Question 是 harness 向用户提的一次问题(Claude Code 的 AskUserQuestion),见 docs/v2/designs/question.md。
type Question struct {
	ID         int64             `json:"id"`
	SessionID  string            `json:"session_id"`
	MessageID  int64             `json:"message_id"`
	ToolUseID  string            `json:"tool_use_id"`
	Questions  json.RawMessage   `json:"questions"`
	Answers    map[string]string `json:"answers,omitempty"`
	AnsweredBy string            `json:"answered_by,omitempty"`
	Status     string            `json:"status"` // pending / answered / expired
	CreatedAt  int64             `json:"created_at"`
}

const (
	QuestionPending  = "pending"
	QuestionAnswered = "answered"
	QuestionExpired  = "expired"
)

// ErrQuestionClosed 表示问题已经答过或已经作废，不能再答。
var ErrQuestionClosed = errors.New("这个问题已经回答过或已经过期")

// AnswerQuestion 记下用户对一个提问的答案。问题还是 pending,daemon 送到后才变成 answered。
// 返回会话(用来找 daemon 推答案)和问题。
func (s *Store) AnswerQuestion(ctx context.Context, u User, sessionID string, questionID int64, answers map[string]string) (Session, Question, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Question{}, err
	}
	defer tx.Rollback()
	ss, err := s.getSession(ctx, tx, u, sessionID)
	if err != nil {
		return Session{}, Question{}, err
	}
	q, err := scanQuestion(tx.QueryRowContext(ctx, questionCols+` WHERE id = ? AND session_id = ?`, questionID, ss.ID))
	if err != nil {
		return Session{}, Question{}, err
	}
	if q.Status != QuestionPending || q.Answers != nil {
		return Session{}, Question{}, ErrQuestionClosed
	}
	var items []struct {
		Question string `json:"question"`
	}
	_ = json.Unmarshal(q.Questions, &items)
	clean := map[string]string{}
	for _, it := range items {
		a := strings.TrimSpace(answers[it.Question])
		if a == "" {
			return Session{}, Question{}, fmt.Errorf("问题 %q 没有答案", it.Question)
		}
		clean[it.Question] = a
	}
	b, _ := json.Marshal(clean)
	if _, err := tx.ExecContext(ctx, `UPDATE questions SET answers = ?, answered_by = ?, updated_at = ? WHERE id = ?`,
		string(b), u.ID, s.unix(), q.ID); err != nil {
		return Session{}, Question{}, err
	}
	q.Answers, q.AnsweredBy = clean, u.ID
	return ss, q, tx.Commit()
}

// expireQuestions 把符合条件的 pending 提问作废。已经答了、还没送到的答案转成一条插话,
// 返回这些新插话(能推就推给 daemon,不然这一轮结束时转成下一条消息)。
func expireQuestions(ctx context.Context, tx *sql.Tx, where string, now int64, args ...any) ([]Steer, error) {
	rows, err := tx.QueryContext(ctx,
		`UPDATE questions SET status = 'expired', updated_at = ? WHERE status = 'pending' AND `+where+`
		 RETURNING id, session_id, message_id, answers, answered_by`, append([]any{now}, args...)...)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, messageID       int64
		sessionID           string
		answers, answeredBy sql.NullString
	}
	var expired []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.sessionID, &r.messageID, &r.answers, &r.answeredBy); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].id < expired[j].id })
	var out []Steer
	for _, r := range expired {
		if !r.answers.Valid || !r.answeredBy.Valid {
			continue
		}
		var answers map[string]string
		_ = json.Unmarshal([]byte(r.answers.String), &answers)
		st := Steer{SessionID: r.sessionID, MessageID: r.messageID, CreatedBy: r.answeredBy.String, Prompt: answerText(answers), Status: SteerPending, CreatedAt: now}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO steers (session_id, message_id, created_by, prompt, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			st.SessionID, st.MessageID, st.CreatedBy, st.Prompt, st.Status, now, now)
		if err != nil {
			return nil, err
		}
		st.ID, _ = res.LastInsertId()
		out = append(out, st)
	}
	return out, nil
}

// answerText 把没送到的答案写成一段插话。
func answerText(answers map[string]string) string {
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("我对你之前提问的回答：")
	for _, k := range keys {
		b.WriteString("\n" + k + " → " + answers[k])
	}
	return b.String()
}

const questionCols = `SELECT id, session_id, message_id, tool_use_id, questions, answers, answered_by, status, created_at FROM questions`

func scanQuestion(row scanner) (Question, error) {
	var q Question
	var questions string
	var answers, by sql.NullString
	err := row.Scan(&q.ID, &q.SessionID, &q.MessageID, &q.ToolUseID, &questions, &answers, &by, &q.Status, &q.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Question{}, ErrNotFound
	}
	if err != nil {
		return Question{}, err
	}
	q.Questions = json.RawMessage(questions)
	if answers.Valid {
		_ = json.Unmarshal([]byte(answers.String), &q.Answers)
	}
	q.AnsweredBy = by.String
	return q, nil
}

func (s *Store) queryQuestions(ctx context.Context, query string, args ...any) ([]Question, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Questions 列会话里的所有提问。
func (s *Store) Questions(ctx context.Context, sessionID string) ([]Question, error) {
	return s.queryQuestions(ctx, questionCols+` WHERE session_id = ? ORDER BY id`, sessionID)
}

// PendingAnswers 列这台 daemon 上答了、还没送到的提问(daemon 重连后再推一遍)。
func (s *Store) PendingAnswers(ctx context.Context, daemonID string) ([]Question, error) {
	return s.queryQuestions(ctx, questionCols+` WHERE status = 'pending' AND answers IS NOT NULL
		AND session_id IN (SELECT id FROM sessions WHERE daemon_id = ? AND status = 'waiting') ORDER BY id`, daemonID)
}
