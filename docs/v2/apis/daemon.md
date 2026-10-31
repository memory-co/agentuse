# Daemon API 与执行协议

- `POST /api/daemon/register`：用 `{token,hostname}` 将一次性安装令牌换成 `{daemon_id,name,key}`。
- `GET /api/daemon/me`：Bearer dk_ 查询本机注册信息。
- `GET /api/daemon/ws`：Bearer dk_ 建立 WebSocket；同一 daemon 的新连接替换旧连接。

## 连接与派活

hello 上报 `harnesses`、`version`、`capabilities:["session-runs-v1","attachments-v1"]` 与 `active_runs:[{run_id,attempt_id}]`。welcome 返回 daemon_id、name、ignored 和 valid_runs。daemon 必须停止无效执行，再发 ready。

服务只领取本 daemon、运行时可用的 pending run，一次派一个执行。派活消息：

```json
{"type":"run","run":{"id":1,"run_id":"sr_xxx","attempt_id":"ra_xxx","session_id":"ss_xxx","agent_id":"coder","harness":"claude","task_message_ids":[1],"context_seq":1,"input":{"prompt":"检查实现"}}}
```

工作说明使用 instructions；仅工作指定的协调者 agent 执行获得 work_token。每次执行都启动新的原生会话，不下发 resume。

执行开始前经 HTTP GET session 读取共享消息，携带 `X-Agentuse-Run`、`X-Agentuse-Attempt`，并写入 `.agentuse/session-history.json`。附件独立走 HTTP，避免 WebSocket 帧大小限制。

## 回报与控制

- heartbeat：携带 run_id、attempt_id、message_id；租约 60 秒，每 15 秒续租。
- event：同样带执行标识，另带单调 event_seq、kind、payload；kind 支持 started、progress、plan、steered、question、answered、question_expired、done、failed。
- 服务按 run/attempt/event_seq 去重，拒绝旧 attempt 或失效租约。最终结果生成署名 assistant 消息；事件记录于 run_events。
- steer、answer：内部推送到当前 run/attempt；客户端插话统一 POST messages。
- cancel：停止指定 run/attempt。
- 执行结束后 daemon 发 ready；断线重连时重放未确认事件，服务根据有效执行清单恢复租约或重新排队。

只支持 run 协议，详见 [共享 session](../designs/session-mentions.md)。
