# agentuse-server

部署和运维 agentuse 服务用的 CLI,跑在服务器上，直接读写服务的 SQLite 数据库。

数据库路径所有命令都用 `-db` 指定，默认 `~/.agentuse/server.db`,也可以用环境变量 `AGENTUSE_DB`。

## 命令

| 命令 | 说明 |
|---|---|
| `agentuse-server start` | 起服务 |
| `agentuse-server user create <用户名>` | 建用户 |
| `agentuse-server user list` | 列用户 |
| `agentuse-server user token <用户名>` | 重置登录令牌 |
| `agentuse-server version` | 版本 |

### start

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

- 库里没有用户时，自动建管理员 `admin`,在日志里打印登录链接(令牌只显示这一次)。
- 启动时把上次留下的 daemon `online` 状态清成 `offline`。
- `-dist` 里缺平台的二进制时打警告，对应平台的用户跑 `setup.sh` 会下载失败。
- 前台运行，SIGTERM 优雅退出;用 systemd / docker 托管。

### user create

```bash
agentuse-server user create alice -id u_10086 -role user
```

| 参数 | 说明 |
|---|---|
| `-id` | 用户 id,填接入方自己系统里的 user id;不填生成 UUID。规则见 [tables/users](../tables/users.md) |
| `-role` | `user`(默认)/ `admin` |

输出用户和登录令牌(只显示这一次)。和 [`POST /api/users`](../apis/users.md#post-apiusers) 等价，适合还没有管理员能登录网页、或批量导入时用。

### user list

```bash
agentuse-server user list [-json]
```

列出所有用户:id、用户名、角色、创建时间。

### user token

```bash
agentuse-server user token alice
```

给用户换一个新的登录令牌，旧的立即失效。管理员令牌丢了用这个找回。

## 和现在的差别

现在是一个二进制 `agentuse`,服务端命令是 `agentuse server` / `agentuse user …`。拆分后:

| 现在 | 拆分后 |
|---|---|
| `agentuse server …` | `agentuse-server start …` |
| `agentuse user create/token …` | `agentuse-server user create/token …` |
| — | `agentuse-server user list`(新增) |
| `agentuse daemon register …` | `agentuse register …` |
| `agentuse daemon run` | `agentuse daemon` |
| — | `agentuse status`、`agentuse agents`(新增) |

`setup.sh` 和后台服务的启动命令跟着改。
