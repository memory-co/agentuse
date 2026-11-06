# 執行中的提問

harness 回報 question 事件，服務記錄 run_questions 並將對應 run 設爲 waiting。問題屬於 run 和 attempt，多個 agent 可以同時在同一個 session 提問。

用戶經 `POST /api/sessions/{id}/runs/{run_id}/questions/{qid}/answer` 提交 answers，服務將答案送到對應執行；收到 answered 事件後恢復 running。過期問題不能再次回答。

執行被回收時，尚未送達的答案進入共享消息，下一次執行可讀取；舊 attempt 的提問與答案不能影響新執行。

見 [Sessions API](../apis/sessions.md) 與 [共享 session](session-mentions.md)。
