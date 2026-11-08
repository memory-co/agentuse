# 插話

插話通過 `POST /api/sessions/{id}/messages` 發送，消息包含 prompt 與 to_agent_ids。

每個目標獨立路由：沒有活動執行時創建 run；已有 pending/running/waiting run 時記錄 steer delivery。尚未啓動的執行將文本補充合入初始輸入，運行中的執行通過內部 steer 推送。

harness 接受插話後回報 steered。沒有被接收的補充在當前執行結束後掛到後續 run，複用原用戶消息，不復制消息。取消執行時關聯投遞也取消。

所有 agent 讀取同一 session 的共享歷史。詳見 [共享 session](session-mentions.md)。

## 內置 harness

- Claude：通過 PreToolUse / Stop hook 回調注入。
- Codex：通過 app-server 的 turn/steer 注入當前活動 turn；不創建新的 turn。expectedTurnId 校驗當前執行，成功響應後回報 steered。收到 RPC 拒絕時保留投遞，不能提前標爲已送達。
- 自定義且未實現實時輸入的 harness：結束當前執行後處理未接收的消息。

新版 daemon hello 聲明 codex-steer-v1；已安裝的 daemon 必須更新二進制並重啓才具備該實現，單獨更新服務器無法改變遠端進程。

驗證：`go test -count=1 -run '^TestCodexSteer$' ./test/e2e/session` 走真實服務、daemon 和假 app-server；`AGENTUSE_REAL_CODEX=1 go test -count=1 -v -run '^TestRealCodexSteer$' -timeout 5m ./test/e2e/session` 使用本機已登錄的 Codex。真實測試在 shell 等待期間發送隨機標記，斷言只有原 run、一條最終回覆、對應的 steered 確認且回覆採納隨機標記。
