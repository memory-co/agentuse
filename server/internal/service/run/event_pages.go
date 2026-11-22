package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
)

const DefaultEventPageSize = 20
const MaxEventPageSize = 50
const eventPreviewChars = 4096

func (s *Service) authorizeEventRead(ctx context.Context, u agentuse.User, conversation, runID string) error {
	if _, err := s.ConversationService.GetConversation(ctx, u, conversation); err != nil {
		return err
	}
	var exists int
	err := database.Row(ctx, s.DB, `SELECT 1 FROM runs WHERE conversation_id=? AND id=?`, conversation, runID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return database.ErrNotFound
	}
	return err
}

func (s *Service) PageEvents(ctx context.Context, u agentuse.User, conversation, runID string, after int64, limit int) (agentuse.RunEventPage, error) {
	page := agentuse.RunEventPage{Events: []agentuse.RunEventPreview{}}
	if after < 0 || limit < 1 || limit > MaxEventPageSize {
		return page, fmt.Errorf("invalid run event page")
	}
	if err := s.authorizeEventRead(ctx, u, conversation, runID); err != nil {
		return page, err
	}
	// Bound each projection before reading it from the database. One extra
	// row detects the end of the page without counting or reading the tail.
	rows, err := database.Rows(ctx, s.DB, `SELECT id,kind,created_at,SUBSTR(payload,1,?) FROM run_events
		WHERE conversation_id=? AND run_id=? AND id>? ORDER BY id LIMIT ?`,
		eventPreviewChars+1, conversation, runID, after, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(page.Events) == limit {
			cursor := page.Events[limit-1].ID
			page.NextCursor = &cursor
			break
		}
		var id int64
		var raw string
		var event agentuse.RunEventPreview
		if err := rows.Scan(&id, &event.Kind, &event.CreatedAt, &raw); err != nil {
			return page, err
		}
		event.ID = strconv.FormatInt(id, 10)
		if utf8.RuneCountInString(raw) > eventPreviewChars {
			_, size := utf8.DecodeLastRuneInString(raw)
			event.PayloadPreview = raw[:len(raw)-size]
		} else {
			event.Payload = json.RawMessage(raw)
		}
		page.Events = append(page.Events, event)
	}
	return page, rows.Err()
}

func (s *Service) EventPayload(ctx context.Context, u agentuse.User, conversation, runID string, id int64) (string, error) {
	if err := s.authorizeEventRead(ctx, u, conversation, runID); err != nil {
		return "", err
	}
	var payload string
	err := database.Row(ctx, s.DB, `SELECT payload FROM run_events WHERE conversation_id=? AND run_id=? AND id=?`, conversation, runID, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", database.ErrNotFound
	}
	return payload, err
}
