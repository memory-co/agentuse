# daemon

> 代码:[`internal/daemon/`](../../internal/daemon/)

daemon 是用户电脑上的常驻进程,负责博客里与平台无关六件事中的三件:**连接、驱动 CLI、现场**。

## 1. 连接

daemon **主动连出** hub,所以用户电脑在 NAT 后面也没关系,平台和 hub 都不需要能访问用户电脑。

- 现在:HTTP 轮询。`POST /v1/daemon/claim`,有运行返回 200,队列空返回 204,空了等 5 秒再问。
- 下一步:出站 WebSocket,15 秒心跳,hub 有运行时直接推。消息形状和轮询一致(见 [apis/daemon.md](../apis/daemon.md)),换传输不改语义。

daemon 用 `X-AgentUse-Daemon: <id>` + `Authorization: Bearer <key>` 认证,id 与 key 在 hub.json 的 `daemon_keys` 里登记。

## 2. 现场

每个会话一个 git worktree:

- 位置:`<worktree 根>/<platform>/<session id>`,根默认 `~/.agentuse/worktrees`;
- 仓库:daemon.json 里 `workspaces[platform].repo`,如 `~/work/shop-api`;
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
  claim → 204? 等 Interval
        → 200: 准备 worktree → 驱动 agent → complete(成功 / 失败 + agent_session_id)
}
```

一次只跑一条运行。并发与会话锁见 [roadmap.md](roadmap.md)。
