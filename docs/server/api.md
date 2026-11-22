# Server API

管理员自升级入口为 `/api/upgrades`，包括查询版本、异步开始、进度及历史，见[自升级 API](upgrades.md#异步-api)。

Server 提供用户、设备、Agent、Skill、conversation 和文件接口。网站后端可通过 [SDK](../../sdk/README.md) 调用；接口权限由请求身份和资源归属共同决定。

## 在线接口文档

Server 和 Client 分别用 Huma 生成各自的接口文档。启动并登录对应服务后访问：

| 路径 | 内容 |
|---|---|
| `/api/docs` | 可查看和调试接口的交互文档 |
| `/api/openapi.json`、`/api/openapi.yaml` | OpenAPI 定义 |
| `/api/schemas/{schema}.json` | JSON Schema |

请求、响应、状态码和上传下载格式以运行版本的 OpenAPI 为准。路由与文档共用注册定义，schema 从 Go 类型生成。Client 本机接口使用单独的地址与登录凭据，见 [Client API](../client/api.md)。

## 身份与鉴权

脚本使用 `Authorization: Bearer TOKEN`；网页登录后使用 cookie。

| 身份 | 适用范围 |
|---|---|
| 用户令牌 `ut_…` | 按用户权限管理资源、发送消息和读写文件；管理员可管理用户及代用户操作 |
| 输入／run 票据 `at1.…` | 仅由安全设备 WS 发放；准备期只读固定配置和源消息，accepted 后受 run、期限和会话关系限制 |

管理员可带 `X-Agentuse-User: USER_ID` 代指定用户操作。设备不能使用该头冒充用户；Agent 的协作操作必须携带已验证的执行来源，具体范围见 [conversation 权限](conversations.md#模式与嵌套)。

```bash
curl -H "Authorization: Bearer $AGENTUSE_TOKEN" \
  "$AGENTUSE_SERVER/api/me"
```

令牌只在可信客户端或网站后端保存。网页登录入口为 `POST /api/login`，其 `token` 字段也接受安装时设置的管理员密码；退出为 `POST /api/logout`。管理 CLI 的凭据配置见 [Server CLI](cli.md#管理命令的地址与凭据)。

## 设备与执行器

| 方法 | 路径 | 用途 |
|---|---|---|
| GET / POST | `/api/devices` | 列出设备 / 创建设备并返回私钥正文和注册命令 |
| GET | `/api/devices/latest-release` | 查询最新正式 Client 版本 |
| POST | `/api/devices/{id}/upgrade` | 请求在线设备通过 WebSocket 开始升级，返回 202 和升级状态 |
| PUT | `/api/devices/{id}/harnesses/{harness}` | 用 `{"enabled":true}` 启用或关闭该设备的执行器 |
| POST | `/api/devices/{id}/reset` | 撤销设备旧凭据并返回新私钥 |
| DELETE | `/api/devices/{id}` | 删除设备并断开连接 |
| GET | `/api/harnesses` | 读取设备上报的执行器视图 |

创建设备或重置设备的响应包含 `setup_token`（OpenSSH Ed25519 私钥正文）、`setup_server_pin`（Server CA 指纹）及 `setup_command`（复制到目标终端执行的 Shell 命令）。命令用 `curl` 下载 `setup.sh`，通过 Bash 的 `TOKEN` 环境变量将私钥正文交给脚本。脚本会复用或安装 Client，通过标准输入将私钥交给 `agentuse register`，然后启动后台服务。`setup_command` 含设备私钥，应按私钥处理。这两个操作需要用户认证及相应权限，HTTP 和 HTTPS 均可使用。响应使用 `Cache-Control: no-store`。列表不包含私钥或注册命令。持有当前私钥即可再次导入同一设备，重置会撤销旧私钥和证书。

Client 通过安全 WS 提交 CSR 和当前连接的签名，领取设备证书。业务 HTTP 使用短期输入或 run 票据。旧 `POST /api/client/register` 和 `GET /api/client/me` 返回 426。设备配置顺序见 [快速开始](../client/cli.md#接入-server)。

设备列表的 `client_version` 来自设备最近一次连接上报。支持远程升级的在线设备具有 `client-upgrade-v1` 能力。升级请求针对最新正式版，设备重新上线并上报目标版本后，列表中的 `client_upgrade.status` 更新为 `succeeded`。

## Agent 与固定版本

| 方法 | 路径 | 用途 |
|---|---|---|
| GET / POST | `/api/agents` | 列出 Agent / 创建身份及 v1 |
| GET / DELETE | `/api/agents/{id}` | 读取当前版本 / 软删除身份 |
| GET / POST | `/api/agents/{id}/versions` | 查看历史版本 / 发布固定配置 |
| GET | `/api/agents/{id}/versions/{version_id}` | 读取指定版本 |

创建 Agent 前，先注册设备并在 Server 启用其执行器。示例请求体：

```json
{"id":"code-helper","name":"写代码助手","device_id":"dev_替换为设备ID","harness":"claude"}
```

发布时提交完整配置及 `base_version_id`，可修改名称、设备、执行器、模型、指令及固定 Skill 版本；冲突返回 409，相同配置返回已有版本。版本与初始化关系见 [Agent 设计](../../designs/v3/server-agent.md)。

## Skill

| 方法 | 路径 | 用途 |
|---|---|---|
| GET / POST | `/api/skills` | 列出技能 / 上传 ZIP 创建身份与 v1 |
| GET / DELETE | `/api/skills/{id}` | 读取当前版本 / 软删除身份 |
| GET / POST | `/api/skills/{id}/versions` | 查看历史版本 / 上传下一版本 |
| GET | `/api/skills/{id}/versions/{version_id}` | 读取固定版本描述 |
| GET | `/api/skills/{id}/versions/{version_id}/package` | 下载固定 ZIP |

上传使用 multipart 的 `file` 字段，发布下一版本另带 `base_version_id`。包根需有 `SKILL.md`，每包一个技能。详细约束见 [Skill 设计](../../designs/v3/server-skill.md)。

## Conversation、执行与文件

[Conversation 接口](conversations.md) 集中说明创建与列表、消息收件人、共享历史、轮次读取、取消、原生请求回复，以及嵌套协作。多 Agent 的请求示例见 [共享消息示例](conversations.md#多-agent-共享消息示例)。

附件上传、逐条消息选择文件、独立文件树和容量限制见 [对话文件](conversation-files.md)。AI 读写文件及按路径下载见 [Client context](../client/context.md)。

## 用户管理

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/me` | 读取当前请求身份 |
| GET / POST | `/api/users` | 管理员列出 / 创建用户 |
| POST | `/api/users/{id}/token` | 管理员重置用户令牌 |

创建或重置时返回用户令牌和登录链接，重置后旧令牌立即失效。CLI 示例见 [用户管理](cli.md#user-create)。

## WebSocket

`/api/client/ws` 用于 Client 连接和执行协议，Huma 文档描述握手，连接后的报文及恢复规则见 [通信协议](../../designs/v3/protocol.md)。网页通过 `/api/browser/ws` 接收变更提示，再以 HTTP 读取内容，见 [浏览器通知设计](../../designs/v3/server-browser.md)。

设备授权在 run 终态、固定期限到达或设备重置后失效；已受理输入不能退回准备期。历史文件由用户身份读取。WS 内层加密不保护独立 HTTP 下载；需要文件和访问令牌的链路保密时，部署 HTTPS。
