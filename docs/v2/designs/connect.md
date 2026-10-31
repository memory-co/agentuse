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

## 派活与本机执行

服务领取 pending run，按本次执行的 daemon/harness 快照派发。一台 daemon 发 ready 后才接下一次执行。租约过期或重连时没有对应执行，服务重新排队并产生新的 attempt；旧 attempt 的事件和文件操作失效。

每次执行读取同一个 session 的共享消息和附件，在 `~/.agentuse/work/<session_id>/` 启动新的原生会话。agent、harness、机器可以在后续 run 中更换，没有原生续接状态。

本机 CLI 路径与参数由 `~/.agentuse/daemon.json` 的 harnesses 配置决定，内置命令定义见 [harness](harness.md)。协议见 [daemon API](../apis/daemon.md)，执行语义见 [共享 session](session-mentions.md)。
