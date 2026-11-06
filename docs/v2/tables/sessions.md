# sessions

Session 是共享消息與附件的容器，不綁定 agent 或執行運行時。

| 字段 | 含義 |
|---|---|
| id | ss_ 會話 ID |
| user_id / created_by | 創建者及歸屬 |
| work_id | 可空，所屬工作 |
| title | 標題 |
| status | idle / pending / running / waiting / resolved，由活動 run 聚合 |
| mode | 固定 multi_agent |
| created_at / updated_at | Unix 秒 |

執行者、daemon、harness、attempt 和 lease 位於 [session_runs](session_runs.md)。每次執行讀取同一個 session 當前內容並啓動新的原生會話。
