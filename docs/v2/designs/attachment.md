# 会话附件

> 已实现。附件需要服务端和 daemon 一起升级；daemon 通过 `attachments-v1` 声明支持。

## 用途

在 session 这一级增加 attachment。用户把需求文档、图片、数据上传到会话，harness 读取这些文件；harness 生成报告、代码包、图片后，用本机 `agentuse` 上传，用户在会话里下载。附件属于会话，不挂在某条消息上，也不放进消息的 JSON 或 WebSocket 里传输。

同一会话中的每个附件有独立 id，文件内容和元数据创建后不修改。同名文件可以共存，重新上传得到新的 id。第一版不做跨会话共享、在线编辑、目录上传或断点续传；目录由调用者先打包成一个文件。

## 一次完整交互

1. 用户建会话、上传输入文件，再发消息让 harness 处理。
2. daemon 领到消息后，取得附件清单、下载缺少的文件，放进会话工作目录的 `.agentuse/attachments/<attachment_id>/<filename>`。
3. daemon 把附件 id、原始文件名和本地相对路径交给 harness。文件都是普通文件，文本、图片等能否理解由 harness 自己决定，服务不解析内容。
4. harness 生成文件后执行 `agentuse attachment upload ./report.pdf`。CLI 流式上传，打印附件 id 和下载路径。
5. 页面轮询附件列表，显示上传者、文件名、大小和下载按钮。最终回复可以引用下载路径，文件即使不写进最终回复也会出现在附件列表里。

上传不自动发消息，不改变 session 的 `status`、`updated_at`，也不自动把 `resolved` 重新打开。文件落盘成功后才会出现在列表中。

## 先建空会话

现有 `POST /api/sessions` 建会话时会立即派出第一条消息，没有先上传文件的机会。增加可选 `defer_start: true`：

```json
{ "agent_id": "code-helper", "defer_start": true }
```

返回 201 和 `{"session": {"status": "idle", "…": "…"}}`，不建 message、不派活。此时不能同时传 `prompt` 或 `input`，否则 400；agent 和运行时的校验保持不变。工作令牌用它建线程时也遵守相同规则；CLI 用 `agentuse work new --agent <id> --defer-start`，上传后 `agentuse work send` 开始。随后上传文件，再用现有 messages 接口发第一条消息；第一条有 prompt 的消息补上空标题。未传 `defer_start` 的行为保持不变。工作协调者原本就从 `idle` 开始，可以直接上传。

## HTTP 和权限

接口统一放在 `/api/sessions/{id}/attachments` 下，完整请求、响应见 [apis/attachments](../apis/attachments.md)。

| 方法和路径后缀 | 行为 |
|---|---|
| `POST /attachments` | multipart 上传一个文件，201 返回 attachment |
| `GET /attachments` | 分页列出元数据 |
| `GET /attachments/{attachment_id}` | 取元数据 |
| `GET /attachments/{attachment_id}/content` | 下载原始文件 |
| `DELETE /attachments/{attachment_id}` | 删除元数据并安排清理文件，204 |

会话主人、发起人和 admin 可以读、上传、删除。支持网页登录 cookie、`ut_` 和 admin 的 `X-Agentuse-User`，与 session 一致；代为用户时按该用户权限检查。附件 id 必须属于 URL 中的 session，看不到会话或附件一律 404。

客户端上传产物使用 daemon 已有的 `dk_`，无需把用户登录令牌传给 harness。允许读取分配给该 daemon 的会话附件；上传必须额外带 multipart 字段 `message_id`，且该消息正在这台 daemon 上执行、租约有效、会话为 `running` / `waiting`。会话属于别的 daemon 返回 404；本 daemon 的消息已结束或失去租约返回 409。提交元数据前再次检查租约，上传过程中失去租约不提交文件。daemon 身份不能删除附件，不能访问会话其他管理接口。删除 daemon 后其密钥立即失效。

`wt_` 增加附件读、上传、删除权限，范围仅限本工作的协调者和线程；超出范围返回 403，过期返回 401。工作令牌不能扩大到 agent 主人的其他会话。工作删除时所有会话附件一起删除。

