# 还没做的事

骨架已经能跑通「评论 → 唤醒 → 领取 → 回复 → 回调」。下面按大致优先级列出后续:

| 事 | 现状 | 方向 |
|---|---|---|
| 持久化存储 | 内存,重启即丢 | `store.Store` 加 SQLite 实现 |
| daemon 传输 | HTTP 轮询 | 出站 WebSocket,15 秒心跳,hub 推送 |
| 会话锁 | 单 daemon 一次一条,天然串行 | 多 daemon / 并发时按会话加锁,同一会话的运行串行 |
| 运行超时与回收 | 没有 | claimed 太久没 complete 回到 queued 或标 failed |
| 密钥 | 进出共用一把平台密钥 | 拆两把,或 hub 用非对称签名,平台只存公钥 |
| 现场隔离 | 靠 Claude Code 权限模式 | 只读挂载、无网络、无凭据;公开平台放进容器 |
| 其它驱动 | 只有 Claude Code | Codex app-server 等 |
| 进度展示 | activity 原样转发 | 平台侧 SDK 提供「先占位后替换」(Liveblocks 式)的帮助函数 |
| 其它语言 SDK | 只有 Go | TypeScript(平台多是 Web),照 [apis/platform.md](../apis/platform.md) 实现 |
| 管理接口 | 没有 | 列会话 / 运行、按平台查看,供实例主人用 |
