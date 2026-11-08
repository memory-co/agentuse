# 身份三段式

> 代碼:[`pkg/signature/`](../../../pkg/signature/)、[`internal/services/token/`](../../../internal/services/token/)、[`internal/services/callback/`](../../../internal/services/callback/)

平臺不接觸 agent 令牌,agent 不直接訪問平臺。身份分三段,每段只驗自己該驗的(用戶電腦怎麼拿到自己的設備密鑰是另一條線,見 [connect.md](connect.md#4-安全)):

```
平臺 ──(平臺簽名)──▶ hub ──(運行令牌)──▶ agent ──▶ hub ──(hub 簽名)──▶ 平臺
       進來:驗籤不認人      中間:線程級、動作級           出去:平臺只驗 hub
```

## 1. 進來:平臺簽名

平臺用與 hub 約定的密鑰,對「線程、作者(id / 名字 / 角色)、內容」整個請求體簽名。hub **只驗籤,不認識用戶本人**——這是 Disqus SSO(`remote_auth_s3`)那一套:角色由平臺擔保,hub 不維護用戶表。

- 算法:`HMAC-SHA256(secret, timestamp + "." + body)`,hex 編碼,前綴 `v1=`。
- 頭:`X-AgentUse-Timestamp`(unix 秒)、`X-AgentUse-Signature`。
- 時間戳偏差超過 5 分鐘拒絕,防重放。
- 細節見 [apis/platform.md](../apis/platform.md#簽名)。

## 2. 中間:運行令牌

daemon 領取運行時,hub 爲這次運行簽發一枚令牌:

- 形態 `aut_<claims>.<sig>`,hub 自己的 `token_key` 籤,claims 見 [structs/models.md](../structs/models.md#runtoken)。
- **範圍**:一個平臺的一個線程、一次運行。
- **動作**:只限策略裏的 `actions`(如博客只有 `reply`,工單有 `reply` + `status`)。
- **時效**:最長 24 小時;運行 complete 後立刻失效(hub 檢查運行狀態)。
- **領取次數**:claims 裏帶 `attempt`。運行租約過期被回收、別的 daemon 重領後,上一次領取的令牌作廢。
- daemon 把它注入 `AGENTUSE_TOKEN`,agent 用 `agentuse reply` / `agentuse status` 調 hub。

寫回時線程、運行 id、回覆對象都**以令牌和運行爲準**,agent 在請求體裏自帶的 `thread` 一律忽略,所以一枚令牌寫不到別的線程。

## 3. 出去:hub 簽名

hub 校驗令牌後,用同一套簽名(該平臺的 `secret`)回調平臺的 `callback_url`。平臺只需要驗 hub 的簽名,然後把 `reply` 寫成 agent 應用用戶的評論、把 `status` 寫回對象。

平臺配置裏可以給回調單獨一把 `callback_secret`:notify 用 `secret` 驗,回調用 `callback_secret` 籤;不配則兩個方向共用 `secret`。拆開之後,平臺泄露其中一把,攻擊者也只能冒充一個方向。hub 用非對稱簽名(平臺只存公鑰)見 [roadmap.md](roadmap.md)。

## 4. agent 在平臺裏是誰

平臺在用戶表裏加一個代表 agent 的**應用用戶**(參考 Linear 的 `actor=app`),它:

- 是 `@claude` 能提到的名字(策略裏的 `agent_name`);
- 是 agent 回覆的作者;它的 id 寫進策略的 `agent_user_id`,hub 據此忽略 agent 自己的評論。
