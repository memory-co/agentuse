# 接入与通信

怎么把一台电脑接成 daemon,以及 daemon 和服务之间说什么。

## 接入一台电脑

```
页面「添加 daemon」 ──▶ daemons 新增一行(status=pending,带一次性 setup_token)
        │
        ▼ 页面展示安装命令
curl -fsSL "https://<服务>/setup.sh" -o setup.sh && TOKEN="st_xxx" bash setup.sh
        │
        ▼ setup.sh 在用户电脑上
1. 从 <服务>/download/agentuse-<os>-<arch> 下载二进制到 ~/.agentuse/bin/agentuse
2. agentuse daemon register:用 TOKEN 换长期密钥(dk_xxx),写进 ~/.agentuse/daemon.json
3. 装成后台服务并启动:macOS 用 launchd,Linux 有 systemd --user 就用它，否则 nohup
        │
        ▼
daemon 连上 WebSocket,上报本机 agent ──▶ 页面上出现这台电脑和它的 agent 开关
```

- 安装令牌只能用一次，注册后作废;要换电脑或重装，在页面上「重新生成安装命令」,旧密钥同时失效。
- 二进制从服务的 `-dist` 目录取(`make dist` 交叉编译 linux/darwin × amd64/arm64);没有时，和服务同平台的请求直接拿服务自己的可执行文件。
- 用户在页面上为每台 daemon 选择开启哪些 agent,默认都关闭。

## 认证

| 谁 | 凭据 | 怎么带 |
|---|---|---|
| 用户(网页 / 脚本) | 登录令牌 `ut_` | 网页 cookie;脚本 `Authorization: Bearer ut_…` |
| daemon 注册 | 安装令牌 `st_` | `POST /api/daemon/register` 的 body |
| daemon 长连接 | 密钥 `dk_` | WebSocket 握手 `Authorization: Bearer dk_…` |

库里只存哈希(安装令牌除外，见 [daemons](../tables/daemons.md))。

## 消息

注册接口和 WebSocket 消息的完整格式见 [apis/daemon](../apis/daemon.md)。

## 派任务

- 一台 daemon 一次只跑一个任务：说了 `ready` 才派，派出后等它再说 `ready`。
- 有新任务、用户打开了某个 agent、有任务被收回时，服务叫醒该用户所有空闲的 daemon 去领。
- 领任务在一个事务里挑最早的 `pending`,条件是同一用户、agent 在这台 daemon 上 `available = 1 AND enabled = 1`。
- 租约 60 秒，心跳和每条回报都续租。daemon 掉线后租约过期，任务回到 `pending`。断线期间跑完的结果，daemon 重连后补发。

## 本机怎么跑 agent

| agent | 调用 | 进度 | 结果 |
|---|---|---|---|
| `claude` | `claude -p <prompt> --output-format stream-json --verbose [args…]` | assistant 文本、工具调用 | `result` 行 |
| `codex` | `codex exec [args…] <prompt>` | stderr 每行 | stdout,退出码 0 为成功 |
| 自定义 | `<command> [args…]`,input 原文写进 stdin | stdout 的 `{"type":"progress",…}` 行 | stdout 的 `{"type":"result","ok":…,"output":…}` 行，没有就按退出码 |

任务在 `input.cwd` 里跑;没给就在 `~/.agentuse/work/<task_id>/`。本机的启动命令和追加参数在 `~/.agentuse/daemon.json` 里配:

```json
{
  "server": "https://agentuse.example.com",
  "daemon_id": "dm_xxx",
  "key": "dk_xxx",
  "agents": {
    "claude": { "args": ["--permission-mode", "acceptEdits"] },
    "codex": { "disabled": true },
    "my-harness": { "command": "/usr/local/bin/my-harness" }
  }
}
```

改完重启 daemon 生效。
