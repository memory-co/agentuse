package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
)

func schema3Database(t *testing.T, broken bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("testdata/schema3.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	seed := `INSERT INTO sessions(id,user_id,created_by,created_at,updated_at) VALUES('conversation','u','u',1,1);
 INSERT INTO messages(id,session_id,role,content,created_at) VALUES(7,'conversation','user','{"prompt":"pending"}',1),(9,'conversation','user','{"prompt":"accepted"}',2),(10,'conversation','assistant','{"output":"done"}',3);
 UPDATE messages SET reply_to_message_id=9 WHERE id=10;
 INSERT INTO run_prompts(id,session_id,message_id,agent_id,agent_version_id,daemon_id,harness,dispatch,status,run_id,input_id,client_session_id,content,created_at,updated_at) VALUES
 (17,'conversation',7,'reviewer','v1','device','codex','{"id":17,"conversation_id":"conversation","message_id":7,"agent_id":"reviewer","agent_version_id":"v1","content":"context\n\n# 本次任务\npending","skills":[],"work_token":"secret"}','sent','','','','{"prompt":"pending"}',1,1),
 (18,'conversation',9,'reviewer','v1','device','codex',NULL,'completed','run','input','native','{"prompt":"accepted"}',2,2);
 INSERT INTO runs(id,session_id,first_message_id,agent_id,agent_version_id,daemon_id,client_session_id,harness,harness_revision,status,result_message_id,created_at,updated_at) VALUES('run','conversation',9,'reviewer','v1','device','native','codex','revision','completed',10,2,3);
 INSERT INTO run_events(session_id,client_session_id,run_id,agent_id,agent_version_id,daemon_id,event_seq,kind,payload,created_at) VALUES('conversation','native','run','reviewer','v1','device',120,'accepted','{"type":"accepted","run_id":"run","input_id":"input"}',2);
 INSERT INTO work_run_tokens VALUES('token-hash','work',17);
 INSERT INTO run_notifications VALUES(9);
 INSERT INTO run_controls(id,session_id,client_session_id,run_id,daemon_id,kind,created_at,updated_at) VALUES('control','conversation','native','run','device','reply',2,2);`
	if _, err = db.Exec(seed); err != nil {
		t.Fatal(err)
	}
	if broken {
		if _, err = db.Exec(`UPDATE run_prompts SET dispatch='invalid' WHERE id=17`); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
func TestSchema3MigrationPreservesMessagesAndExecutionBindings(t *testing.T) {
	path := schema3Database(t, false)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var revision int
	if err = st.db.QueryRow(`PRAGMA user_version`).Scan(&revision); err != nil || revision != 4 {
		t.Fatal(revision, err)
	}
	var first, result string
	if err = st.db.QueryRow(`SELECT first_message_id,result_message_id FROM runs WHERE id='run'`).Scan(&first, &result); err != nil {
		t.Fatal(err)
	}
	if first != protocol.LegacyMessageID("conversation", 9) || result != protocol.LegacyMessageID("conversation", 10) {
		t.Fatal("run references lost", first, result)
	}
	var parent string
	var seq int
	if err = st.db.QueryRow(`SELECT reply_to_message_id,seq FROM messages WHERE id=?`, result).Scan(&parent, &seq); err != nil || parent != first || seq != 3 {
		t.Fatal(parent, seq, err)
	}
	var raw, message, agent string
	if err = st.db.QueryRow(`SELECT dispatch,message_id,agent_id FROM message_agents WHERE status='sent'`).Scan(&raw, &message, &agent); err != nil {
		t.Fatal(err)
	}
	var delivery protocol.ConversationMessage
	if err = json.Unmarshal([]byte(raw), &delivery); err != nil {
		t.Fatal(err)
	}
	if delivery.MessageID != protocol.LegacyMessageID("conversation", 7) || delivery.Context != "context" || delivery.Content != "pending" || delivery.WorkToken != "secret" {
		t.Fatal(delivery)
	}
	var authorized string
	if err = st.db.QueryRow(`SELECT message_id FROM work_run_tokens WHERE agent_id=?`, agent).Scan(&authorized); err != nil || authorized != message {
		t.Fatal("Work authorization lost", authorized, err)
	}
	var source string
	if err = st.db.QueryRow(`SELECT message_id FROM run_events WHERE kind='accepted'`).Scan(&source); err != nil || source != first {
		t.Fatal(source, err)
	}
	var kind string
	if err = st.db.QueryRow(`SELECT kind FROM run_controls WHERE id='a:control'`).Scan(&kind); err != nil || kind != "answer" {
		t.Fatal(kind, err)
	}
	var old int
	if err = st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('run_prompts','prompts_v3','messages_v3')`).Scan(&old); err != nil || old != 0 {
		t.Fatal("legacy business identities retained", old, err)
	}
}
func TestFailedMessageMigrationRollsBackHistory(t *testing.T) {
	path := schema3Database(t, true)
	if st, err := Open(path); err == nil {
		st.Close()
		t.Fatal("corrupt snapshot accepted")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revision, count int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&revision); err != nil || revision != 3 {
		t.Fatal(revision, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM messages WHERE id IN (7,9,10)`).Scan(&count); err != nil || count != 3 {
		t.Fatal("migration destroyed history", count, err)
	}
}
