// SQL used by the server metadata services. Native execution facts are client-owned.
package store

const CheckRuntimeSelect = `SELECT 1 FROM daemon_harnesses WHERE daemon_id = ? AND harness = ? AND user_id = ?`

const RuntimeErrSelect = `SELECT available, enabled FROM daemon_harnesses WHERE daemon_id = ? AND harness = ?`

const AgentCols = `SELECT id,user_id,name,daemon_id,harness,description,created_at,updated_at,current_version_id,version,COALESCE(model,''),instructions FROM (SELECT a.id,a.user_id,v.name,v.device_id AS daemon_id,v.harness,v.description,a.created_at,a.updated_at,a.current_version_id,v.version,v.model,v.instructions FROM agents a JOIN agent_versions v ON v.id=a.current_version_id WHERE a.deleted_at IS NULL)`
const GetAgentClause = ` WHERE id = ?`
const GetAgentClause2 = ` AND user_id = ?`
const ListAgentsClause = ` WHERE user_id = ?`
const ListAgentsClause2 = ` ORDER BY name, id`

const AttachmentSessionSelect = `SELECT user_id FROM daemons WHERE id=? AND key_hash=?`

const AttachmentSessionSelect2 = `SELECT COUNT(*) FROM work_run_tokens t JOIN message_agents p ON p.message_id=t.message_id AND p.agent_id=t.agent_id WHERE t.work_id=? AND t.token_hash=? AND p.status IN ('sent','accepted') AND ?>=0`

const AttachmentSessionSelect3 = `SELECT id,role FROM users WHERE token_hash=?`

const CommitAttachmentSelect = `SELECT COALESCE(SUM(size_bytes),0) FROM attachments WHERE session_id=?`

const CommitAttachmentInsert = `INSERT INTO attachments(id,session_id,filename,content_type,size_bytes,sha256,created_by,source,daemon_id,created_at,created_by_run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`

const AttachmentCols = `SELECT id,session_id,filename,content_type,size_bytes,sha256,created_by,source,COALESCE(daemon_id,''),created_at,COALESCE(created_by_run_id,''),COALESCE((SELECT agent_id FROM runs r WHERE r.id=attachments.created_by_run_id),'') FROM attachments`

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

const DaemonHarnessesSelect = `SELECT daemon_id, user_id, harness, version, revision, available, enabled, updated_at FROM daemon_harnesses `

const DaemonHarnessesClause = ` ORDER BY harness`

const DeleteDaemonDelete = `DELETE FROM daemons WHERE id = ?`

const DeleteDaemonClause = ` AND user_id = ?`

const DeleteDaemonDelete2 = "DELETE FROM"

const DeleteDaemonSelect = "SELECT 1 FROM"

const DaemonConnectedDelete = `DELETE FROM daemon_capabilities WHERE daemon_id=?`

const DaemonConnectedInsert = `INSERT OR IGNORE INTO daemon_capabilities VALUES(?,?)`

const DaemonConnectedUpdate = `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ?`

const DaemonConnectedUpdate2 = `UPDATE daemon_harnesses SET available = 0, updated_at = ? WHERE daemon_id = ?`

const DaemonConnectedInsert2 = `INSERT INTO daemon_harnesses (daemon_id,user_id,harness,version,revision,available,enabled,updated_at) VALUES(?,?,?,?,?,1,0,?) ON CONFLICT(daemon_id,harness) DO UPDATE SET version=excluded.version,revision=excluded.revision,available=1,updated_at=excluded.updated_at`

const DaemonDisconnectedUpdate = `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ? AND status = ?`

const TouchDaemonUpdate = `UPDATE daemons SET last_seen_at = ? WHERE id = ?`

const MarkAllDaemonsOfflineUpdate = `UPDATE daemons SET status = ? WHERE status = ?`

const SetDaemonHarnessEnabledUpdate = `UPDATE daemon_harnesses SET enabled = ?, updated_at = ? WHERE daemon_id = ? AND harness = ?`

const SetDaemonHarnessEnabledClause = ` AND user_id = ?`

const CreateMultiWorkInsert = `INSERT INTO works(id,user_id,name,instructions,coordinator_id,coordinator_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`

const CreateMultiWorkUpdate = `UPDATE works SET coordinator_id=? WHERE id=?`

const InsertMultiInsert = `INSERT INTO sessions(id,user_id,created_by,work_id,title,status,created_at,updated_at) VALUES(?,?,?,?,?,'idle',?,?)`

const InsertMessageInsert = `INSERT INTO messages (id,seq,session_id,role,content,created_at) VALUES (?,?,?,?,?,?)`

// work_role 是算出来的：工作的 coordinator_id 指着它就是协调者，否则在工作里就是线程。
const SessionCols = `SELECT id,user_id,created_by,work_id,
 CASE WHEN work_id IS NULL THEN '' WHEN EXISTS(SELECT 1 FROM works w WHERE w.coordinator_id=sessions.id) THEN 'coordinator' ELSE 'thread' END,
 title,status,created_at,updated_at,'multi_agent' FROM sessions`

const MessageCols = `SELECT id,session_id,role,content,created_at,'',COALESCE(author_agent_id,''),COALESCE((SELECT name FROM agent_versions WHERE id=messages.author_agent_version_id),''),run_agent_ids,COALESCE(author_agent_version_id,''),seq FROM messages`

const GetSessionClause = ` WHERE id = ?`

const GetSessionClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListSessionsClause = ` WHERE work_id IS NULL`

const ListSessionsClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListWorkSessionsClause = ` WHERE work_id = ? AND id != ?`

const ListSessionsClause3 = ` ORDER BY updated_at DESC, id DESC LIMIT ?`

const DeleteSessionDelete = `DELETE FROM sessions WHERE id = ?`

const MessagesClause = ` WHERE session_id = ? ORDER BY seq`

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

const DeleteWorkSelect = `SELECT COUNT(*) FROM sessions WHERE work_id = ? AND (status IN ('running', 'waiting') OR status='pending')`

const DeleteWorkDelete = `DELETE FROM sessions WHERE work_id = ?`

const DeleteWorkDelete2 = `DELETE FROM works WHERE id = ?`

const SetResolvedUpdate = `UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`
