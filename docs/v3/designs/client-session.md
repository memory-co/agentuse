# Client sessionService：原生 Session 与领来的 Run

> v3 目标设计，尚未实现。Session 是 client 本地的原生执行会话记录，直接对应 Codex thread 或 Claude Code session。Conversation 是 server 的共享对话，二者不再混用。配置、session 与执行账本共用 client 本机 SQLite，关联字段不使用外键；本篇时间字段使用 Unix 毫秒。

## 一、三个对象的关系

| 对象 | 属于哪里 | 含义 |
|---|---|---|
| conversation | server | 多 agent 共享的消息和文件空间 |
| session | client / 原生 harness | 一个可继续使用的原生执行上下文 |
| run | server 分配，client 领取 | 一次任务；绑定本机 session 后执行 |

一个 session 包含若干个从 server 领取的 run。Client 不创建独立于 server 的业务 run，也不因调用了原生 API 就另造一个 run_id；本机 runs 表记录 server run 的执行尝试。

```text
中心 A / Conversation C / Agent coder
  └─ Client Session S1 ↔ Codex Thread T1
       ├─ server Run R1 / Attempt A1 → Turn U1
       └─ server Run R2 / Attempt A2 → Turn U2（沿用 T1）

中心 A / Conversation C / Agent reviewer
  └─ Client Session S2 ↔ Claude Code Session H1
       ├─ server Run R3 / Attempt A3 → 首次执行
       └─ server Run R4 / Attempt A4 → 恢复 H1 执行
```

C 是共享对话，S1/S2 是各自原生上下文。一台设备上的不同 agent 不共用同一个原生 session，但它们仍从 server 读取同一个 conversation 的共享消息。

Session 对应原生历史，不等于操作系统进程。一次 Claude print 进程结束不代表 session 被删除；Codex app-server 连接也可以重新建立后恢复原 thread。

![领来的 Run 与原生 Session](../../images/client-sessions.svg)

## 二、sessionService 做什么

- 为领取的 run 选择可复用的本地 session，或请求 harnessService 创建新的原生 session。
- 保存本地 session 与原生会话 ID 的一对一映射。
- 串行管理同一个 session 的执行，维护上下文同步位置。
- 在 run 结束后保留 session，供后续领来的 run 继续使用。
- 处理 session 恢复、失效、关闭及本地历史查询。

endpointService 只管已注册 server 的信息；client runService 管连接、领取、本机容量和 run/attempt 生命周期；sessionService 管跨 run 的原生上下文；harnessService 管创建、恢复、输入和原生交互的具体实现。

## 三、收到 Run 后怎样选 Session

默认按 `(endpoint_id, conversation_id, agent_id, harness)` 查找当前 session，并检查 harness_revision 与本地可恢复条件。

1. client runService 从 endpointService 读取注册信息，预留本机槽位并向对应 server 领取 run。
2. client runService登记 `(endpoint_id, run_id, attempt_id)`，交 sessionService 选择会话。
3. 有可恢复、空闲且版本匹配的当前 session，则复用；没有则建立新的本地记录，让 harnessService 创建原生会话并回填 native_session_id。
4. 获得 session 执行锁后绑定本机 run 记录，准备当前输入和上下文，交 harnessService 开始本次执行。
5. 结束 run 后更新账本，释放 session 锁与全局槽位；session 恢复 idle，保留历史，不随 run 完成关闭。

同一个 session 同时最多执行一个 run。已忙碌的 session 不能被另一个任务强行复用，插话则进入当前 run 的输入，不因此创建另一个 session。

中心、conversation、agent 或 harness 不同不能复用；接入包版本变化、原生历史丢失或不可恢复时，将旧 session 标为非当前，再创建新的 session。不得把旧记录的 native_session_id 改成另一个原生会话 ID。

## 四、Codex 与 Claude Code 怎样对应

| 本地操作 | Codex | Claude Code |
|---|---|---|
| 首次建立 session | 建立 thread，保存 thread.id | 首次执行，保存原生返回的 session ID |
| 在 session 中执行 run | 对同一 thread 发起本次 turn | 对同一原生 session 执行本次输入 |
| 进程/连接已退出后继续 | 初始化连接后恢复指定 thread，再开始新 turn | 按保存的 ID 恢复指定 session，继续 print 执行 |
| run 执行中插话 | 关联当前 thread/turn | 关联当前原生执行的 hook/控制请求 |
| run 结束 | 当前 turn 结束，thread 历史保留 | 本次执行/进程结束，session 历史保留 |

