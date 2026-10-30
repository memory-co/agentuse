# attachments

> 会话附件设计见 [designs/attachment](../designs/attachment.md)，表结构见 [tables/attachments](../tables/attachments.md)。

附件属于 session，创建后内容不可变，不关联某条 message。全部接口先校验会话权限，再校验附件属于这个会话。用户、admin、daemon 密钥、工作令牌的范围见设计文档；下面的下载路径也需要认证。

## attachment 对象

```json
{
  "id": "at_51e76a8b1a1e45c0",
  "session_id": "ss_b83a324345de1f46",
  "filename": "report.pdf",
  "content_type": "application/pdf",
  "size_bytes": 204800,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "created_by": "u_10086",
  "source": "daemon",
  "daemon_id": "dm_5c1c8602536e55cb",
  "created_at": 1792800105,
  "download_path": "/api/sessions/ss_b83a324345de1f46/attachments/at_51e76a8b1a1e45c0/content"
}
```

`sha256` 是文件原始字节的 64 位小写十六进制摘要，示例值仅表示格式。`source` 为 `user` / `daemon` / `work`；`created_by` 是执行请求的用户身份，daemon 上传时为 daemon 主人，工作令牌上传时为工作主人。`daemon_id` 仅 daemon 来源且 daemon 仍存在时出现。`download_path` 是相对服务根路径，客户端拼接自己的服务地址，响应不包含磁盘路径或认证令牌。

## POST /api/sessions/{id}/attachments

`Content-Type: multipart/form-data; boundary=…`，恰好一个 `file` 字段，文件名取该 part 的 filename。可选文本字段 `filename` 覆盖展示文件名。用 `dk_` 时必须再带 `message_id`，其他身份不允许传这个字段（400）。重复字段、多个文件、未知字段、缺文件返回 400；空文件允许。

```bash
curl -H 'Authorization: Bearer ut_…' \
  -F 'file=@./report.pdf' \
  https://agentuse.example.com/api/sessions/ss_xxx/attachments
```

成功返回 201 和 attachment 对象，`Location` 指向元数据接口。上传不创建消息、不触发派活，session 的任何状态都允许用户或工作令牌上传；daemon 上传受活动消息和租约约束。大小和会话配额见设计，失败不产生可见附件。同一文件重传会创建新 id。

## GET /api/sessions/{id}/attachments

返回 200：

```json
{ "attachments": [ { "id": "at_…", "session_id": "ss_…", "…": "…" } ], "next_cursor": null }
```

按 `(created_at, id)` 升序。`limit` 默认 100，范围 1–100；`cursor` 为上一页返回的不透明游标，非法或跨会话游标返回 400。没有下一页时 `next_cursor` 为 null；空列表为 `[]`。分页不是快照，期间新增、删除可能改变列表，daemon 按本次取到的清单执行，下一轮重取。

## GET /api/sessions/{id}/attachments/{attachment_id}

返回 200 和 attachment 对象。

## GET /api/sessions/{id}/attachments/{attachment_id}/content

返回 200 和二进制原文，带 `Content-Length`、检测出的 `Content-Type`、`Content-Disposition: attachment`、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`。第一版不要求 Range / 断点续传，客户端校验完整大小和摘要。浏览器使用登录 cookie 下载；脚本带 Bearer token，不把令牌放在 URL。

## DELETE /api/sessions/{id}/attachments/{attachment_id}

用户或本工作令牌可以删除，成功返回 204；不存在或已经删除返回 404。daemon 密钥调用返回 403。允许在会话执行中删除；已经下载到 daemon 的副本不会被远程删除。删除 session 或 work 会级联删除附件元数据并异步回收文件。

## 错误

沿用 `{"error": "说明"}`；鉴权和参数验证失败时也不返回附件内容。

| 状态码 | 场景 |
|---|---|
| 400 | 文件名或 multipart 字段错误、游标错误、daemon 上传缺少 message_id |
| 401 | 未认证、令牌/密钥失效、工作令牌过期 |
| 403 | 身份不允许该操作，或工作令牌越出本工作 |
| 404 | 会话不可见、附件不存在或不属于 URL 中的会话、daemon 不匹配 |
| 409 | 会话的 daemon 未声明附件能力；daemon 的消息不在执行、租约失效；上传提交前会话被删除 |
| 413 | 文件、请求体或会话总配额超限 |
| 415 | 上传不是 multipart/form-data |
| 500 | 文件写入失败、数据库提交失败、元数据存在但文件缺失 |

## 多 agent session

multi_agent 空会话可以先上传文件，不要求先绑定 daemon。选执行目标时校验 daemon 附件能力。每次执行读取同一 session 当前附件列表。

元数据增加可选 created_by_run_id、created_by_agent_key，用于标识产物来自哪个执行和 agent。普通用户上传不填它们；daemon 和新工作令牌的作者由服务推导。

daemon 请求带 Bearer dk_ 及 X-Agentuse-Run、X-Agentuse-Attempt；新模式不再仅凭 message_id 授权。执行结束、取消或租约失效后，读取和上传都拒绝；其他会话/其他 daemon 的执行返回 404。legacy 继续用现有 message_id 规则。
