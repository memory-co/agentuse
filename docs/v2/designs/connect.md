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
2. agentuse register:用 TOKEN 换长期密钥(dk_xxx),写进 ~/.agentuse/daemon.json
3. 装成后台服务并启动:macOS 用 launchd,Linux 有 systemd --user 就用它，否则 nohup
        │
        ▼
daemon 连上 WebSocket,上报本机 harness ──▶ 页面上出现这台电脑和它的 harness 开关
```

- 安装令牌只能用一次，注册后作废;要换电脑或重装，在页面上「重新生成安装命令」,旧密钥同时失效。
- 二进制从服务的 `-dist` 目录取(`make dist` 交叉编译 linux/darwin × amd64/arm64)。目录里缺平台时 `agentuse-server start` 会警告。
- 用户在页面上为每台 daemon 选择开启哪些 harness,默认都关闭。

## 认证

| 谁 | 凭据 | 怎么带 |
|---|---|---|
| 用户(网页 / 脚本) | 登录令牌 `ut_` | 网页 cookie;脚本 `Authorization: Bearer ut_…` |
| daemon 注册 | 安装令牌 `st_` | `POST /api/daemon/register` 的 body |
| daemon 长连接 | 密钥 `dk_` | WebSocket 握手 `Authorization: Bearer dk_…` |

库里只存哈希(安装令牌除外，见 [daemons](../tables/daemons.md))。

## 消息

注册接口和 WebSocket 消息的完整格式见 [apis/daemon](../apis/daemon.md)。

## 派活

派给 daemon 的是 `pending` 的[会话](../tables/sessions.md),带上它最后一条 `user` 消息。

- 一台 daemon 一次只跑一个会话：说了 `ready` 才派，派出后等它再说 `ready`。
- 有新消息、用户打开了某个 harness、有会话被收回时，服务叫醒相关的空闲 daemon 去领。
- 领活在一个事务里挑最早的 `pending` 会话，条件是会话的 `daemon_id` 就是这台、会话的 harness 在这台 daemon 上 `available = 1 AND enabled = 1`。
- 会话建的时候就定了 daemon(取 [agent](../tables/agents.md) 的运行时),只给它(harness 的会话存在那台电脑上)。它离线就等它上线，不换机器。
- 租约 60 秒，心跳和每条回报都续租。daemon 掉线后租约过期，会话回到 `pending`。断线期间跑完的结果,daemon 重连后补发。

## 本机怎么跑 harness

每条消息起一次 CLI,第一条新建 harness 会话，之后用上次回报的 harness 会话 id(派活消息里的 `resume`)续接。

内置 harness 怎么起、怎么续接、怎么从输出里读出进度 / 结果 / 会话 id,都写在仓库顶层 [`harness/<name>/harness.yaml`](../../../harness/)([设计](harness.md)):

| harness | 第一条 | 之后 |
|---|---|---|
| `claude` | `claude -p --input-format stream-json --output-format stream-json --verbose --permission-mode bypassPermissions --permission-prompt-tool stdio [args…]`,prompt 作为一行 `{"type":"user",…}` 写进 stdin | 加 `--resume <id>` |
| `codex` | `codex exec --json --skip-git-repo-check -c sandbox_mode=workspace-write -c sandbox_workspace_write.network_access=true [args…] <prompt>`(沙箱可写、能联网;args 里给 `-s` 时以它为准) | 同上，`[args…]` 后面加 `resume <id> <prompt>` |
| 自定义 | `<command> [args…]`,stdin 写入 `{"session_id":"ss_…","resume":"…","instructions":"…","input":…}`(`instructions` 是[工作](work.md)的说明，没有就不带);stdout 的 `{"type":"progress",…}` 行是进度,`{"type":"result","ok":…,"output":…,"session_id":…}` 行是结果，没有就按退出码 | 同左,`resume` 有值 |

会话固定在 `~/.agentuse/work/<session_id>/` 里跑，用户不能指定，每次都是同一个目录(Claude Code 按目录找会话)。起进程前 daemon 把 harness 自带的 skills 拷进去([设计](harness.md#工作目录))。本机的启动命令和追加参数在 `~/.agentuse/daemon.json` 里配:

```json
{
  "server": "https://agentuse.example.com",
  "daemon_id": "dm_xxx",
  "key": "dk_xxx",
  "harnesses": {
    "claude": { "args": ["--model", "opus"] },
    "codex": { "disabled": true },
    "my-harness": { "command": "/usr/local/bin/my-harness" }
  }
}
```

改完重启 daemon 生效。
