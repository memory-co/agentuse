# agentuse

> 用户电脑上跑一个 daemon,从 agentuse 服务领活，在本地调用 CLI agent(Claude Code、Codex 或自己写的 harness)干活，再把进度和结果汇报回去。

设计见 [`docs/v2/designs/main.md`](docs/v2/designs/main.md),接入与通信见 [`connect.md`](docs/v2/designs/connect.md),表结构见 [`docs/v2/tables/`](docs/v2/tables/),接口见 [`docs/v2/apis/`](docs/v2/apis/README.md),命令行见 [`docs/v2/cli/`](docs/v2/cli/README.md)。嵌进自己网站见 [`embedding.md`](docs/v2/designs/embedding.md) 和 [`sdk/`](sdk/README.md)(Python / Node.js / Go)。旧版(v1)文档留在 [`docs/v1/`](docs/v1/README.md)。

## 快速开始

### 1. 起服务

```bash
make build dist                          # bin/agentuse、bin/agentuse-server + dist/ 下各平台 agentuse
bin/agentuse-server start -dist dist     # 默认监听 :8080,数据在 ~/.agentuse/server.db
```

第一次启动会建管理员 `admin`,并在日志里打印登录链接:

```
第一次启动，已创建管理员 admin。登录令牌(只显示这一次): ut_…
登录链接: http://localhost:8080/login?token=ut_…
```

放到公网 / 反向代理后面时，用 `-public-url https://agentuse.example.com` 指定用户电脑访问服务的地址(会写进安装命令)。不指定就按请求的 Host 推断。

### 2. 接入一台电脑

打开网页 →「机器」→「添加 daemon」,页面会给出一条命令，在要跑 agent 的电脑上执行:

```bash
curl -fsSL "https://agentuse.example.com/setup.sh" -o setup.sh && TOKEN="st_…" bash setup.sh
```

它会下载 agentuse、注册这台电脑、装成后台服务(macOS launchd / Linux systemd --user,都没有就 nohup)。几秒后页面上这台电脑变成「在线」,并列出本机检测到的 agent。

### 3. 选择开启哪些 agent

在这台 daemon 下打开要用的 agent 开关(默认都关)。之后在「会话」页选 agent、写 prompt 开一个会话，会派给你自己的、开启了这个 agent 的在线 daemon,进度和回复实时显示，可以接着回复(会话对齐 Claude Code / Codex 自己的 session,在同一台电脑上续接)。

## 命令

```
# 服务端
agentuse-server start [-addr :8080] [-db 路径] [-public-url URL] [-dist 目录] [-reset-admin-token]
agentuse-server login -server URL -token 管理员令牌     # 之后的管理命令都走 HTTP API
agentuse-server user create 用户名 [-id 用户id] [-role user|admin]
agentuse-server user list [-json]
agentuse-server user token 用户id                       # 重置登录令牌

# 用户电脑
agentuse register -server URL -token 安装令牌           # setup.sh 会替你跑
agentuse daemon                                         # setup.sh 装成的后台服务跑的就是它
agentuse status                                         # 注册和连接状态
agentuse agents                                         # 本机检测到的 agent
```

两个 CLI 的完整说明见 [`docs/v2/cli/`](docs/v2/cli/README.md)。`start` 的参数也能用环境变量给:`AGENTUSE_ADDR`、`AGENTUSE_DB`、`AGENTUSE_PUBLIC_URL`、`AGENTUSE_DIST`。

## API

网页用的就是这些接口，脚本里带 `Authorization: Bearer <登录令牌>` 即可调用。完整说明见 [`docs/v2/apis/`](docs/v2/apis/README.md)。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/daemons` | 我的 daemon(admin 看全部) |
| POST | `/api/daemons` | 添加 daemon,返回安装命令 |
| PUT | `/api/daemons/{id}/agents/{agent}` | `{"enabled": true}` 开关 agent |
| POST | `/api/daemons/{id}/reset` | 重新生成安装命令 |
| DELETE | `/api/daemons/{id}` | 删除 |
| GET / POST | `/api/sessions` | 列会话 / 开会话 `{"agent":"claude","prompt":"…","cwd":"可选"}` |
| POST | `/api/sessions/{id}/messages` | 回复 `{"prompt":"…"}`,agent 还没回时 409 |
| GET / DELETE | `/api/sessions/{id}` | 会话详情(消息和回报) / 删除 |
| GET / POST | `/api/agents` | 列 agent / 注册自定义 harness |
| GET / POST | `/api/users` | (admin)列用户 / 建用户 |

## 开发

```bash
make test     # go test ./...
make build    # bin/agentuse、bin/agentuse-server(先构建网页 frontend/dist 再嵌进去，需要 Node 18+)
make dist     # dist/agentuse-{linux,darwin}-{amd64,arm64}(只有用户电脑上的 agentuse)
```

## 布局

```
cmd/agentuse/        用户电脑上的 CLI:register / daemon / status / agents
cmd/agentuse-server/ 服务端 CLI:start / login / user
internal/
├── cli/             命令行
├── store/           SQLite 存储(表结构见 docs/v2/tables/)
├── protocol/        daemon ⇄ 服务的注册接口和 WebSocket 消息
├── server/          HTTP API、WebSocket 派活、setup.sh、托管网页
└── daemon/          注册、连接、调用本地 CLI agent、汇报
frontend/            网页:React + Vite + shadcn/ui,构建产物 dist/ 嵌进 agentuse-server
sdk/                 嵌入用客户端:python / nodejs / golang
docs/                v2(当前) / v1(旧版)
```

## License

[Apache License 2.0](LICENSE)
