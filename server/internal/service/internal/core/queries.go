package core

import (
	"context"
	"database/sql"
)

// Query interfaces accept both the shared database and an orchestration tx.
type Scanner interface{ Scan(...any) error }
type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type RowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type QueryReader interface {
	Querier
	RowsQuerier
}
