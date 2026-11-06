# 工作(work)

工作 = 一個**協調者會話** + 若干**線程會話** + 一份共用**說明**，照 Claude Projects 的樣子(就像這裏：項目聊天裏有一個協調者，具體的活各開一個線程)。比如「重構 daemon」：用戶只跟協調者說要做什麼，協調者開幾個線程分別改代碼、寫測試、寫文檔，線程做完結果回到協調者，協調者再彙總給用戶。

表見 [tables/works](../tables/works.md),接口見 [apis/works](../apis/works.md)。

## 三種會話

| | 怎麼區分 | 在哪顯示 |
|---|---|---|
| 普通會話 | `work_id` 爲空 | 側邊欄「會話」tab |
| 協調者 | `works.coordinator_id` 指向它(`work_id` 也是這個工作) | 工作頁的主對話 |
| 線程 | `work_id` 有值，又不是協調者 | 「工作」tab 裏該工作下面、工作頁的線程列表 |

- 協調者用 `works.coordinator_id` 認，不在 sessions 上加類型字段：一個工作正好一個協調者，放在 works 上天然唯一。接口裏的 session 對象多一個派生字段 `work_role`:`coordinator` / `thread`,不在工作裏時不出現。
- **進了工作的會話不在「會話」tab 裏顯示**,只在工作下面看。「會話」tab 只列 `work_id` 爲空的。

## 規則

- **工作是個人的**:屬於建它的用戶，只有主人(和 admin)能看、改、刪。
- **建工作時選一個 agent 給協調者**:同時建出協調者會話(`idle`,還沒有消息)。之後用戶在工作頁發的話就是發給協調者的消息，接口和普通會話一樣。
- **線程只能由協調者開**:協調者通過 `agentuse work new` 開(見下)。用戶不能手動開線程，只跟協調者說話；用戶令牌帶 `work_id` 建會話返回 403。**線程可以用工作主人名下的任何 agent**,不限於協調者所在的這臺機器：agent 在哪臺 daemon 上，線程就跑在哪臺上(`agentuse work agents` 列出的就是這些);別人的 agent 不行(403)。用戶仍然可以打開線程看、給它發消息、標記完成。
- **已完成**:線程(和普通會話)可以標記成 `resolved`,人在頁面上點，或者協調者用 `agentuse work resolve`。協調者不能標記，它代表整個工作。給已完成的線程再發消息會重新打開它。
- **`work_id` 建了就定死**:會話建的時候屬於哪個工作就是哪個，之後不能移進、移出、換工作;普通會話也不能後來放進工作。協調者不能單獨刪。
- **刪除工作**:連協調者和所有線程一起刪(和 Claude Projects 一樣);有會話在跑時返回 409。
- **文件(知識庫)**:Claude Projects 還能掛文件，先不做。

## 協調者怎麼開線程、怎麼收結果

**開線程 / 發消息 / 看結果**:協調者跑的時候,daemon 給 harness 進程加環境變量:

| 變量 | 值 |
|---|---|
| `AGENTUSE_SERVER` | 服務地址 |
| `AGENTUSE_WORK` | 工作令牌 `wt_…`(變量名不帶 TOKEN:codex 默認會把名字含 KEY/SECRET/TOKEN 的變量從它跑的命令裏過濾掉),服務在派活消息裏給，只在這條消息跑着時有效，只能動這個工作的線程 |

