package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/memory-co/agentuse/internal/protocol"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/store"
)

func (s *Service) RunQuestions(ctx context.Context, conversation string) ([]agentuse.Question, error) {
	return s.QuestionsIn(ctx, s.DB, `conversation_id=?`, conversation)
}

func (s *Service) QuestionsIn(ctx context.Context, q *gorm.DB, where string, args ...any) ([]agentuse.Question, error) {
	rows, err := store.Rows(ctx, q, `SELECT request_id,conversation_id,run_id,payload,state,answer,answered_by,created_at,COALESCE((SELECT error FROM run_controls c WHERE c.request_id=run_requests.request_id AND c.kind='answer' ORDER BY c.created_at DESC,c.seq DESC LIMIT 1),'') FROM run_requests WHERE `+where+` ORDER BY created_at,request_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.Question{}
	for rows.Next() {
		var r agentuse.Question
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

func (s *Service) ReplyRequest(ctx context.Context, u agentuse.User, conversation, run, request string, answer json.RawMessage) error {
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
	if err = store.Row(ctx, s.DB, `SELECT state FROM run_requests WHERE request_id=? AND run_id=?`, request, run).Scan(&state); err != nil {
		return err
	}
	if state != "pending" {
		return ErrQuestionClosed
	}
	_, err = s.queueControl(ctx, r, protocol.TypeAnswer, request, answer, u.ID)
	return err
}

var ErrQuestionClosed = errors.New("request is no longer awaiting a reply")
