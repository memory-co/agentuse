# userService：用戶與訪問身份

> 用戶身份與令牌模型已落實到 schema 7。Work 改用父子 conversation 的部分仍為待實作目標，見 [父子 conversation 設計](server-conversation.md#父子對話)。時間為 Unix 秒，關聯由所屬 service 校驗與清理，不聲明數據庫外鍵。

## 職責

userService 管理用戶身份、角色和訪問令牌。它回答“當前請求是誰、能以什麼身份訪問”，資源是否歸這個用戶訪問，由對應業務 service 判斷。

- 創建用戶，維護 admin/user 角色。
- 簽發、重置和校驗用戶令牌，只存哈希，明文只在簽發時返回。
- 支持管理員明確代用戶操作；設備憑據交給 deviceService 校驗；conversationService 校驗對話及父子關係的業務訪問權限，Work 入口復用這些結果。

## 操作與協作

| 操作 | 行爲 |
|---|---|
| 創建用戶 | 校驗 ID、名稱和角色，插入用戶並返回初始令牌 |
| 登錄/鑑權 | 計算令牌哈希，查詢有效用戶，返回身份 |
| 重置令牌 | 替換 token_hash，舊令牌立即失效 |
| 管理員代辦 | 驗證 admin 權限後解析目標用戶；後續操作以該身份進行 |

一個用戶擁有多個 device、agent 和 conversation；Work 視圖直接組織該用戶的父子 conversation。userService 不直接清理這些關聯業務；v3 第一版不提供刪除用戶操作，以免把資源刪除策略混入身份管理。

## 表結構

### users

用戶主表。`id` 是穩定身份，`name` 是唯一顯示/登錄名稱，`role` 由 service 限定爲 admin/user；`token_hash` 唯一。重置令牌不改變用戶 ID，也不改變已有資源歸屬。

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'user',
    token_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX users_token
    ON users(token_hash);
```

## 相關設計

[Server 總覽](server-main.md) · [deviceService](server-device.md) · [agentService](server-agent.md) · [skillService](server-skill.md) · [Client harnessService](client-harness.md) · [conversationService](server-conversation.md) · [runService](server-run.md) · [fileService](server-file.md) · [Work 視圖](server-work.md)
