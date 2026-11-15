package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsOtherDatabaseSchemas(t *testing.T) {
	for _, revision := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11} {
		for _, table := range []string{"sessions", "conversations"} {
			t.Run(fmt.Sprintf("%d/%s", revision, table), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "unsupported.db")
				db, err := sql.Open("sqlite3", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.Exec(fmt.Sprintf("CREATE TABLE %s(id TEXT PRIMARY KEY); PRAGMA user_version=%d;", table, revision))
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
}

func TestConversationSchemaSeparatesConversationsAndNativeSessions(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var oldTables int
	var versions int64
	if err := st.db.Table("schema_migrations").Where("id=?", InitialMigration).Count(&versions).Error; err != nil || versions != 1 {
		t.Fatal(versions, err)
	}
	if err := st.db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('sessions','works','work_run_tokens','run_notifications','message_agents')").Row().Scan(&oldTables); err != nil || oldTables != 0 {
		t.Fatal("old Server table exists", oldTables, err)
	}
	for _, table := range []string{"messages", "attachments", "run_inputs", "runs", "run_events", "run_requests", "run_controls"} {
		var ownership, native, legacy int
		wantOwnership := 1
		if table == "run_inputs" {
			wantOwnership = 0
		}
		if err := st.db.Raw("SELECT count(*) FROM pragma_table_info(?) WHERE name='conversation_id'", table).Row().Scan(&ownership); err != nil || ownership != wantOwnership {
			t.Fatal("missing conversation ownership", table, err)
		}
		if err := st.db.Raw("SELECT count(*) FROM pragma_table_info(?) WHERE name='client_session_id'", table).Row().Scan(&legacy); err != nil || legacy != 0 {
			t.Fatal("old native session alias exists", table, err)
		}
		wantNative := 1
		if table == "messages" || table == "attachments" || table == "run_inputs" {
			wantNative = 0
		}
		if err := st.db.Raw("SELECT count(*) FROM pragma_table_info(?) WHERE name='session_id'", table).Row().Scan(&native); err != nil || native != wantNative {
			t.Fatal("incorrect Client session reference", table, err)
		}
	}
}
