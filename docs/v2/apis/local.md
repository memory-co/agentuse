# 本机只读页面与 API

`agentuse daemon` 同时启动本机账本与页面；`agentuse ui` 打印登录链接，`agentuse ui -open` 同时打开浏览器。页面数据来自本机 `~/.agentuse/ledger.db`，无需连接服务器读取。

配置：

```json
{"ui_addr":"127.0.0.1:7801","ledger_retention_days":90}
```

省略 ui_addr 时默认开启，空字符串关闭；只允许回环地址。AGENTUSE_UI_ADDR 可覆盖监听地址。账本默认保留 90 天，记录 run/attempt、来源服务、会话发起人、agent/session/work/harness、前 200 字摘要、状态、耗时、附件数、插话次数、用量与费用。完整 prompt 不写入账本。输入 token 包含缓存部分，未提供 usage 时保留为空，不伪造为零。

`ui.token` 权限为 0600。首次通过登录链接访问后，令牌从 URL 移除并写入 HttpOnly、SameSite=Strict cookie。API 也允许 Bearer 本机令牌。Host 仅允许实际端口的 localhost/127.0.0.1，不提供 CORS，所有非 GET 请求返回 405。

- `GET /api/runs`：按时间倒序列执行，支持 sender、agent、status、from/to（YYYY-MM-DD、UTC，to 包含当天）、limit（最多 100）、offset。
- `GET /api/runs/{run}/{attempt}`：查看一条本机记录。
- `GET /api/stats?group=day|sender|agent|harness`：汇总次数、已提供用量的次数、输入/输出/缓存 tokens、费用；支持同样的筛选。

service 只拿 store.Writer，controller 只拿 store.Reader，后者是单独的 SQLite mode=ro/query_only 连接。重启时未完成记录标为失败，WebSocket 重连时保持当前执行不受影响。
