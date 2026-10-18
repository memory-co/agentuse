package store_test

import (
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/internal/models"

	"github.com/memory-co/agentuse/internal/store"
	"github.com/memory-co/agentuse/internal/store/storetest"
)

func TestMemory(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return store.NewMemory() })
}

func TestSQLite(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "hub.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	})
}

// 重开同一个文件,数据还在。
func TestSQLiteReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "hub.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(ctx, models.Run{ID: "r1", SessionID: "s1", Platform: "shop", Status: models.RunQueued}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if r, err := st.GetRun(ctx, "r1"); err != nil || r.Status != models.RunQueued {
		t.Fatalf("run gone after reopen: %+v %v", r, err)
	}
}
