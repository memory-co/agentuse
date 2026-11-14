// SQL used by the server metadata services. Native execution facts are client-owned.
package store

const CheckRuntimeSelect = `SELECT 1 FROM device_harnesses WHERE device_id = ? AND harness = ? AND user_id = ?`

const RuntimeErrSelect = `SELECT available, enabled FROM device_harnesses WHERE device_id = ? AND harness = ?`

const AgentCols = `SELECT id,user_id,name,device_id,harness,description,created_at,updated_at,current_version_id,version,COALESCE(model,''),instructions FROM (SELECT a.id,a.user_id,v.name,v.device_id AS device_id,v.harness,v.description,a.created_at,a.updated_at,a.current_version_id,v.version,v.model,v.instructions FROM agents a JOIN agent_versions v ON v.id=a.current_version_id WHERE a.deleted_at IS NULL)`
const GetAgentClause = ` WHERE id = ?`
const GetAgentClause2 = ` AND user_id = ?`
const ListAgentsClause = ` WHERE user_id = ?`
const ListAgentsClause2 = ` ORDER BY name, id`

const AttachmentConversationSelect = `SELECT user_id FROM devices WHERE id=? AND key_hash=?`

const ActiveWorkTokenFrom = ` FROM work_run_tokens t JOIN message_agents p ON p.message_id=t.message_id AND p.agent_id=t.agent_id JOIN works w ON w.id=t.work_id AND w.coordinator_id=p.conversation_id AND w.coordinator_agent_id=p.agent_id`

const AttachmentConversationSelect2 = `SELECT COUNT(*)` + ActiveWorkTokenFrom + ` WHERE t.work_id=? AND t.token_hash=? AND p.status IN ('sent','accepted')`

const AttachmentConversationSelect3 = `SELECT id,role FROM users WHERE token_hash=?`

const CommitAttachmentSelect = `SELECT COALESCE(SUM(size_bytes),0) FROM attachments WHERE conversation_id=?`

const CommitAttachmentInsert = `INSERT INTO attachments(id,conversation_id,filename,content_type,size_bytes,sha256,created_by,source,device_id,created_at,created_by_run_id,relative_path) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`

const AttachmentCols = `SELECT id,conversation_id,filename,content_type,size_bytes,sha256,created_by,source,COALESCE(device_id,''),created_at,COALESCE(created_by_run_id,''),COALESCE((SELECT agent_id FROM runs r WHERE r.id=attachments.created_by_run_id),''),relative_path FROM attachments`

const GetAttachmentClause = ` WHERE conversation_id=? AND id=?`

const ListAttachmentsClause = ` WHERE conversation_id=? AND (?='' OR created_by_run_id=?) AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`

const DeleteAttachmentDelete = `DELETE FROM attachments WHERE conversation_id=? AND id=?`

const AttachmentExistsSelect = `SELECT COUNT(*) FROM attachments WHERE conversation_id=? AND id=?`

const DeviceCapabilitiesSelect = `SELECT capability FROM device_capabilities WHERE device_id=? ORDER BY capability`

const CreateDeviceInsert = `INSERT INTO devices (id, user_id, name, setup_token, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`

const RegisterDeviceSelect = `SELECT id, user_id, name FROM devices WHERE setup_token = ?`

const RegisterDeviceUpdate = `UPDATE devices SET name = ?, key_hash = ?, setup_token = NULL, status = ? WHERE id = ?`

const ResetDeviceSetupSelect = `SELECT 1 FROM devices WHERE id=?`

const ResetDeviceSetupClause = ` AND user_id=?`

const ResetDeviceSetupUpdate = `UPDATE devices SET setup_token=?,key_hash=NULL,status=? WHERE id=?`

const DeviceByKeyClause = ` WHERE key_hash = ?`

const DeviceCols = `SELECT id, user_id, name, setup_token, status, last_seen_at, created_at FROM devices`

const GetDeviceClause = ` WHERE id = ?`

const GetDeviceClause2 = ` AND user_id = ?`

const GetDeviceClause3 = `WHERE device_id = ?`

const ListDevicesClause = ` WHERE user_id = ?`

const ListDevicesClause2 = ` ORDER BY created_at`

const ListDevicesClause3 = `WHERE user_id = ?`

const DeviceHarnessesSelect = `SELECT device_id, user_id, harness, version, harness_revision, capabilities, available, enabled, updated_at FROM device_harnesses `

const DeviceHarnessesClause = ` ORDER BY harness`

const DeleteDeviceDelete = `DELETE FROM devices WHERE id = ?`

const DeleteDeviceClause = ` AND user_id = ?`

const DeleteDeviceDelete2 = "DELETE FROM"

const DeleteDeviceSelect = "SELECT 1 FROM"

const DeviceConnectedDelete = `DELETE FROM device_capabilities WHERE device_id=?`

const DeviceConnectedInsert = `INSERT OR IGNORE INTO device_capabilities VALUES(?,?)`

const DeviceConnectedUpdate = `UPDATE devices SET status = ?, last_seen_at = ? WHERE id = ?`

const DeviceConnectedUpdate2 = `UPDATE device_harnesses SET available = 0, updated_at = ? WHERE device_id = ?`

