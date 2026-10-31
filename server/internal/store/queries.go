// SQL statements and fragments used by service transactions.
package store

const CheckRuntimeSelect = `SELECT 1 FROM daemon_harnesses WHERE daemon_id = ? AND harness = ? AND user_id = ?`

const RuntimeErrSelect = `SELECT available, enabled FROM daemon_harnesses WHERE daemon_id = ? AND harness = ?`

const CreateAgentInsert = `INSERT INTO agents (id, user_id, name, daemon_id, harness, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

const UpdateAgentUpdate = `UPDATE agents SET name = ?, daemon_id = ?, harness = ?, description = ?, updated_at = ? WHERE id = ?`

const DeleteAgentDelete = `DELETE FROM agents WHERE id = ?`

const DeleteAgentClause = ` AND user_id = ?`

const AgentCols = `SELECT id, user_id, name, daemon_id, harness, description, created_at, updated_at FROM agents`

const GetAgentClause = ` WHERE id = ?`

const GetAgentClause2 = ` AND user_id = ?`

const ListAgentsClause = ` WHERE user_id = ?`

const ListAgentsClause2 = ` ORDER BY name, id`

const AttachmentSessionSelect = `SELECT user_id FROM daemons WHERE id=? AND key_hash=?`

const AttachmentSessionSelect2 = `SELECT COUNT(*) FROM work_run_tokens t JOIN session_runs r ON r.id=t.run_id WHERE t.work_id=? AND t.token_hash=? AND t.attempt_id=r.attempt_id AND r.status IN ('running','waiting') AND r.lease_until>?`

const AttachmentSessionSelect3 = `SELECT id,role FROM users WHERE token_hash=?`

const CommitAttachmentSelect = `SELECT COALESCE(SUM(size_bytes),0) FROM attachments WHERE session_id=?`

const CommitAttachmentInsert = `INSERT INTO attachments(id,session_id,filename,content_type,size_bytes,sha256,created_by,source,daemon_id,created_at,created_by_run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`

const AttachmentCols = `SELECT id,session_id,filename,content_type,size_bytes,sha256,created_by,source,COALESCE(daemon_id,''),created_at,COALESCE(created_by_run_id,''),COALESCE((SELECT agent_key FROM session_runs r WHERE r.id=attachments.created_by_run_id),'') FROM attachments`

const GetAttachmentClause = ` WHERE session_id=? AND id=?`

const ListAttachmentsClause = ` WHERE session_id=? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`

const DeleteAttachmentDelete = `DELETE FROM attachments WHERE session_id=? AND id=?`

const AttachmentExistsSelect = `SELECT COUNT(*) FROM attachments WHERE session_id=? AND id=?`

const DaemonCapabilitiesSelect = `SELECT capability FROM daemon_capabilities WHERE daemon_id=? ORDER BY capability`

const CreateDaemonInsert = `INSERT INTO daemons (id, user_id, name, setup_token, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`

const RegisterDaemonSelect = `SELECT id, user_id, name FROM daemons WHERE setup_token = ?`

const RegisterDaemonUpdate = `UPDATE daemons SET name = ?, key_hash = ?, setup_token = NULL, status = ? WHERE id = ?`

const ResetDaemonSetupSelect = `SELECT 1 FROM daemons WHERE id=?`

const ResetDaemonSetupClause = ` AND user_id=?`

const ResetDaemonSetupUpdate = `UPDATE daemons SET setup_token=?,key_hash=NULL,status=? WHERE id=?`

const DaemonByKeyClause = ` WHERE key_hash = ?`

const DaemonCols = `SELECT id, user_id, name, setup_token, status, last_seen_at, created_at FROM daemons`

const GetDaemonClause = ` WHERE id = ?`

const GetDaemonClause2 = ` AND user_id = ?`

const GetDaemonClause3 = `WHERE daemon_id = ?`

const ListDaemonsClause = ` WHERE user_id = ?`

const ListDaemonsClause2 = ` ORDER BY created_at`

const ListDaemonsClause3 = `WHERE user_id = ?`

const DaemonHarnessesSelect = `SELECT daemon_id, user_id, harness, version, available, enabled, updated_at FROM daemon_harnesses `

const DaemonHarnessesClause = ` ORDER BY harness`

const DeleteDaemonDelete = `DELETE FROM daemons WHERE id = ?`

const DeleteDaemonClause = ` AND user_id = ?`

const DeleteDaemonDelete2 = "DELETE FROM"

const DeleteDaemonSelect = "SELECT 1 FROM"

const DaemonConnectedDelete = `DELETE FROM daemon_capabilities WHERE daemon_id=?`

const DaemonConnectedInsert = `INSERT OR IGNORE INTO daemon_capabilities VALUES(?,?)`

const DaemonConnectedUpdate = `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ?`

const DaemonConnectedUpdate2 = `UPDATE daemon_harnesses SET available = 0, updated_at = ? WHERE daemon_id = ?`

const DaemonConnectedSelect = `SELECT COUNT(*) FROM harnesses WHERE name = ?`

const DaemonConnectedInsert2 = `INSERT INTO daemon_harnesses (daemon_id, user_id, harness, version, available, enabled, updated_at) VALUES (?, ?, ?, ?, 1, 0, ?)
			 ON CONFLICT(daemon_id, harness) DO UPDATE SET version = excluded.version, available = 1, updated_at = excluded.updated_at`

const DaemonDisconnectedUpdate = `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ? AND status = ?`

const TouchDaemonUpdate = `UPDATE daemons SET last_seen_at = ? WHERE id = ?`

const MarkAllDaemonsOfflineUpdate = `UPDATE daemons SET status = ? WHERE status = ?`

const SetDaemonHarnessEnabledUpdate = `UPDATE daemon_harnesses SET enabled = ?, updated_at = ? WHERE daemon_id = ? AND harness = ?`

const SetDaemonHarnessEnabledClause = ` AND user_id = ?`

const CreateCustomHarnessInsert = `INSERT INTO harnesses (name, kind, display_name, description, owner_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`

const DeleteCustomHarnessDelete = `DELETE FROM harnesses WHERE name = ? AND kind = 'custom'`

const DeleteCustomHarnessClause = ` AND owner_id = ?`

const ListHarnessesSelect = `SELECT name, kind, display_name, description, owner_id, created_at FROM harnesses
		 ORDER BY kind = 'custom', name`

const ReportRunEventClause = ` WHERE id=? AND daemon_id=?`

const ReportRunEventSelect = `SELECT kind,payload FROM run_events WHERE run_id=? AND attempt_id=? AND event_seq=?`

const ReportRunEventInsert = `INSERT INTO run_events(session_id,run_id,attempt_id,message_id,daemon_id,event_seq,kind,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?)`

const ReportRunEventUpdate = `UPDATE session_runs SET lease_until=? WHERE id=?`

const ReportRunEventUpdate2 = `UPDATE session_runs SET context_seq=MIN(?,(SELECT COALESCE(MAX(id),0) FROM messages WHERE session_id=?)) WHERE id=?`

const ReportRunEventUpdate3 = `UPDATE message_deliveries SET status='delivered' WHERE run_id=? AND prepared_attempt=? AND status='pending'`

const ReportRunEventUpdate4 = `UPDATE message_deliveries SET status='delivered' WHERE id=? AND run_id=? AND status='pending'`

const ReportRunEventInsert2 = `INSERT INTO run_questions(session_id,run_id,attempt_id,message_id,tool_use_id,questions,status,created_at,updated_at) VALUES(?,?,?,?,?,?,'pending',?,?) ON CONFLICT(run_id,attempt_id,tool_use_id) DO NOTHING`

const ReportRunEventUpdate5 = `UPDATE session_runs SET status='waiting' WHERE id=?`

const ReportRunEventUpdate6 = `UPDATE run_questions SET status=?,updated_at=? WHERE run_id=? AND attempt_id=? AND tool_use_id=? AND status='pending'`

const ReportRunEventUpdate7 = `UPDATE session_runs SET status='running' WHERE id=?`

const RecoverRunAnswersSelect = `SELECT answers FROM run_questions WHERE run_id=? AND attempt_id=? AND status='pending' AND answers IS NOT NULL AND `

const RecoverRunAnswersInsert = `INSERT INTO message_formats VALUES(?,1)`

const RecoverRunAnswersInsert2 = `INSERT INTO message_deliveries(message_id,session_id,agent_id,agent_key,run_id,kind,status) VALUES(?,?,?,?,?,'steer','pending')`

const RunEventsSelect = `SELECT id,session_id,run_id,attempt_id,message_id,daemon_id,kind,payload,created_at FROM run_events WHERE session_id=? ORDER BY id`

const QueryRunQuestionsSelect = `SELECT id,session_id,run_id,attempt_id,message_id,tool_use_id,questions,answers,COALESCE(answered_by,''),status,created_at FROM run_questions WHERE `

const QueryRunQuestionsClause = ` ORDER BY id`

const AnswerRunQuestionClause = ` WHERE id=? AND session_id=?`

const AnswerRunQuestionSelect = `SELECT questions,answers,status,tool_use_id,message_id,created_at FROM run_questions WHERE id=? AND run_id=? AND attempt_id=?`

const AnswerRunQuestionUpdate = `UPDATE run_questions SET answers=?,answered_by=?,updated_at=? WHERE id=?`

const PendingRunAnswersClause = `status='pending' AND answers IS NOT NULL AND EXISTS(SELECT 1 FROM session_runs r WHERE r.id=run_questions.run_id AND r.attempt_id=run_questions.attempt_id AND r.daemon_id=? AND r.status='waiting' AND r.lease_until>?)`

const CheckRunClause = ` WHERE id=?`

const RenewRunUpdate = `UPDATE session_runs SET lease_until=? WHERE id=?`

const RunInputsClause = `run_id=? AND status IN ('pending','delivered')`

const RunInputsSelect = `SELECT COALESCE((SELECT is_prompt FROM message_formats WHERE message_id=?),0)`

const RunInputsSelect2 = `SELECT COALESCE((SELECT is_prompt FROM message_formats WHERE message_id=?),0)`

const RunInputsUpdate = `UPDATE message_deliveries SET prepared_attempt=? WHERE id=?`

const RunInputsClause2 = ` WHERE session_id=? ORDER BY id`

const ClaimRunClause = ` WHERE status='pending' AND daemon_id=? ORDER BY created_at,message_id,id LIMIT 1`

const ClaimRunUpdate = `UPDATE session_runs SET status='running',attempt_id=?,lease_until=?,updated_at=? WHERE id=?`

const ClaimRunUpdate2 = `UPDATE session_runs SET context_seq=? WHERE id=?`

const ClaimRunSelect = `SELECT content FROM messages WHERE id=?`

const ClaimRunSelect2 = `SELECT id,name FROM users WHERE id=?`

const ClaimRunSelect3 = `SELECT instructions,coordinator_agent_id FROM works WHERE id=?`

const ClaimRunInsert = `INSERT INTO work_run_tokens VALUES(?,?,?,?)`

const FailUnavailableRunsClause = ` WHERE status='pending' ORDER BY created_at,id`

const FailUnavailableRunsSelect = `SELECT COUNT(*) FROM attachments WHERE session_id=? AND NOT EXISTS(SELECT 1 FROM daemon_capabilities WHERE daemon_id=? AND capability='attachments-v1')`

const FinishRunInsert = `INSERT INTO message_authors VALUES(?,?,?,?)`

const FinishRunUpdate = `UPDATE session_runs SET status=?,lease_until=NULL,result_message_id=?,error=?,updated_at=? WHERE id=?`

const FinishRunDelete = `DELETE FROM work_run_tokens WHERE run_id=?`

const FinishRunUpdate2 = `UPDATE run_questions SET status='expired',updated_at=? WHERE run_id=? AND status='pending'`

const FinishRunUpdate3 = `UPDATE message_deliveries SET status=?,error=? WHERE run_id=? AND (kind='new_run' OR status='delivered' OR (prepared_attempt=? AND ?='succeeded'))`

const FinishRunClause = `run_id=? AND status='pending'`

const FinishRunUpdate4 = `UPDATE message_deliveries SET status=?,error=? WHERE run_id=? AND status='pending'`

const FinishRunUpdate5 = `UPDATE message_deliveries SET status='failed',error=? WHERE run_id=? AND status='pending'`

const FinishRunUpdate6 = `UPDATE message_deliveries SET run_id=?,kind='steer',prepared_attempt='' WHERE run_id=? AND status='pending'`

const FinishRunUpdate7 = `UPDATE message_deliveries SET kind='new_run' WHERE id=?`

const RunSteersClause = `run_id=? AND kind='steer' AND status='pending' AND prepared_attempt != (SELECT attempt_id FROM session_runs WHERE id=message_deliveries.run_id) AND message_id IN (SELECT message_id FROM message_formats WHERE is_prompt=1)`

const RunTaskPromptSelect = `SELECT content FROM messages WHERE id=? AND EXISTS(SELECT 1 FROM message_formats f WHERE f.message_id=messages.id AND f.is_prompt=1)`

const CancelRunClause = ` WHERE id=? AND session_id=?`

const CancelRunInsert = `INSERT INTO run_events(session_id,run_id,attempt_id,message_id,daemon_id,event_seq,kind,payload,created_at) VALUES(?,?,?,?,?,(SELECT COALESCE(MAX(event_seq),0)+1 FROM run_events WHERE run_id=? AND attempt_id=?),'cancelled',?,?)`

const RequeueRunsUpdate = `UPDATE session_runs SET status='pending',lease_until=NULL,updated_at=? WHERE status IN ('running','waiting') AND lease_until<=? RETURNING id,session_id`

const RequeueRunsClause = ` WHERE id=?`

const RequeueRunsUpdate2 = `UPDATE message_deliveries SET status='pending',prepared_attempt='' WHERE run_id=? AND status='delivered'`

const RequeueRunsUpdate3 = `UPDATE run_questions SET status='expired',updated_at=? WHERE run_id=? AND status='pending'`

const ReconcileRunsClause = ` WHERE daemon_id=? AND status IN ('running','waiting')`

const ReconcileRunsUpdate = `UPDATE session_runs SET status='pending',lease_until=NULL WHERE id=?`

const ReconcileRunsUpdate2 = `UPDATE message_deliveries SET status='pending',prepared_attempt='' WHERE run_id=? AND status='delivered'`

const ReconcileRunsUpdate3 = `UPDATE run_questions SET status='expired' WHERE run_id=? AND status='pending'`

const ReconcileRunsDelete = `DELETE FROM work_run_tokens WHERE run_id=?`

const PendingRunSteersClause = `kind='steer' AND status='pending' AND EXISTS(SELECT 1 FROM session_runs r WHERE r.id=message_deliveries.run_id AND r.status='running' AND r.attempt_id!=message_deliveries.prepared_attempt) AND message_id IN (SELECT message_id FROM message_formats WHERE is_prompt=1)`

const TerminateDaemonRunsClause = ` WHERE daemon_id=? AND status IN ('pending','running','waiting')`

const CreateMultiWorkClause = ` WHERE id=? AND user_id=?`

const CreateMultiWorkInsert = `INSERT INTO works(id,user_id,name,instructions,coordinator_id,coordinator_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`

const CreateMultiWorkUpdate = `UPDATE works SET coordinator_id=? WHERE id=?`

const WorkRunByTokenClause = ` WHERE id=(SELECT run_id FROM work_run_tokens t WHERE t.token_hash=? AND t.attempt_id=session_runs.attempt_id) AND status IN ('running','waiting') AND lease_until>?`

const RunWorkTokenSelect = `SELECT t.work_id FROM work_run_tokens t JOIN session_runs r ON r.id=t.run_id WHERE t.token_hash=? AND t.attempt_id=r.attempt_id AND r.status IN ('running','waiting') AND r.lease_until>?`

const RunWorkTokenClause = ` WHERE w.id=?`

const NotifyRunMessagesInsert = `INSERT INTO run_notifications VALUES(?) ON CONFLICT DO NOTHING`

const NotifyRunMessagesSelect = `SELECT m.content FROM session_runs r LEFT JOIN messages m ON m.id=r.result_message_id WHERE r.id=?`

const RunCols = `SELECT id,session_id,message_id,COALESCE(agent_id,''),agent_key,agent_name,agent_user_id,COALESCE(daemon_id,''),harness,status,attempt_id,lease_until,context_seq,result_message_id,error,created_at,updated_at FROM session_runs`

const GetRunClause = ` WHERE session_id=? AND id=?`

const RunsClause = ` WHERE session_id=? ORDER BY message_id,created_at,id`

const DeliveryCols = `SELECT id,message_id,session_id,COALESCE(agent_id,''),agent_key,COALESCE(run_id,''),kind,status,error FROM message_deliveries`

const DeliveriesClause = ` WHERE `

const DeliveriesClause2 = ` ORDER BY id`

const MultiStatusUpdate = `UPDATE sessions SET status=CASE
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='waiting') THEN 'waiting'
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='running') THEN 'running'
 WHEN EXISTS(SELECT 1 FROM session_runs WHERE session_id=? AND status='pending') THEN 'pending'
 WHEN status='resolved' THEN 'resolved' ELSE 'idle' END,updated_at=? WHERE id=? AND mode='multi_agent'`

const InsertMultiInsert = `INSERT INTO sessions(id,user_id,created_by,work_id,title,status,created_at,updated_at,mode) VALUES(?,?,?,?,?,'idle',?,?,'multi_agent')`

const ReplyForClause = ` WHERE id=?`

const ReplyForClause2 = ` WHERE id=?`

const RunAgentClause = ` WHERE id=?`

const RunRuntimeSelect = `SELECT COUNT(*) FROM daemon_capabilities WHERE daemon_id=? AND capability=?`

const NewRunSelect = `SELECT COUNT(*) FROM attachments WHERE session_id=?`

const NewRunSelect2 = `SELECT COUNT(*) FROM daemon_capabilities WHERE daemon_id=? AND capability='attachments-v1'`

const NewRunInsert = `INSERT INTO session_runs(id,session_id,message_id,agent_id,agent_key,agent_name,agent_user_id,daemon_id,harness,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,'pending',?,?)`

const SendMultiTxSelect = `SELECT digest,message_id FROM session_requests WHERE session_id=? AND user_id=? AND request_key=?`

const SendMultiTxInsert = `INSERT INTO message_formats VALUES(?,?)`

const SendMultiTxClause = ` WHERE session_id=? AND agent_id=? AND status IN ('pending','running','waiting')`

const SendMultiTxInsert2 = `INSERT INTO message_deliveries(message_id,session_id,agent_id,agent_key,run_id,kind,status) VALUES(?,?,?,?,?,?,'pending')`

const SendMultiTxUpdate = `UPDATE sessions SET status='idle',title=CASE WHEN title='' THEN ? ELSE title END,updated_at=? WHERE id=?`

const SendMultiTxInsert3 = `INSERT INTO session_requests VALUES(?,?,?,?,?)`

const InsertMessageInsert = `INSERT INTO messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`

// work_role 是算出来的：工作的 coordinator_id 指着它就是协调者，否则在工作里就是线程。
const SessionCols = `SELECT id,user_id,created_by,work_id,
 CASE WHEN work_id IS NULL THEN '' WHEN EXISTS(SELECT 1 FROM works w WHERE w.coordinator_id=sessions.id) THEN 'coordinator' ELSE 'thread' END,
 title,status,created_at,updated_at,mode FROM sessions`

const MessageCols = `SELECT id, session_id, role, content, created_at,
 COALESCE((SELECT run_id FROM message_authors a WHERE a.message_id=messages.id),''),
 COALESCE((SELECT agent_key FROM message_authors a WHERE a.message_id=messages.id),''),
 COALESCE((SELECT agent_name FROM message_authors a WHERE a.message_id=messages.id),''),
 (SELECT json_group_array(agent_key) FROM message_deliveries d WHERE d.message_id=messages.id) FROM messages`

const GetSessionClause = ` WHERE id = ?`

const GetSessionClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListSessionsClause = ` WHERE work_id IS NULL`

const ListSessionsClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListWorkSessionsClause = ` WHERE work_id = ? AND id != ?`

const ListSessionsClause3 = ` ORDER BY updated_at DESC, id DESC LIMIT ?`

const DeleteSessionDelete = `DELETE FROM sessions WHERE id = ?`

const MessagesClause = ` WHERE session_id = ? ORDER BY id`

const CreateUserInsert = `INSERT INTO users (id, name, role, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`

const ResetUserTokenUpdate = `UPDATE users SET token_hash = ? WHERE id = ?`

const GetUserSelect = `SELECT id, name, role, created_at FROM users WHERE id = ?`

const FirstAdminSelect = `SELECT id, name, role, created_at FROM users WHERE role = ? ORDER BY created_at, id LIMIT 1`

const UserByTokenSelect = `SELECT id, name, role, created_at FROM users WHERE token_hash = ?`

const CountUsersSelect = `SELECT COUNT(*) FROM users`

const ListUsersSelect = `SELECT id, name, role, created_at FROM users ORDER BY created_at, name`

const WorkCols = `SELECT w.id, w.user_id, w.name, w.instructions, w.coordinator_id,
  (SELECT COUNT(*) FROM sessions s WHERE s.work_id = w.id AND s.id != w.coordinator_id),
  w.created_at,
  MAX(w.updated_at, COALESCE((SELECT MAX(s.updated_at) FROM sessions s WHERE s.work_id = w.id), 0)) AS last
   , COALESCE(w.coordinator_agent_id,'') FROM works w`

const GetWorkClause = ` WHERE w.id = ?`

const GetWorkClause2 = ` AND w.user_id = ?`

const ListWorksClause = ` WHERE w.user_id = ?`

const ListWorksClause2 = ` ORDER BY last DESC, w.id DESC`

const UpdateWorkClause = ` WHERE id=? AND user_id=?`

const UpdateWorkUpdate = `UPDATE works SET coordinator_agent_id=? WHERE id=?`

const UpdateWorkUpdate2 = `UPDATE sessions SET title = ? WHERE id = ?`

const UpdateWorkUpdate3 = `UPDATE works SET name = ?, instructions = ?, updated_at = ? WHERE id = ?`

const DeleteWorkSelect = `SELECT COUNT(*) FROM sessions WHERE work_id = ? AND (status IN ('running', 'waiting') OR (mode='multi_agent' AND status='pending'))`

const DeleteWorkDelete = `DELETE FROM sessions WHERE work_id = ?`

const DeleteWorkDelete2 = `DELETE FROM works WHERE id = ?`

const SetResolvedUpdate = `UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`
