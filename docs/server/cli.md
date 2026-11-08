# Server CLI：agentuse-server

`agentuse-server start` 启动中心服务，提供管理页面、HTTP API 和 Client WebSocket 入口。`login`、`user` 命令通过 HTTP 管理运行中的服务，可以在能访问 Server 的其他机器上执行。

## 快速开始

安装好 `agentuse-server` 后，在服务主机上运行：

```bash
agentuse-server start
```

默认监听 `:8080`，数据库位于 `~/.agentuse/server.db`。第一次启动时，日志会显示管理员登录令牌和登录链接。用链接进入管理页面；默认本机访问地址是 `http://localhost:8080`。

`start` 在前台持续运行。另开终端，保存管理命令使用的地址和管理员令牌：

```bash
agentuse-server login --server http://localhost:8080 --token ut_替换为管理员令牌
agentuse-server user create alice --id alice
agentuse-server user list
```

创建用户会输出用户 ID、登录令牌和登录链接。执行设备的注册与启动见 [Client CLI](../client/cli.md)。

生产部署可显式指定持久化目录和用户访问的地址：

```bash
agentuse-server start \
  --addr :8080 \
  --public-url https://agentuse.example.com \
  --db /srv/agentuse/server.db \
  --files-dir /srv/agentuse/files \
  --attachments-dir /srv/agentuse/attachments \
  --dist /opt/agentuse/dist
```

## 命令一览

| 命令 | 作用 |
|---|---|
| `agentuse-server start` | 启动服务 |
| `agentuse-server login` | 验证管理员身份并保存管理凭据 |
| `agentuse-server user create USERNAME` | 创建用户 |
| `agentuse-server user list` | 列出用户 |
| `agentuse-server user token USER_ID` | 重置用户登录令牌 |
| `agentuse-server help` | 查看命令一览 |
| `agentuse-server version` | 显示版本 |

参数支持 `--flag value`、`-flag value` 和 `--flag=value`。布尔参数直接写 `--json`、`--reset-admin-token` 即可。`user create` 和 `user token` 的主对象放在参数之前，其他命令按下文示例使用。

## start

```bash
agentuse-server start [参数]
```

命令行参数优先于对应环境变量，环境变量优先于默认值。

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--addr` | `AGENTUSE_ADDR` | `:8080` | HTTP 监听地址，兼用于 WebSocket |
| `--db` | `AGENTUSE_DB` | `~/.agentuse/server.db` | SQLite 数据库文件；`:memory:` 使用临时数据库 |
| `--public-url` | `AGENTUSE_PUBLIC_URL` | 空 | 用户和 Client 访问 Server 的地址，用于生成登录链接和安装命令；省略时按监听地址或请求推导 |
| `--dist` | `AGENTUSE_DIST` | `~/.agentuse/dist` | 各平台 Client 二进制所在目录 |
| `--attachments-dir` | `AGENTUSE_ATTACHMENTS_DIR` | `~/.agentuse/attachments` | 会话附件的本地存储目录 |
| `--attachment-max-bytes` | `AGENTUSE_ATTACHMENT_MAX_BYTES` | `104857600`，100 MiB | 单个附件的最大字节数 |
| `--attachment-session-max-bytes` | `AGENTUSE_ATTACHMENT_SESSION_MAX_BYTES` | `1073741824`，1 GiB | 每个会话全部附件的最大总字节数 |
| `--files-backend` | `AGENTUSE_FILES_BACKEND` | `local` | 会话文件和 skill 包的存储后端：`local` 或 `s3` |
| `--files-dir` | `AGENTUSE_FILES_DIR` | `~/.agentuse/files` | `local` 后端的根目录 |
| `--s3-endpoint` | `AGENTUSE_S3_ENDPOINT` | 空 | S3 兼容服务地址；省略时使用 AWS S3 默认地址 |
| `--s3-bucket` | `AGENTUSE_S3_BUCKET` | 空 | S3 bucket，使用 `s3` 后端时必填 |
| `--s3-region` | `AGENTUSE_S3_REGION` | `us-east-1` | S3 region |
| `--s3-prefix` | `AGENTUSE_S3_PREFIX` | 空 | S3 对象前缀 |
| `--reset-admin-token` | — | `false` | 启动时重置最早创建的管理员的令牌，并在日志中显示新令牌和链接；旧令牌立即失效 |

两个附件容量参数必须是正整数，单位为字节。

### Client 二进制分发

Server 的 `/setup.sh` 从 `--dist` 目录提供对应平台的 Client。目录中的文件名应为：

```text
agentuse-linux-amd64
agentuse-linux-arm64
agentuse-darwin-amd64
agentuse-darwin-arm64
```

仓库根目录的 `install.sh` 默认把这些文件安装到 `~/.agentuse/dist`。从源码部署时，运行 `make dist`，再用 `agentuse-server start --dist dist` 指定构建产物。缺少平台文件会在启动日志中显示，该平台的自动安装下载会失败。

### S3 存储

S3 后端读取 `AWS_ACCESS_KEY_ID` 和 `AWS_SECRET_ACCESS_KEY`。这两个环境变量以及 bucket 都是必需的：

```bash
export AWS_ACCESS_KEY_ID="替换为访问密钥 ID"
export AWS_SECRET_ACCESS_KEY="替换为访问密钥"

