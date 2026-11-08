package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsOtherDatabaseSchemas(t *testing.T) {
	for _, revision := range []int{0, 1, 2, 3, 4, 5, 7} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsupported.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(fmt.Sprintf("CREATE TABLE sessions(id TEXT PRIMARY KEY); PRAGMA user_version=%d;", revision))
			db.Close()
			if err != nil {
				t.Fatal(err)
			}

			st, err := Open(path)
			if err == nil {
				st.Close()
				t.Fatal("unsupported database accepted")
			}
			if !strings.Contains(err.Error(), "unsupported database schema") || !strings.Contains(err.Error(), "reset the database") {
				t.Fatal("missing database reset instruction", err)
			}
		})
	}
}
