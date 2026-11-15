package database

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestTxRollback(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	stop := errors.New("abort business operation")
	err = st.Tx(ctx, func(tx *gorm.DB) error {
		if _, e := Exec(ctx, tx, `INSERT INTO users(id,name,role,token_hash,created_at) VALUES('rollback','rollback','user','hash',1)`); e != nil {
			return e
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	var count int
	if err = Row(ctx, st.Connection(), `SELECT COUNT(*) FROM users WHERE id='rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback: count=%d err=%v", count, err)
	}
}