第一版对上传和下载使用普通 HTTP。WebSocket 继续只传派活、心跳和事件，文件传输不阻塞心跳。

## 存储和一致性

元数据存 [attachments](../tables/attachments.md) 表，二进制放服务本地磁盘。新增 `agentuse-server start -attachments-dir` / `AGENTUSE_ATTACHMENTS_DIR`，默认 `~/.agentuse/attachments`，不能放在公开静态资源目录下。数据库与这个目录必须一起备份、恢复和迁移；第一版不接对象存储。

磁盘路径用 `<root>/<session_id>/<attachment_id>/content`，由服务生成并验证 id，不拼接用户文件名。上传使用流式读写和 SHA-256 校验，默认单文件 100 MiB、每会话总计 1 GiB（含输入与产物）；新增 `-attachment-max-bytes` / `AGENTUSE_ATTACHMENT_MAX_BYTES`、`-attachment-session-max-bytes` / `AGENTUSE_ATTACHMENT_SESSION_MAX_BYTES`，必须是正整数。请求体上限额外预留 1 MiB 给 multipart 边界和字段。

上传顺序：在同一文件系统写临时文件并计数、算摘要 → fsync 并关闭 → 开数据库写事务，重新检查会话、身份、租约和会话配额 → 原子 rename 到最终路径 → 插入元数据并提交。失败时回滚、清理临时或最终文件。并发上传在事务内串行核算配额，不能各自检查后一起超额。超过大小或配额返回 413。

崩溃可能留下没有元数据的文件，但不会提前暴露未写完的附件。启动和定期清理超过 24 小时的临时文件、未被元数据引用的最终文件；清理任务与本进程上传共用互斥锁，避免删到尚未提交的文件。第一版一个数据目录只由一个服务进程写入。下载时记录存在但文件缺失，返回 500 并记录告警，不伪装成不存在。

删除先在事务中删除元数据，再清理文件；清理失败由上述任务补偿。删除会话时外键级联删元数据，文件异步清理。附件删除后不再能下载；已经下载到 daemon 的副本不做远程擦除，备份也遵守各自保留策略。

文件名取上传文件名的 basename（同时识别 `/` 和 `\`），拒绝空名、`.`、`..`、控制字符及超过 255 UTF-8 字节的名字。保存原始文件名仅用于展示和下载响应头。服务检测前 512 字节得到 `content_type`，无法判断用 `application/octet-stream`，不信任客户端声明。所有内容下载使用 `Content-Disposition: attachment`（编码后的 `filename*`）、`X-Content-Type-Options: nosniff`、`Cache-Control: private, no-store`；鉴权后才打开文件，不提供免登录公开链接。页面不直接执行或内嵌 HTML、SVG 等上传内容。

## daemon 和 harness

daemon 每次执行一条消息前，通过 HTTP 用自己的 `dk_` 读取该 session 的完整附件列表，按列表中的 id 下载并验证大小、SHA-256。附件缓存文件先写临时路径，校验成功后原子 rename；失败就回报本轮 `failed`，不启动 harness。重试复用校验通过的文件，不仅凭文件名判断命中。不跟随目标目录中的符号链接，防止附件路径写出会话工作目录。

附件清单以这次执行前拉取的结果为准。执行中新增文件不自动塞进正在运行的 harness，用户应下一轮引用它；本轮急需的补充可插话告知，并由 CLI 显式下载。附件在拉清单后被删除、下载返回 404 时，本轮失败并说明附件已删除，下次执行重新取清单。已经缓存的文件即使被服务端删除也可能仍在本地，但之后不再写进附件清单。

daemon 在自己生成的 harness 上下文中加入附件 id、文件名、本地相对路径和 CLI 用法，不修改用户的原始 `input` 或工作说明。Claude / Codex 沿用现有说明文件机制，和工作说明合并生成；自定义 harness 在 stdin 的运行上下文增加 `attachments` 数组。附件名是数据，不能作为说明指令拼接。目录固定保留给 daemon，harness 不应写入。

给每个 harness 进程设置 `AGENTUSE_SERVER`、`AGENTUSE_SESSION`、`AGENTUSE_MESSAGE_ID`，PATH 前面加上 agentuse 可执行文件目录；已有协调者的 `AGENTUSE_WORK` 行为保持不变。CLI 的 daemon 配置路径也由 daemon 显式传入 `AGENTUSE_CONFIG`，避免 daemon 使用自定义 `-config` 时 CLI 读错密钥。环境中不增加用户令牌或 daemon 密钥明文，CLI 从配置读取密钥。

## agentuse 客户端

```bash
# harness 内，默认当前 session；路径相对 harness 当前工作目录
agentuse attachment upload ./report.pdf
agentuse attachment upload ./report-v2.pdf --name report.pdf --json
agentuse attachment list --json
agentuse attachment download at_xxx --output ./input.csv

