# sessions

Session 是共享消息与附件的容器，不绑定 agent 或执行运行时。

| 字段 | 含义 |
|---|---|
| id | ss_ 会话 ID |
| user_id / created_by | 创建者及归属 |
| work_id | 可空，所属工作 |
| title | 标题 |
| status | idle / pending / running / waiting / resolved，由活动 run 聚合 |
| mode | 固定 multi_agent |
| created_at / updated_at | Unix 秒 |

执行者、daemon、harness、attempt 和 lease 位于 [session_runs](session_runs.md)。每次执行读取同一个 session 当前内容并启动新的原生会话。
