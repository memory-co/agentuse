# attachmentService：会话附件

> v3 目标设计，尚未据此修改代码或数据库。统一使用 device 命名；本文 SQL 表示目标结构，不是线上迁移脚本。时间字段除另行说明外，均为 Unix 秒，布尔值用 SQLite INTEGER（0/1），JSON 用 TEXT 保存。

## 职责

attachmentService 管 session 下的附件元数据和正文，包括上传、下载、访问范围、大小/总容量限制以及清理。附件属于 session，由其中的 agent 共享，run 不复制一套附件。

## 操作与协作

| 操作 | 行为 |
|---|---|
| 上传 | controller 接收临时文件、计算大小和 SHA256；service 在提交前重新校验凭据、session、run/attempt 和配额 |
| 发布 | 在锁保护下将临时文件发布到最终目录，并提交元数据；失败时清理新文件 |
| 列表/下载 | 校验访问范围；正文与元数据对应，client 下载后校验大小和摘要 |
| 删除 | 检查权限，删除元数据并清理文件；失败残留由清理任务恢复 |
| 会话删除/孤儿清理 | 识别已无元数据的文件，超过保留门槛后清理；不把数据库级联误当成文件删除 |

userService 提供用户身份；sessionService 提供会话权限；runService 校验设备访问对应 run/attempt；workService 限定工作令牌的范围。上传前和提交时必须使用同一套授权规则，不能仅信任接收请求时的旧凭据。

正文路径为 `<attachment_dir>/<session_id>/<attachment_id>/content`。文件名仅用于显示和下载，不能拼成任意磁盘路径。文件系统与 SQLite 不是同一事务资源，通过发布/失败清理和孤儿清理共同保持一致。

## 表结构

### attachments

文件元数据与来源。source 在 v3 为 user/device/work；created_by 为归属用户，device_id 为设备来源，created_by_run_id 为生成该文件的 run 标识。沿用来源快照字段不设 run 外键，访问授权仍必须用 runService 校验；不能仅凭字段声称归属。正文不存数据库。

```sql
CREATE TABLE attachments (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
    sha256 TEXT NOT NULL CHECK(length(sha256)=64),
    created_by TEXT NOT NULL REFERENCES users(id),
    source TEXT NOT NULL CHECK(source IN ('user','device','work')),
    device_id TEXT REFERENCES devices(id) ON DELETE SET NULL,
    created_by_run_id TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX attachments_session
    ON attachments(session_id,created_at,id);
```

## 相关设计

[Server 总览](server.md) · [userService](user.md) · [deviceService](device.md) · [agentService](agent.md) · [harnessService](harness.md) · [sessionService](session.md) · [runService](run.md) · [workService](work.md)
