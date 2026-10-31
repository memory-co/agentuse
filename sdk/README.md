# SDK

给**网站后端**用的 agentuse 客户端，把 agentuse 嵌进自己的站点。思路见 [designs/embedding](../docs/v2/designs/embedding.md),接口见 [apis](../docs/v2/apis/README.md)。

| 语言 | 目录 | 依赖 |
|---|---|---|
| Python ≥ 3.8 | [python](python/README.md) | 无(标准库) |
| Node.js ≥ 18 | [nodejs](nodejs/README.md) | 无(自带 fetch),带 TypeScript 类型 |
| Go ≥ 1.24 | [golang](golang/README.md) | 无(标准库) |

三个版本用法一样:

1. 用 agentuse-server 的地址和 **admin 令牌**(服务密钥)建一个客户端。客户端不带状态，全局建一个共用就行。令牌只放在后端，不能下发到浏览器。
2. 每个方法的第一个参数(Go 里是 `ctx` 之后)直接传**网站的 user id**,请求会带 `X-Agentuse-User` 按这个用户执行，用户第一次出现时 agentuse 自动建。传空(`None` / `null` / `""`)就是 admin 本身。用户管理(`list_users`、`create_user`、`reset_user_token`)只有 admin 能调，不带这个参数。
3. 网站上的功能对应的方法:

| 网站上的功能 | 方法(Python / Node / Go) |
|---|---|
| 「连接我的电脑」 | `create_daemon` / `createDaemon` / `CreateDaemon`,把返回的 `setup_command` 给用户复制 |
| 看电脑在不在线 | `list_daemons` / `listDaemons` / `ListDaemons` |
| 开关某台电脑上的 harness | `set_daemon_harness` / `setDaemonHarness` / `SetDaemonHarness` |
| 建一个 agent(起名字，绑定运行时：某台电脑上的某个 harness,以后可以切换) | `create_agent` / `createAgent` / `CreateAgent`;切换运行时用 `update_agent` / `updateAgent` / `UpdateAgent` |
| 用某个 agent 发起一次任务 | `create_session` / `createSession` / `CreateSession` |
| 接着回复 | `send_message` / `sendMessage` / `SendMessage`(harness 还没回时 409) |
| 看进度和结果 | `get_session` / `getSession` / `GetSession`,或 `wait_session` 轮询到 `idle` |

另外还有 `me`、`list_agents`、`get_agent`、`delete_agent`、`list_sessions`、`delete_session`、`list_harnesses`、`create_harness`、`delete_harness`、`reset_daemon`、`delete_daemon`,以及 admin 用的 `list_users`、`create_user`、`reset_user_token`,和接口一一对应。

非 2xx 响应统一抛(Go 里返回)一个带 `status` 和服务端 `error` 说明的错误。

## 会话附件

设计见 [attachment](../docs/v2/designs/attachment.md)，HTTP 约定见 [attachments](../docs/v2/apis/attachments.md)。三种 SDK 同步增加 `Attachment` 类型和以下方法，仍按第一个用户参数发送 `X-Agentuse-User`：

| 操作 | Python / Node / Go |
|---|---|
| 上传一个文件流、指定文件名 | `upload_attachment` / `uploadAttachment` / `UploadAttachment` |
| 分页列出 session 附件，接收 limit/cursor | `list_attachments` / `listAttachments` / `ListAttachments` |
| 取附件元数据 | `get_attachment` / `getAttachment` / `GetAttachment` |
| 下载附件，返回可关闭的响应流 | `download_attachment` / `downloadAttachment` / `DownloadAttachment` |
| 删除附件 | `delete_attachment` / `deleteAttachment` / `DeleteAttachment` |

以上方法均显式传 session_id；get/download/delete 再传 attachment_id。列表返回 `{attachments, next_cursor}`，upload/get 返回 Attachment，delete 不返回内容。Go upload 接收 `io.Reader` 和 filename、download 返回 `io.ReadCloser`（另带响应头）；Python 接收二进制文件对象、返回可关闭的二进制响应；Node 接收文件流、返回含 headers 的流响应，调用方负责消费或关闭。传输取消与错误处理沿用各 SDK 约定。upload 必须流式生成 multipart，不能先缓冲整个文件或转 base64；download 不自动落盘。

`create_session` / `createSession` / `CreateSession` 的可选参数增加 `defer_start`。为 true 时仅创建 idle 会话，返回中的 message 可缺省；调用者可先上传输入附件，再调用 send_message 开始执行。已有调用的默认行为保持不变。

## 多 agent 共享 session

创建时显式传 mode=multi_agent，不传 agent_id。发送消息带 target_agent_ids，返回 message、runs、deliveries；空闲目标新建执行，忙碌目标自动插话。目标省略只保存笔记，不默认调用上次 agent。

三个 SDK 增加 get/cancel/wait run、wait message runs、answer run question 方法。等待一条消息完成要跟踪 deliveries（包括改挂到后续 run 的插话），不能只等最初返回的新 runs。WaitSession 对新模式默认等全部执行结束；returnOnWaiting / return_on_waiting / Go WaitOptions 可选择在提问时返回。
