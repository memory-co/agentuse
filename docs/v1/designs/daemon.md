# daemon

> 代码:[`internal/daemon/`](../../../internal/daemon/)

daemon 是用户电脑上的常驻进程,负责博客里与平台无关六件事中的三件:**连接、驱动 CLI、现场**。

## 1. 连接

daemon **主动连出** hub,所以用户电脑在 NAT 后面也没关系,平台和 hub 都不需要能访问用户电脑。

两种传输,daemon.json 的 `transport` 选:

| transport | 怎么领 | 适合 |
|---|---|---|
| `ws`(默认) | 一条出站 WebSocket `GET /v1/daemon/connect`。空闲时发 `ready`,hub 有运行就替它领好推过来;空闲期间 hub 每 15 秒 ping。连接断了退避 5 秒重连 | 正常使用,评论一来马上开工 |
| `poll` | `POST /v1/daemon/claim`,有运行 200,队列空 204,空了等 5 秒再问 | 中间有不支持 WebSocket 的代理 |

**心跳和交回总是走 HTTP**(`/v1/runs/{run}/heartbeat`、`/complete`):它们幂等、可重试,不依赖推送连接还在。推送连接只负责「有活马上推」;如果推出去的一瞬间连接断了,那条运行靠租约过期回收。

daemon 用 `X-AgentUse-Daemon: <id>` + `Authorization: Bearer <key>` 认证。现在 id 与 key 在 hub.json 的 `daemon_keys` 里手工登记;改成平台侧实例之后,它们换成**设备 id + 设备密钥**,由用户跑安装命令时自动登记,不再有人手写([connect.md](connect.md))。领取时 hub 只给这台设备上绑定的 agent 的运行([accounts.md §4](accounts.md#4-运行只派给主人的设备))。

## 2. 现场

每个会话一个 git worktree:

- 位置:`<worktree 根>/<platform>/<session id>`,根默认 `~/.agentuse/worktrees`;
- 仓库:现在是 daemon.json 里 `workspaces[platform].repo`,如 `~/work/shop-api`;改成平台侧实例之后,是 agent 接入时绑定的那个目录(用户跑安装命令的目录),记在 hub 的 agent 上,daemon.json 不用手写;
- 分支:会话的 `branch`(`agent/claude/MUL-42`),第一次从 `base_ref`(默认 `main`)切出;
- 同一会话的后续运行复用同一个 worktree,现场接着上次;
- 没配仓库的平台(如博客问答)给一个空目录。

## 3. 驱动 CLI

`Driver` 接口把交互式 agent CLI 变成可被程序调用的。现在只有 Claude Code:

```
claude -p <提示词> --output-format stream-json --verbose \
       --allowedTools 'Bash(agentuse *)' --permission-mode <plan|acceptEdits> [--resume <agent_session_id>]
```

- 环境变量注入 `AGENTUSE_URL`、`AGENTUSE_TOKEN`;
- 从 stream-json 里读 `session_id`,complete 时交回 hub 回填到会话;
- 提示词的写法见 [wake-and-trust.md §4](wake-and-trust.md#4-提示词里怎么摆)。

Codex(app-server)等其它驱动按同一接口加。

## 4. 循环

```
for {
  next(ws 等推送 / poll 轮询)
  → 准备 worktree → 驱动 agent(同时每 15 秒 heartbeat)→ complete(attempt + 成功 / 失败 + agent_session_id)
}
```

- 一次只跑一条运行。要并行就在同一台或多台机器上多起几个 daemon(不同 id);同一线程的运行由 hub 保证串行。
- 心跳收到 409(运行被回收给了别人,或已经结束)→ 立刻停掉 agent,结果不交。
- 交回失败(网络)→ 不重试,等租约过期 hub 回收重排;重排后的运行会 `--resume` 同一个 agent 会话、复用同一个 worktree,现场接着上次。
