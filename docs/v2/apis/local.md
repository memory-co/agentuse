# 本机只读页面与 API

本机 client 已采用 v3 的 YAML/JSON 文件存储与分级目录查询。当前 API、认证与目录说明以 [client-main.md](../../v3/designs/client-main.md#九api-设计) 为准。

`agentuse start` 启动 client，`agentuse ui -open` 打开页面。旧版 SQLite 账本、全局 `/api/runs` 和 `/api/stats` 接口已移除。