const DeviceConnectedInsert2 = `INSERT INTO device_harnesses (device_id,user_id,harness,version,harness_revision,capabilities,available,enabled,updated_at) VALUES(?,?,?,?,?,?,1,0,?) ON CONFLICT(device_id,harness) DO UPDATE SET version=excluded.version,harness_revision=excluded.harness_revision,capabilities=excluded.capabilities,available=1,updated_at=excluded.updated_at`

const DeviceDisconnectedUpdate = `UPDATE devices SET status = ?, last_seen_at = ? WHERE id = ? AND status = ?`

const TouchDeviceUpdate = `UPDATE devices SET last_seen_at = ? WHERE id = ?`

const MarkAllDevicesOfflineUpdate = `UPDATE devices SET status = ? WHERE status = ?`

const SetDeviceHarnessEnabledUpdate = `UPDATE device_harnesses SET enabled = ?, updated_at = ? WHERE device_id = ? AND harness = ?`

const SetDeviceHarnessEnabledClause = ` AND user_id = ?`

const CreateWorkInsert = `INSERT INTO works(id,user_id,name,instructions,coordinator_id,coordinator_agent_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`

const CreateWorkUpdate = `UPDATE works SET coordinator_id=? WHERE id=?`

const InsertConversationInsert = `INSERT INTO conversations(id,user_id,created_by,work_id,title,status,created_at,updated_at) VALUES(?,?,?,?,?,'idle',?,?)`

const InsertMessageInsert = `INSERT INTO messages (id,seq,conversation_id,author_role,content,created_at) VALUES (?,?,?,?,?,?)`

// work_role 是算出来的：工作的 coordinator_id 指着它就是协调者，否则在工作里就是线程。
const ConversationCols = `SELECT id,user_id,created_by,work_id,
 CASE WHEN work_id IS NULL THEN '' WHEN EXISTS(SELECT 1 FROM works w WHERE w.coordinator_id=conversations.id) THEN 'coordinator' ELSE 'thread' END,
 title,status,created_at,updated_at FROM conversations`

const MessageCols = `SELECT id,conversation_id,author_role,content,created_at,COALESCE(author_agent_id,''),CASE WHEN author_role='agent' THEN COALESCE((SELECT name FROM agent_versions WHERE id=messages.author_agent_version_id),'') ELSE COALESCE((SELECT name FROM users WHERE id=messages.author_user_id),'') END,to_agent_ids,to_user_ids,COALESCE(author_user_id,''),COALESCE(reply_to_message_id,''),COALESCE(author_agent_version_id,''),seq,attachments FROM messages`

const GetConversationClause = ` WHERE id = ?`

const GetConversationClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListConversationsClause = ` WHERE work_id IS NULL`

const ListConversationsClause2 = ` AND (user_id = ? OR created_by = ?)`

const ListWorkConversationsClause = ` WHERE work_id = ? AND id != ?`

const ListConversationsClause3 = ` ORDER BY updated_at DESC, id DESC LIMIT ?`

const DeleteConversationDelete = `DELETE FROM conversations WHERE id = ?`

const MessagesClause = ` WHERE conversation_id = ? ORDER BY seq`

const CreateUserInsert = `INSERT INTO users (id, name, role, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`

const ResetUserTokenUpdate = `UPDATE users SET token_hash = ? WHERE id = ?`

const GetUserSelect = `SELECT id, name, role, created_at FROM users WHERE id = ?`

const FirstAdminSelect = `SELECT id, name, role, created_at FROM users WHERE role = ? ORDER BY created_at, id LIMIT 1`

const UserByTokenSelect = `SELECT id, name, role, created_at FROM users WHERE token_hash = ?`

const CountUsersSelect = `SELECT COUNT(*) FROM users`

const ListUsersSelect = `SELECT id, name, role, created_at FROM users ORDER BY created_at, name`

const WorkCols = `SELECT w.id, w.user_id, w.name, w.instructions, w.coordinator_id,
  (SELECT COUNT(*) FROM conversations s WHERE s.work_id = w.id AND s.id != w.coordinator_id),
  w.created_at,
  MAX(w.updated_at, COALESCE((SELECT MAX(s.updated_at) FROM conversations s WHERE s.work_id = w.id), 0)) AS last
   , COALESCE(w.coordinator_agent_id,'') FROM works w`

const GetWorkClause = ` WHERE w.id = ?`

const GetWorkClause2 = ` AND w.user_id = ?`

const ListWorksClause = ` WHERE w.user_id = ?`

const ListWorksClause2 = ` ORDER BY last DESC, w.id DESC`

const UpdateWorkClause = ` WHERE id=? AND user_id=?`

const UpdateWorkUpdate = `UPDATE works SET coordinator_agent_id=? WHERE id=?`

const RenameConversationUpdate = `UPDATE conversations SET title = ? WHERE id = ?`

const UpdateWorkUpdate3 = `UPDATE works SET name = ?, instructions = ?, updated_at = ? WHERE id = ?`

const WorkConversationsBusySelect = `SELECT COUNT(*) FROM conversations WHERE work_id = ? AND (status IN ('running', 'waiting') OR status='pending')`

const DeleteWorkConversationsDelete = `DELETE FROM conversations WHERE work_id = ?`

const DeleteWorkDelete2 = `DELETE FROM works WHERE id = ?`

const SetResolvedUpdate = `UPDATE conversations SET status = ?, updated_at = ? WHERE id = ?`
