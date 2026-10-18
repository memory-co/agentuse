# hub 实例

> 代码:[`internal/hub/`](../../internal/hub/)、[`internal/store/`](../../internal/store/)

## 1. 定位:属于用户的实例

hub 不是一家公司的公共服务,而是**实例**:用户或团队自建,或买托管的(如 `hub.alice.dev`)。平台在设置页里填两样东西:**地址**和**密钥**。类比 Redis——协议固定,实例可换。

hub 只保存三样东西:

| 东西 | 是什么 | 结构 |
|---|---|---|
| 会话表 | 线程键 → agent 会话 | [Session](../structs/models.md#session) |
| 运行队列 | 每次唤醒一条运行,queued → claimed → succeeded / failed | [Run](../structs/models.md#run) |
| 策略 | 按平台:谁能唤醒、谁的话可信、agent 能做什么、现场权限 | [Policy](../structs/models.md#policy) |

**不存评论。** 平台 notify 时可以把线程里最近的评论一起带上(`context`),hub 只把它们挂在这次运行上交给 daemon,不另存一份。

## 2. 会话映射

- 键是 `(platform, thread)`。线程键的格式由平台决定,hub 不解析,只在算分支名时去掉第一个 `:` 之前的类型前缀。
- 第一次被唤醒时建会话,分支名 `agent/<agent_name>/<线程键安全形式>`,如 `issue:MUL-42` → `agent/claude/MUL-42`。
- 第一次运行结束时 daemon 回填 `agent_session_id`(Claude Code 的 session id),之后的运行用 `--resume` 续接。

## 3. 运行队列

```
queued ──claim──▶ claimed ──complete──▶ succeeded
                     │                     
                     └────complete(err)──▶ failed
```

- 一次 notify 唤醒 = 一条运行。同一线程连着 @ 两次就是两条运行,按先后被领取;同一会话的运行目前不加锁,daemon 一次只跑一条,所以单 daemon 下天然串行。多 daemon 下的会话锁见 [roadmap.md](roadmap.md)。
- 只有 claimed 状态的运行能写回;complete 之后令牌即使没过期也写不了(409)。
- 只有领取它的那个 daemon 能 complete 它。

## 4. 存储

`store.Store` 是接口,现在只有内存实现(重启即丢)。持久化(SQLite)按同一接口加,见 [roadmap.md](roadmap.md)。
