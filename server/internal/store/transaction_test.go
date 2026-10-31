package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestTxRollback(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	stop := errors.New("abort business operation")
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO users(id,name,role,token_hash,created_at) VALUES('rollback','rollback','user','hash',1)`); e != nil {
			return e
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	var count int
	if err = st.Connection().QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id='rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback: count=%d err=%v", count, err)
	}
}
