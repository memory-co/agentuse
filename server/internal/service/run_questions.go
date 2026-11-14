package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/memory-co/agentuse/internal/protocol"
)

var ErrQuestionClosed = errors.New("request is no longer awaiting a reply")

func (s *RunService) RunQuestions(ctx context.Context, conversation string) ([]Question, error) {
	return queryRunQuestions(ctx, s.db, `conversation_id=?`, conversation)
}

func queryRunQuestions(ctx context.Context, q rowsQuerier, where string, args ...any) ([]Question, error) {
	rows, err := q.QueryContext(ctx, `SELECT request_id,conversation_id,run_id,payload,state,answer,answered_by,created_at,COALESCE((SELECT error FROM run_controls c WHERE c.request_id=run_requests.request_id AND c.kind='answer' ORDER BY c.created_at DESC,c.rowid DESC LIMIT 1),'') FROM run_requests WHERE `+where+` ORDER BY created_at,request_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Question{}
	for rows.Next() {
		var r Question
		var raw string
		var answer, by sql.NullString
		if err = rows.Scan(&r.RequestID, &r.ConversationID, &r.RunID, &raw, &r.State, &answer, &by, &r.CreatedAt, &r.DeliveryError); err != nil {
			return nil, err
		}
		r.Payload = json.RawMessage(raw)
		if answer.Valid {
			r.Answer = json.RawMessage(answer.String)
		}
		r.AnsweredBy = by.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *RunService) ReplyRequest(ctx context.Context, u User, conversation, run, request string, answer json.RawMessage) error {
	r, err := s.GetRun(ctx, u, conversation, run)
	if err != nil {
		return err
	}
	if r.Status != "running" && r.Status != "waiting" {
		return ErrQuestionClosed
	}
	if !json.Valid(answer) {
		return fmt.Errorf("answer must be JSON")
	}
	var state string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM run_requests WHERE request_id=? AND run_id=?`, request, run).Scan(&state); err != nil {
		return err
	}
	if state != "pending" {
		return ErrQuestionClosed
	}
	_, err = s.queueControl(ctx, r, protocol.TypeAnswer, request, answer, u.ID)
	return err
}