Codex 的恢复句柄使用 `thread.id`，不把 `turn.id` 或可能出现的 thread.sessionId 聚合标识当成另一个可互换的恢复 ID。Claude 恢复时使用指定 session ID，不用“当前目录最近一个 session”猜测，也不默认 fork 新 session。

原生接口依据 [Codex App Server](https://learn.chatgpt.com/docs/app-server) 的 thread/start、thread/resume、turn/start，以及 [Claude Code CLI](https://code.claude.com/docs/en/cli-reference) 的指定 session 恢复能力。具体调用与插话/提问细节仍归 [harnessService](client-harness.md)，不交给 server 解释。

## 五、共享历史和原生历史

Server conversation 仍是共享对话的权威来源，本地原生 session 保留自己的执行历史。复用 session 时，不再把整个 conversation 原样重复塞进已有原生上下文。

sessionService 依据 last_context_seq 读取共享历史增量，结合当前 run_prompts 组织本次输入。需跳过原生会话中已存在的任务输入和本机回复：可结合本地执行记录、run_prompts 及 server runs.result_message_id 核对。确认输入交付后更新同步位置，不能在读取或排队时提前推进。

首次创建或原生历史不可恢复时，用共享历史重新准备上下文；复用时追加新的共享消息和本次任务。一个会话的同步位置不更新另一个会话的同步位置。

同一个 session 执行多个 run 时，用量按本次 turn/执行的新增量记录。若原生通知提供累积值，需要按本次基线计算，不能每个 run 都重复累计整个 session 的历史用量。

## 六、本机表结构

### sessions

每行对应一个确定的原生 session。id 是本机生成的 UUID；native_session_id 是原生程序返回的 opaque ID，不要求符合 UUID 格式。创建尚未完成时 native_session_id 为空，成功后固定不变。

```sql
CREATE TABLE sessions (
    id TEXT NOT NULL PRIMARY KEY,
    endpoint_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    harness TEXT NOT NULL,
    harness_revision TEXT NOT NULL,
    native_session_id TEXT,
    directory TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'creating'
        CHECK(status IN ('creating','idle','running','unavailable','closed')),
    is_current INTEGER NOT NULL DEFAULT 1 CHECK(is_current IN (0,1)),
    last_context_seq INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX sessions_current
    ON sessions(endpoint_id,conversation_id,agent_id,harness)
    WHERE is_current=1;
CREATE UNIQUE INDEX sessions_native
    ON sessions(harness,native_session_id)
    WHERE native_session_id IS NOT NULL;
```

directory 是恢复该原生 session 时使用的工作目录；同 conversation 的业务文件可共享，原生会话 ID 和控制资源仍独立。status 表示本地执行/可恢复状态，等待问题期间仍属 running，不是 server conversation 的聚合状态。is_current 表示相同业务上下文后续 run 的默认选择；替换当前 session 时在本机事务里更新旧标记并创建新记录，历史保留。

本机 runs 表由 [client runService](client-run.md) 维护，session_id 关联这里的 sessions.id。每个 session 包含若干个领来的 run；同一个 run 的不同 attempt 可以各有本地执行记录，不另造服务端任务。

## 七、恢复和清理

- 网络重连保持仍受控的 session/run；server 确认的 valid_runs 只用于恢复对应中心的执行身份。
- client 重启先对账，不因本地存在 session 就自动恢复执行旧 run；只有领到或确认有效的任务才能开始工作。
- 原生历史缺失或恢复失败则标记不可用；重新建立 session 时保留旧执行关联和失败原因。
- 取消 run 结束当前执行，不必删除原生 session；本地适配确认历史仍可恢复时，后续 run 可以继续使用。
- 默认 90 天的执行账本清理只处理过期执行记录，不删除当前 session、在用目录或原生 transcript。关闭/清理原生 session 是独立本地操作，不删除 server conversation。

## 相关设计

[client runService](client-run.md) · [Client 总览](client-main.md) · [endpointService](client-endpoint.md) · [harnessService](client-harness.md) · [Server conversation](server-conversation.md) · [Server run](server-run.md)
