# agentuse

> 用户电脑上跑一个 daemon,从 agentuse 服务领任务，在本地调用 CLI agent(Claude Code、Codex 或自己写的 harness)干活，再把进度和结果汇报回去。

设计见 [`docs/v2/designs/main.md`](docs/v2/designs/main.md),接入与通信见 [`connect.md`](docs/v2/designs/connect.md),表结构见 [`docs/v2/tables/`](docs/v2/tables/),接口见 [`docs/v2/apis/`](docs/v2/apis/README.md),命令行见 [`docs/v2/cli/`](docs/v2/cli/README.md)。旧版(v1)文档留在 [`docs/v1/`](docs/v1/README.md)。

## 快速开始

### 1. 起服务

```bash
make build dist                     # bin/agentuse + dist/ 下各平台二进制
bin/agentuse server -dist dist      # 默认监听 :8080,数据在 ~/.agentuse/server.db
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

在这台 daemon 下打开要用的 agent 开关(默认都关)。之后在「任务」页选 agent、写 prompt 提交，任务会派给你自己的、开启了这个 agent 的在线 daemon,进度和结果实时显示。

## 命令

```
agentuse server [-addr :8080] [-db 路径] [-public-url URL] [-dist 目录]
agentuse daemon register -server URL -token 安装令牌   # setup.sh 会替你跑
agentuse daemon run                                     # setup.sh 装成的后台服务跑的就是它
agentuse user create 用户名 [-id 用户id] [-role user|admin]
agentuse user token 用户名                              # 重置登录令牌
```

server 的参数也能用环境变量给:`AGENTUSE_ADDR`、`AGENTUSE_DB`、`AGENTUSE_PUBLIC_URL`、`AGENTUSE_DIST`。

## API

网页用的就是这些接口，脚本里带 `Authorization: Bearer <登录令牌>` 即可调用。完整说明见 [`docs/v2/apis/`](docs/v2/apis/README.md)。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/daemons` | 我的 daemon(admin 看全部) |
| POST | `/api/daemons` | 添加 daemon,返回安装命令 |
| PUT | `/api/daemons/{id}/agents/{agent}` | `{"enabled": true}` 开关 agent |
| POST | `/api/daemons/{id}/reset` | 重新生成安装命令 |
| DELETE | `/api/daemons/{id}` | 删除 |
| GET / POST | `/api/tasks` | 列任务 / 建任务 `{"agent":"claude","prompt":"…","cwd":"可选"}` |
| GET | `/api/tasks/{id}` | 任务详情和回报 |
| GET / POST | `/api/agents` | 列 agent / 注册自定义 harness |
| GET / POST | `/api/users` | (admin)列用户 / 建用户 |

## 开发

```bash
make test     # go test ./...
make build    # bin/agentuse
make dist     # dist/agentuse-{linux,darwin}-{amd64,arm64}
```

## 布局

```
cmd/agentuse/        单一二进制:server / daemon / user 命令
internal/
├── cli/             命令行
├── store/           SQLite 存储(表结构见 docs/v2/tables/)
├── protocol/        daemon ⇄ 服务的注册接口和 WebSocket 消息
├── server/          HTTP API、WebSocket 派任务、setup.sh、网页(web/)
└── daemon/          注册、连接、调用本地 CLI agent、汇报
docs/                v2(当前) / v1(旧版)
```

## License

[Apache License 2.0](LICENSE)