協調者用本機的 `agentuse work` 命令(讀這兩個變量調接口,[cli](../cli/agentuse.md#work)):

```
agentuse work agents                         # 能用哪些 agent
agentuse work threads                        # 線程列表和狀態
agentuse work new --agents code-helper "改 daemon 的重連邏輯"   # 開線程，打印線程 id
agentuse work send --agents code-helper ss_xxx "再補個測試"        # 給線程發消息(在跑就是插話)
agentuse work read ss_xxx                    # 看線程的消息
agentuse work resolve ss_xxx                 # 線程的活做完了，標記已完成
```

daemon 還把自己所在的目錄放到 PATH 最前面，保證找得到 `agentuse`;協調者的說明文件(見下)**最前面**放一段固定的規矩：只派活不幹活(哪怕活很小也開線程，不用 harness 自帶的子 agent)、這些命令怎麼用、開完線程就結束這一輪等結果回來、`agentuse work` 報錯就原樣告訴用戶而不是繞過去自己幹。工作的說明接在後面，標題「工作說明」。codex、自定義 harness 也一樣適用，所以不做成 claude 專用的 skill。

工作令牌存在 [work_run_tokens](../tables/session_runs.md) 裏，派活給協調者時生成，這一輪結束時刪掉。

**結果回到協調者**:線程每回完一條(`done` / `failed`),服務給協調者追加一條消息，內容是:

```json
{ "prompt": "[線程 ss_xxx「改重連邏輯」回覆了]\n…回覆正文，最長 4000 字…", "from_session": "ss_xxx" }
```

協調者 `idle` 時它就是下一條消息，會叫醒協調者;協調者在跑時作爲[插話](steer.md)送進去。頁面上 `from_session` 的消息不顯示成用戶氣泡，顯示成一張「線程 xxx 回覆了」的小卡片，點了進線程。

## 說明怎麼交給 harness

派活時，會話在工作裏(協調者和線程都算),服務就把工作的 `instructions` 放進 daemon 收到的 `message` 裏([apis/daemon](../apis/daemon.md)),沒有就不帶。daemon 每次起進程前在會話目錄裏寫好(協調者的是上面那段規矩在前、工作說明在後):

| harness | 寫到哪 |
|---|---|
| `claude` | `~/.agentuse/work/<session_id>/CLAUDE.md` |
| `codex` | `~/.agentuse/work/<session_id>/AGENTS.md` |
| 自定義 | 不寫文件,stdin 裏多一個 `"instructions": "…"` 字段 |

- 寫哪個文件由 `internal/harness/<name>/harness.yaml` 的 `instructions: CLAUDE.md` 一項指定([harness](harness.md))。
- **每條消息都重寫**:說明改了，下一條就用新的;沒有說明就刪掉這個文件。這個文件歸 agentuse 管,harness 自己改了也會被覆蓋。

## 網頁

側邊欄原來的會話列表上方加兩個 tab:**會話** / **工作**(英文 Sessions / Works),選中哪個記在 localStorage。

- **會話** tab:只列普通會話(`work_id` 爲空);已完成的收在列表最下面的「已完成 (n)」裏，默認摺疊。
- **會話頁**:標題旁菜單裏有「標記完成」/「取消完成」;已完成的會話底部輸入框照樣能用，發了就重新打開。
- **會話** tab:和工作 tab 一樣，頂部一個「+ 新會話」(側欄上面的導航不再放新會話);一個都沒有時顯示虛線框空狀態(「讓 agent 幫你乾點活」+「新會話」按鈕)。已完成的會話收在「已完成 (n)」裏。
- **工作** tab:列出我的工作，按 `updated_at` 新的在前，每個工作可以展開，下面列它的線程(帶狀態點);已完成的線程默認收起在「已完成 (n)」裏。頂部一個「+ 新建工作」;一個都沒有時顯示虛線框空狀態(「讓協調者幫你拆活、開線程」+「新建工作」按鈕)。點工作名進 `/works/:id`,點線程進那個會話。
- **新建工作**:填名字(必填)、選協調者的 agent(必填，下拉框只列自己的 agent,每項顯示「名字 · 機器 · harness」,執行器沒開啓的灰掉不能選)、說明(可空),建好進工作頁。
- **工作頁**(右側主區)就是和協調者的對話，跟會話頁一樣(消息、進度、底部輸入框);線程回來的消息顯示成小卡片。頂部：工作名(點了能改)、「說明」按鈕(彈框編輯)、菜單裏刪除工作。沒有「新線程」入口，線程都在側欄工作下面列着。
- **線程頁**:就是會話頁，頂部標題前面顯示「工作名 /」,點了回工作頁。
- 手機：同樣的 tab 放在側邊欄那一屏裏。
