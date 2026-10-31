package service

import (
	"context"
	"database/sql"
)

type rowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ReadSession returns one consistent view. A completed delivery must never be
// paired with messages read before its result was committed.
func (s *Service) ReadSession(ctx context.Context, u User, id string) (SessionDetail, error) {
	var detail SessionDetail
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return detail, err
	}
	defer tx.Rollback()
	if detail.Session, err = s.getSession(ctx, tx, u, id); err != nil {
		return detail, err
	}
	if detail.Messages, err = messages(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Runs, err = runs(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Deliveries, err = deliveries(ctx, tx, "session_id=?", id); err != nil {
		return detail, err
	}
	if detail.Events, err = runEvents(ctx, tx, id); err != nil {
		return detail, err
	}
	if detail.Questions, err = queryRunQuestions(ctx, tx, "session_id=?", id); err != nil {
		return detail, err
	}
	detail.RunCounts = map[string]int{}
	for _, run := range detail.Runs {
		detail.RunCounts[run.Status]++
	}
	return detail, tx.Commit()
}
