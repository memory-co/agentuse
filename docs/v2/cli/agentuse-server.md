# agentuse-server

服务端用的 CLI,做两件事:

- **`start`**:起服务进程。只有它直接打开 SQLite 数据库。
- **管理命令**(`user …` 等):都是 [HTTP API](../apis/README.md) 的客户端，带管理员令牌调接口，不碰数据库。所以可以在任何能访问服务的机器上跑，不用登到服务器上。

```
agentuse-server user create ──HTTP──▶ agentuse-server start ──▶ SQLite
       (管理命令，API 客户端)              (服务进程)
```

## 命令

| 命令 | 说明 | 对应接口 |
|---|---|---|
| `agentuse-server start` | 起服务 | — |
| `agentuse-server login` | 保存服务地址和管理员令牌 | `GET /api/me` |
| `agentuse-server user create <用户名>` | 建用户 | `POST /api/users` |
| `agentuse-server user list` | 列用户 | `GET /api/users` |
| `agentuse-server user token <用户 id>` | 重置登录令牌 | `POST /api/users/{id}/token` |
| `agentuse-server version` | 版本 | — |

## start

```bash
agentuse-server start -addr :8080 -db /var/lib/agentuse/server.db \
  -public-url https://agentuse.example.com -dist /opt/agentuse/dist
```

| 参数 | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `-addr` | `AGENTUSE_ADDR` | `:8080` | 监听地址 |
| `-db` | `AGENTUSE_DB` | `~/.agentuse/server.db` | 数据库文件 |
| `-public-url` | `AGENTUSE_PUBLIC_URL` | 空(按请求推断) | 用户电脑访问服务的地址，写进安装命令 |
| `-dist` | `AGENTUSE_DIST` | `dist` | 各平台 `agentuse` 二进制目录 |
| `-reset-admin-token` | | 关 | 启动时给 `admin` 换一个新令牌并打印，管理员令牌丢了用 |

- 库里没有用户时，自动建管理员 `admin`,在日志里打印登录链接(令牌只显示这一次)。
- 启动时把上次留下的 daemon `online` 状态清成 `offline`。
- `-dist` 里缺平台的二进制时打警告，对应平台的用户跑 `setup.sh` 会下载失败。
- 前台运行，SIGTERM 优雅退出;用 systemd / docker 托管。

## 管理命令怎么连服务

每个管理命令都需要服务地址和一个 **admin** 的登录令牌，按下面的顺序找，先找到先用:

1. 参数 `-server URL`、`-token ut_…`
2. 环境变量 `AGENTUSE_SERVER`、`AGENTUSE_TOKEN`
3. `agentuse-server login` 存下的 `~/.agentuse/server-cli.json`

令牌不是 admin 时，接口返回 403,命令提示「需要管理员令牌」。

### login

```bash
agentuse-server login -server https://agentuse.example.com -token ut_…
```

调一次 `GET /api/me` 验证令牌有效、且是 admin,然后把地址和令牌写进 `~/.agentuse/server-cli.json`(权限 0600)。

## user create

```bash
agentuse-server user create alice -id u_10086 -role user
```

| 参数 | 说明 |
|---|---|
| `-id` | 用户 id,填接入方自己系统里的 user id;不填生成 UUID。规则见 [tables/users](../tables/users.md) |
| `-role` | `user`(默认)/ `admin` |

调 [`POST /api/users`](../apis/users.md#post-apiusers),输出用户和登录链接(令牌只显示这一次)。

## user list

```bash
agentuse-server user list [-json]
```

调 `GET /api/users`,列出 id、用户名、角色、创建时间。

## user token

```bash
agentuse-server user token u_10086
```

给用户换一个新的登录令牌，旧的立即失效。调 [`POST /api/users/{id}/token`](../apis/users.md#post-apiusersidtoken)。

管理员自己的令牌丢了，没法再调接口，用 `start -reset-admin-token` 在服务器上找回。

## 和旧命令的对照

拆分前是一个二进制 `agentuse`,服务端命令 `agentuse server` / `agentuse user …` 直接读写数据库:

| 旧 | 新 |
|---|---|
| `agentuse server …` | `agentuse-server start …` |
| `agentuse user create …`(直连数据库) | `agentuse-server user create …`(调 API) |
| `agentuse user token <用户名>`(直连数据库) | `agentuse-server user token <用户 id>`(调 API)+ `start -reset-admin-token` |
| — | `agentuse-server login`、`agentuse-server user list`(新增) |
| `agentuse daemon register …` | `agentuse register …` |
| `agentuse daemon run` | `agentuse daemon` |
| — | `agentuse status`、`agentuse harnesses`(新增) |
