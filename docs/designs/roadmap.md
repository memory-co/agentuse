# 还没做的事

已经做了:SQLite 持久化、WebSocket 推送 + 心跳租约、同一线程串行、过期回收重排、回调独立密钥、管理查询接口。下面按大致优先级列出后续:

| 事 | 现状 | 方向 |
|---|---|---|
| 现场隔离 | 靠 Claude Code 权限模式(`plan` / `acceptEdits` + 工具白名单) | 只读挂载、无网络、无凭据;公开平台放进容器跑 |
| 回调重试 | 回调失败直接 502 交还给 agent | hub 侧带退避重试,或落一张待投递表 |
| 非对称签名 | 进出各一把对称密钥 | hub 用 Ed25519 签回调,平台只存公钥 |
| 其它驱动 | 只有 Claude Code | Codex app-server 等 |
| daemon 并发 | 一个 daemon 一次一条 | 一个进程内多 worker,按平台限并发 |
| 进度展示 | activity 原样转发 | 平台侧 SDK 提供「先占位后替换」(Liveblocks 式)的帮助函数 |
| 其它语言 SDK | 只有 Go | TypeScript(平台多是 Web),照 [apis/platform.md](../apis/platform.md) 实现 |
| 管理写操作 | 只读 | 取消排队中的运行、清理旧运行记录 |
