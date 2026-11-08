# 嵌入到網站

agentuse 可以嵌進任何網站，給網站的用戶提供「用自己電腦上的 harness 幹活」的能力。它的定位像 **Redis**:網站自己部署一個 agentuse-server,只有網站的**後端**調用它，網站的用戶不直接接觸 agentuse。

```
 網站用戶的瀏覽器                網站後端                        agentuse-server
┌──────────────┐  網站自己的   ┌──────────────┐  服務密鑰 +      ┌──────────────┐
│  網站的頁面  │ ────────────▶ │  網站的接口  │ ───────────────▶ │  /api/*      │
│ (網站自己畫) │  登錄 / 接口  │  鑑權、轉發  │  X-Agentuse-User │  (內網)      │
└──────────────┘               └──────────────┘                  └──────┬───────┘
                                                                        │ WebSocket
 網站用戶的電腦  ── curl setup.sh / 下載二進制 / daemon 長連接 ─────────┘ (公網)
```

## 和 Redis 的類比

| | Redis | agentuse |
|---|---|---|
| 部署 | 網站自己起一個實例 | 網站自己起一個 `agentuse-server`,一個 SQLite 文件 |
| 誰調用 | 只有後端 | 只有後端，用一個服務密鑰 |
| 用戶 | 不知道用戶是誰，key 由後端拼 | 後端每次帶上**網站自己的 user id** |
| 多個網站 | 各起各的實例 | 各起各的實例，互不相干，不做多租戶 |

## 用戶：直接用網站的 user id

- agentuse 不管登錄。網站後端認好用戶之後，調 agentuse 時帶上這個用戶在網站裏的 id,agentuse 就把它當成那個用戶。
- [users](../tables/users.md) 的 `id` 就是網站的 user id(如 `u_10086`)。daemon、agent、會話、自定義 harness 都掛在它下面，網站不用另存映射表。
- 用戶第一次出現時自動創建(`role = user`,`name` 取 id),網站不用提前同步用戶。刪用戶、改名都不需要通知 agentuse。

## 認證：服務密鑰 + 代爲某個用戶

網站後端拿 admin 的令牌當**服務密鑰**,每個請求帶兩個頭:

```
Authorization: Bearer ut_…(admin 令牌)
X-Agentuse-User: u_10086
```

- 帶了 `X-Agentuse-User`:按這個用戶的身份執行，權限和普通用戶完全一樣(只能看到他自己的 daemon、會話)。用戶不存在就先建。
- 不帶：就是 admin 本身，用來做管理(登記內置 harness、看全局)。
- 只有 admin 令牌能用這個頭，普通用戶令牌帶了返回 403。
- 服務密鑰只放在網站後端，不能下發到瀏覽器。

## 網站要做的事

網站用自己的頁面和接口包一層，後端把請求轉給 agentuse 的 [接口](../apis/README.md):

| 網站上的功能 | 後端調用(帶 `X-Agentuse-User`) |
|---|---|
| 「連接我的電腦」 | [`POST /api/daemons`](../apis/daemons.md),把返回的安裝命令展示給用戶複製 |
| 看電腦在不在線、開關 harness | `GET /api/daemons`、`PUT /api/daemons/{id}/harnesses/{harness}` |
| 建 agent、切換它的運行時(daemon + harness) | [`POST /api/agents`](../apis/agents.md)、`PATCH /api/agents/{id}` |
| 用某個 agent 發起一次任務 | [`POST /api/sessions`](../apis/sessions.md) |
| 接着回覆 | `POST /api/sessions/{id}/messages` |
| 看進度和結果 | `GET /api/sessions/{id}`,輪詢 |

現成的客戶端見 [sdk/](../../../sdk/README.md)(Python / Node.js / Go),封裝了服務密鑰、`X-Agentuse-User` 和上面這些接口。

用戶複製的安裝命令裏是 agentuse-server 的公網地址，用戶電腦直接連它，不經過網站後端。

TODO:進度現在只能輪詢，以後加增量拉取(`after=<event id>`)和回調(webhook),讓網站後端不用一直輪詢。

## 網絡

agentuse-server 的地址分兩類，部署時用反向代理分開:

| 路徑 | 誰訪問 | 暴露到 |
|---|---|---|
| `/api/*`(除下面的) | 網站後端 | 只在內網 |
| `/setup.sh`、`/download/*`、`/api/daemon/*`(含 WebSocket) | 網站用戶的電腦 | 公網 |

`agentuse-server start` 的 `-public-url` 填公網那個地址，安裝命令裏用的就是它。

## 自帶網頁

[總體設計](main.md#網頁)裏的網頁照常可用，嵌入時當作**運維後臺**給網站自己的管理員用(登 admin 令牌),網站的用戶看不到它。
