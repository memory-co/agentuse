# Daemon API 與執行協議

- `POST /api/daemon/register`：用 `{token,hostname}` 將一次性安裝令牌換成 `{daemon_id,name,key}`。
- `GET /api/daemon/me`：Bearer dk_ 查詢本機註冊信息。
- `GET /api/daemon/ws`：Bearer dk_ 建立 WebSocket；同一 daemon 的新連接替換舊連接。

## 連接與派活

hello 上報 `harnesses`、`version`、`capabilities:["session-runs-v1","attachments-v1","codex-steer-v1"]` 與 `active_runs:[{run_id,attempt_id}]`。welcome 返回 daemon_id、name、ignored 和 valid_runs。daemon 必須停止無效執行，再發 ready。

服務只領取本 daemon、運行時可用的 pending run，一次派一個執行。派活消息：

```json
{"type":"run","run":{"id":1,"run_id":"sr_xxx","attempt_id":"ra_xxx","session_id":"ss_xxx","agent_id":"coder","harness":"claude","task_message_ids":[1],"context_seq":1,"input":{"prompt":"檢查實現"}}}
```

工作說明使用 instructions；僅工作指定的協調者 agent 執行獲得 work_token。每次執行都啓動新的原生會話，不下發 resume。

執行開始前經 HTTP GET session 讀取共享消息，攜帶 `X-Agentuse-Run`、`X-Agentuse-Attempt`，並寫入 `.agentuse/session-history.json`。附件獨立走 HTTP，避免 WebSocket 幀大小限制。

## 回報與控制

- heartbeat：攜帶 run_id、attempt_id、message_id；租約 60 秒，每 15 秒續租。
- event：同樣帶執行標識，另帶單調 event_seq、kind、payload；kind 支持 started、progress、plan、steered、question、answered、question_expired、done、failed。
- 服務按 run/attempt/event_seq 去重，拒絕舊 attempt 或失效租約。最終結果生成署名 assistant 消息；事件記錄於 run_events。
- steer、answer：內部推送到當前 run/attempt；客戶端插話統一 POST messages。
- cancel：停止指定 run/attempt。
- 執行結束後 daemon 發 ready；斷線重連時重放未確認事件，服務根據有效執行清單恢復租約或重新排隊。

只支持 run 協議，詳見 [共享 session](../designs/session-mentions.md)。