# 人在终端使用用户令牌；--session 显式指定会话
agentuse attachment upload ./requirements.pdf --session ss_xxx --server https://agentuse.example.com --token ut_…
```

命令的完整约定见 [cli/agentuse](../cli/agentuse.md#attachment)。单次上传一个普通文件；拒绝目录，不自动压缩。上传和下载都流式处理，不把整个文件读进内存。成功后纯文本输出 id、文件名、大小及服务端返回的相对下载路径；`--json` 直接输出 API 对象，错误写 stderr，非零退出。客户端不自动无限重试 POST，超时可能已经上传成功，提示先 list 检查；第一版不保证上传幂等。

## 页面与 SDK

页面在 session 详情提供附件列表、上传、下载、删除，上传过程显示进度，失败显示服务端错误。新会话选择文件时先 `defer_start` 建空会话，全部上传成功才发送第一条消息；失败保留空会话和已经上传的附件供重试，不偷偷派活。已有会话上传不自动发送消息；运行中上传提示“下一轮可用”。第一版不要求在消息 Markdown 中解析特殊附件语法。

Python、Node、Go SDK 都增加 `Attachment` 类型和 list/upload/get/download/delete 方法，仍支持按网站用户身份调用。upload 接受文件流、文件名，生成 multipart，不走只支持 JSON 的现有 helper；download 返回可关闭的响应流，调用方决定存到哪里。保留现有错误结构，新增 413 / 415 / 409 等状态的透传。CreateSession 的可选参数增加 `defer_start`，对应返回的 `message` 可缺省。

## 升级兼容

新 daemon 的 WebSocket `hello` 增加可选 `capabilities: ["attachments-v1"]`，服务记录最近一次上报的能力并在 daemon 对象中返回 `capabilities`。旧 daemon 缺省为空数组，不根据版本字符串猜能力。

用户或工作令牌向尚未声明附件能力的 daemon 所属会话上传时返回 409，提示先升级并至少连接一次。daemon 离线但已声明支持时可以上传，排队等上线；重连不带该能力时清空记录。含附件的会话不得派给不支持附件的 daemon：沿用现有 harness 不可用的失败路径，回到 idle，追加明确的 assistant 错误；daemon 升级后用户可以重新发消息。没有附件的旧会话照常执行。

## 实现与验收

1. 表和文件存储层：目录配置、流式写入、限额、摘要、删除补偿，旧数据库新增表即可，不改现有 session 数据。
2. HTTP：上传、列表、元数据、下载、删除；扩展用户、daemon、工作令牌的权限；增加空会话创建。
3. CLI 与 daemon：multipart 上传、下载、执行前同步、上下文和环境变量；CLI 能使用现有自定义配置路径。
4. 前端和三个 SDK；更新内置 harness 的交付说明，要求文件产物上传后再在最终回复引用下载路径。
5. 用 fakeharness 做端到端验证：空会话 → 用户上传 → daemon 校验并读取 → harness 调 CLI 上传产物 → 用户下载，逐字节验证内容。

测试覆盖跨用户、跨会话和跨 daemon 越权，工作令牌范围与过期，上传期间租约失效或会话被删除，同名文件、空文件、恶意文件名、符号链接、超限和并发配额，磁盘写入/提交失败、崩溃残留清理、删除补偿，以及下载中断后的 CLI 错误。升级兼容测试覆盖旧 hello 缺少能力、支持附件的 daemon 离线以及重连后能力变化。
