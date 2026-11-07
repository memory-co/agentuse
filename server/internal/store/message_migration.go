package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
)

// migrateMessages preserves schema-3 history while replacing prompt identities
// with the saved message/Agent pair. All reference rewrites commit together.
func migrateMessages(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE messages RENAME TO messages_v3`,
		`ALTER TABLE runs RENAME TO runs_v3`,
		`ALTER TABLE run_prompts RENAME TO prompts_v3`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`ALTER TABLE work_run_tokens RENAME TO work_tokens_v3`,
		`ALTER TABLE run_notifications RENAME TO notifications_v3`,
		`DROP INDEX IF EXISTS messages_session`, `DROP INDEX IF EXISTS runs_conversation`,
		`ALTER TABLE run_events ADD COLUMN message_id TEXT NOT NULL DEFAULT ''`,
		schema + versionSchema + attachmentSchema + runSchema,
		`CREATE TEMP TABLE message_id_map(old_id INTEGER PRIMARY KEY,new_id TEXT NOT NULL)`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,session_id FROM messages_v3 ORDER BY id`)
	if err != nil {
		return err
	}
	type identity struct {
		id           int64
		conversation string
	}
	var old []identity
	for rows.Next() {
		var v identity
		if err = rows.Scan(&v.id, &v.conversation); err != nil {
			rows.Close()
			return err
		}
		old = append(old, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range old {
		if _, err = tx.ExecContext(ctx, `INSERT INTO message_id_map VALUES(?,?)`, v.id, protocol.LegacyMessageID(v.conversation, v.id)); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`INSERT INTO messages(id,seq,session_id,role,content,author_user_id,author_agent_id,run_agent_ids,reply_to_message_id,author_agent_version_id,created_at)
   SELECT m.new_id,ROW_NUMBER() OVER(PARTITION BY o.session_id ORDER BY o.id),o.session_id,o.role,o.content,o.author_user_id,o.author_agent_id,o.run_agent_ids,parent.new_id,o.author_agent_version_id,o.created_at
   FROM messages_v3 o JOIN message_id_map m ON m.old_id=o.id LEFT JOIN message_id_map parent ON parent.old_id=o.reply_to_message_id`,
		`INSERT INTO runs SELECT r.id,r.session_id,first.new_id,r.agent_id,r.agent_version_id,r.daemon_id,r.client_session_id,r.harness,r.harness_revision,r.status,result.new_id,r.output,r.error,r.result,r.created_at,r.updated_at FROM runs_v3 r JOIN message_id_map first ON first.old_id=r.first_message_id LEFT JOIN message_id_map result ON result.old_id=r.result_message_id`,
		`INSERT INTO message_agents(session_id,message_id,agent_id,agent_version_id,daemon_id,harness,status,run_id,input_id,client_session_id,error,created_at,updated_at)
   SELECT p.session_id,m.new_id,p.agent_id,p.agent_version_id,p.daemon_id,p.harness,p.status,p.run_id,p.input_id,p.client_session_id,p.error,p.created_at,p.updated_at FROM prompts_v3 p JOIN message_id_map m ON m.old_id=p.message_id`,
		`INSERT INTO work_run_tokens SELECT t.token_hash,t.work_id,m.new_id,p.agent_id FROM work_tokens_v3 t JOIN prompts_v3 p ON p.id=t.prompt_id JOIN message_id_map m ON m.old_id=p.message_id`,
		`INSERT INTO run_notifications SELECT m.new_id FROM notifications_v3 n JOIN message_id_map m ON m.old_id=n.message_id`,
		`UPDATE run_events SET message_id=COALESCE((SELECT p.message_id FROM message_agents p WHERE p.run_id=run_events.run_id AND p.input_id=json_extract(run_events.payload,'$.input_id') AND p.daemon_id=run_events.daemon_id),'') WHERE kind='accepted'`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	// Preserve already-published payloads, without embedded mutable configuration.
	rows, err = tx.QueryContext(ctx, `SELECT m.new_id,p.agent_id,p.dispatch FROM prompts_v3 p JOIN message_id_map m ON m.old_id=p.message_id WHERE p.dispatch IS NOT NULL`)
	if err != nil {
		return err
	}
	type snapshot struct{ message, agent, raw string }
	var snapshots []snapshot
	for rows.Next() {
		var v snapshot
		if err = rows.Scan(&v.message, &v.agent, &v.raw); err != nil {
			rows.Close()
			return err
		}
		snapshots = append(snapshots, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range snapshots {
		var old struct {
			ConversationID string                     `json:"conversation_id"`
			AgentID        string                     `json:"agent_id"`
			AgentVersionID string                     `json:"agent_version_id"`
			Content        string                     `json:"content"`
			Attachments    []protocol.LocalAttachment `json:"attachments"`
			WorkID         string                     `json:"work_id"`
			WorkToken      string                     `json:"work_token"`
		}
		if err = json.Unmarshal([]byte(v.raw), &old); err != nil {
			return fmt.Errorf("invalid published message snapshot: %w", err)
		}
		var rawContent string
		if err = tx.QueryRowContext(ctx, `SELECT content FROM messages WHERE id=?`, v.message).Scan(&rawContent); err != nil {
			return err
		}
		var input protocol.PromptInput
		json.Unmarshal([]byte(rawContent), &input)
		if input.Prompt == "" {
			input.Prompt = rawContent
		}
		context := ""
		suffix := "\n\n# 本次任务\n" + input.Prompt
		if strings.HasSuffix(old.Content, suffix) {
			context = strings.TrimSuffix(old.Content, suffix)
		}
		m := protocol.ConversationMessage{ConversationID: old.ConversationID, MessageID: v.message, AgentID: old.AgentID, AgentVersionID: old.AgentVersionID, Content: input.Prompt, Context: context, Attachments: old.Attachments, WorkID: old.WorkID, WorkToken: old.WorkToken}
		raw, e := json.Marshal(m)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE message_agents SET dispatch=? WHERE message_id=? AND agent_id=?`, string(raw), v.message, v.agent); err != nil {
			return err
		}
	}
	for _, q := range []string{`UPDATE run_controls SET id=(CASE kind WHEN 'reply' THEN 'a:' ELSE 'c:' END)||id,kind=CASE kind WHEN 'reply' THEN 'answer' ELSE kind END WHERE id NOT LIKE 'a:%' AND id NOT LIKE 'c:%'`,
		`DROP TABLE prompts_v3`, `DROP TABLE runs_v3`, `DROP TABLE messages_v3`, `DROP TABLE work_tokens_v3`, `DROP TABLE notifications_v3`, `DROP TABLE message_id_map`, `PRAGMA user_version=4`} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
