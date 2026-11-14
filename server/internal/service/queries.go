package service

import (
	"context"
	"database/sql"
)

// Query interfaces accept both the shared database and an orchestration tx.
type scanner interface{ Scan(...any) error }
type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type rowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type queryReader interface {
	querier
	rowsQuerier
}
