package store

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// Projection queries use GORM binding, with database/sql scanners preserving
// nullable API fields and the original JSON payloads.
func Row(ctx context.Context, db *gorm.DB, query string, args ...any) Scanner {
	statement := db.WithContext(ctx).Raw(query, bindArgs(args)...)
	row := statement.Row()
	if statement.Error != nil {
		return scanFailure{statement.Error}
	}
	return row
}

type scanFailure struct{ err error }

func (s scanFailure) Scan(...any) error { return s.err }

func Rows(ctx context.Context, db *gorm.DB, query string, args ...any) (*sql.Rows, error) {
	return db.WithContext(ctx).Raw(query, bindArgs(args)...).Rows()
}

func Exec(ctx context.Context, db *gorm.DB, query string, args ...any) (int64, error) {
	result := db.WithContext(ctx).Exec(query, bindArgs(args)...)
	return result.RowsAffected, result.Error
}

func bindArgs(args []any) []any {
	args = append([]any(nil), args...)
	for i, value := range args {
		// Existing flags are integer columns on all backends.
		if flag, ok := value.(bool); ok {
			args[i] = 0
			if flag {
				args[i] = 1
			}
		}
	}
	return args
}

// Begin preserves the serialized metadata transaction boundary previously
// provided by SQLite IMMEDIATE. The database row lock also covers separate
// connections/processes; correctness never depends on a Go mutex.
func Begin(ctx context.Context, db *gorm.DB) (*gorm.DB, error) {
	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	if db.Dialector.Name() != "sqlite" {
		var id int
		if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT id FROM store_locks WHERE id=1 FOR UPDATE").Row().Scan(&id); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	return tx.Session(&gorm.Session{NewDB: true}), nil
}