agentuse-server start \
  --files-backend s3 \
  --s3-endpoint https://s3.example.com \
  --s3-bucket agentuse \
  --s3-region us-east-1 \
  --s3-prefix production
```

`--files-backend s3` 影响会话文件和 skill 包。会话附件仍写入 `--attachments-dir`。保存部署数据时，应同时保存数据库、附件目录，以及所选文件后端中的数据。

### 启停与数据库

`start` 在前台运行，通过 `Ctrl+C` 或 `SIGTERM` 停止。长期部署可由 systemd、容器或其他进程管理器托管。

当前 Server 使用 schema 5。若启动报 `unsupported database schema: reset the database before starting this version`，需使用新的数据库文件启动，再重新创建资源和注册设备；程序不迁移旧数据库。

## 管理命令的地址与凭据

`user create`、`user list`、`user token` 逐项按下列优先级选择服务地址和管理员令牌：

1. `--server`、`--token` 参数。
2. `AGENTUSE_SERVER`、`AGENTUSE_TOKEN` 环境变量。
3. `login` 保存的配置文件。

| 公共参数 | 默认值 | 说明 |
|---|---|---|
| `--server URL` | 按上述优先级选择 | Server 地址 |
| `--token TOKEN` | 按上述优先级选择 | 管理员用户令牌，`ut_` 开头 |
| `--config FILE` | `~/.agentuse/server-cli.json` | 管理凭据文件 |

这些参数用于管理命令，`start` 的配置由启动参数和环境变量提供。

可以仅使用环境变量：

```bash
export AGENTUSE_SERVER="https://agentuse.example.com"
export AGENTUSE_TOKEN="ut_替换为管理员令牌"
agentuse-server user list --json
```

## login

```bash
agentuse-server login \
  --server https://agentuse.example.com \
  --token ut_替换为管理员令牌
```

| 参数 | 默认来源 | 说明 |
|---|---|---|
| `--server` | `AGENTUSE_SERVER` | Server 地址，必填 |
| `--token` | `AGENTUSE_TOKEN` | 管理员令牌，必填 |
| `--config` | `~/.agentuse/server-cli.json` | 写入的管理凭据文件 |

`login` 调用 `GET /api/me` 验证令牌和管理员角色，再以 `0600` 权限保存地址与令牌。它的地址和令牌来自参数或环境变量。

使用独立配置文件：

```bash
agentuse-server login --server https://agentuse.example.com \
  --token ut_替换为管理员令牌 --config ./admin.json
agentuse-server user list --config ./admin.json
```

## user create

```bash
agentuse-server user create USERNAME [参数]

agentuse-server user create alice --id alice --role user
agentuse-server user create operator --role admin
```

`USERNAME` 放在 `--id`、`--role` 和管理公共参数之前。

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--id` | 自动生成 UUID | 用户 ID，可使用自己业务系统的 ID；最长 128 字符，允许字母、数字及 `. _ - : @` |
| `--role` | `user` | `user` 或 `admin` |

用户名必须非空且唯一，显式指定的 ID 也必须唯一。成功后输出用户信息、令牌和登录链接。

## user list

```bash
agentuse-server user list
agentuse-server user list --json
```

默认输出包含 ID、用户名、角色和创建时间的表格。`--json` 输出用户对象数组，便于脚本处理。

## user token

```bash
agentuse-server user token USER_ID [管理公共参数]

agentuse-server user token alice
```

重置指定用户的登录令牌，输出新令牌和登录链接，旧令牌立即失效。`USER_ID` 放在管理公共参数之前。

若管理员令牌丢失，可停止服务后运行：

```bash
agentuse-server start --reset-admin-token
```

如果原来使用自定义数据库等启动参数，重启时继续传入相同参数，确保操作的是原数据库。

## 帮助、输出与退出码

```bash
agentuse-server help
agentuse-server start --help
agentuse-server version
```

`help` 也可写为 `-h` 或 `--help`；`version` 也可写为 `-v` 或 `--version`。这些是程序后的第一个参数，例如 `agentuse-server --version`。

| 退出码 | 含义 |
|---|---|
| `0` | 命令成功或显示帮助 |
| `1` | 连接、鉴权、存储或其他执行错误 |
| `2` | 命令或参数用法错误 |

管理命令的结果写入 stdout；错误写入 stderr。启动日志写入 stderr。脚本读取用户列表时使用 `user list --json`。

## API 文档

启动 Server 并登录后，访问：

| 路径 | 内容 |
|---|---|
| `/api/docs` | Huma 交互文档，可直接调试接口 |
| `/api/openapi.json` | OpenAPI JSON |
| `/api/openapi.yaml` | OpenAPI YAML |
| `/api/schemas/{schema}.json` | JSON Schema |

文档入口接受用户 cookie 或用户 Bearer token。各业务接口可用的工作令牌、设备密钥及权限范围在生成文档中分别列出。
