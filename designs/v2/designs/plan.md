# 計劃(plan step)

## 一句話

harness 回一條消息時，把它自己列的步驟清單(哪些做完了、正在做哪一步)作爲一種單獨的回報 `plan` 送上來，會話頁把最新的一份顯示成勾選列表：

```
✓ 讀文章和當前實現
✱ 改 harness.yaml
○ 跑測試並開 PR
```

不需要讓 Claude Code 跑什麼額外模式：它遇到多步任務本來就會調用自帶的待辦工具，我們只是把這些調用讀出來。

## Claude Code 怎麼列計劃

兩種工具，看用戶機器上的版本，兩種都認：

| 工具 | 怎麼用 | 我們怎麼讀 |
|---|---|---|
| `TodoWrite`(老) | 每次調用都帶**整份**清單:`input.todos = [{content, status, activeForm}]` | 直接換成這一份 |
| `TaskCreate` / `TaskUpdate`(新) | 一次建一步:`TaskCreate {subject, …}`;改狀態:`TaskUpdate {taskId, status}` | daemon 在這次運行裏攢着：建一步就追加，id 按建的順序從 `1` 編;`TaskUpdate` 按 id 改狀態,`deleted` 就刪掉這一步 |

`status` 只有 `pending` / `in_progress` / `completed` 三種，原樣用。新工具的 id 按建的順序編，已經對真實 Claude Code 驗證過(見下)。

2026-10-27 用 Claude Code 2.1.311 實測：

- 這個版本已經沒有 `TodoWrite`;`-p` 模式默認也沒有 `TaskCreate` / `TaskUpdate`,要設 `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` 纔有。
- 任務清單默認跟着 Claude 會話走，續接時編號接着上次的(第二條消息從 `#4` 開始)。所以 daemon 給每次運行設 `CLAUDE_CODE_TASK_LIST_ID=agentuse-<message_id>`,編號從 `1` 開始，和"按建的順序編"對得上。
- 這兩個環境變量寫在 claude 的 harness.yaml 的 `env` 裏。

一份清單隻屬於這一次運行(回一條 `user` 消息)。下一條消息 Claude 會重新列，不跨消息合併。

## harness.yaml

規則里加兩個鍵：

| 鍵 | 作用 |
|---|---|
| `plan` | 一次給整份清單:`steps` 是數組路徑,`text` / `status` 是數組裏每一項的字段 |
| `plan_add` / `plan_update` | 增量：`plan_add: {id, text}` 追加一步(`id` 不寫就按順序編);`plan_update: {id, status}` 改一步 |
| `final: true` | 命中這條後，同一層後面的規則不再匹配(免得計劃工具再出一行"🔧 TodoWrite …"進度) |

claude 的規則變成：

```yaml
    - match: { type: assistant }
      each: message.content
      rules:
        - match: { type: text }
          progress: "{text}"
        - match: { type: tool_use, name: TodoWrite }
          plan: { steps: input.todos, text: content, status: status }
          final: true
        - match: { type: tool_use, name: TaskCreate }
          plan_add: { text: input.subject }
          final: true
        - match: { type: tool_use, name: TaskUpdate }
          plan_update: { id: input.taskId, status: input.status }
          final: true
        - match: { type: tool_use }
          progress: "🔧 {name} {input}"
```

codex 現在沒有對應的事件，不寫;以後 codex 有了再加規則。自定義 harness 直接輸出 `{"type":"plan","steps":[…]}` 行就行，daemon 原樣轉成 `plan` 回報。

爲了讓 Claude 更常列計劃,[agentuse skill](../../../internal/harness/claude/skills/agentuse/SKILL.md) 里加一句：步驟多於兩三步的任務，先用待辦工具列出計劃，邊做邊更新。

## 回報

daemon 每次清單變了就發一條 `plan` 回報，帶**整份**清單(服務和前端不用自己拼):

```json
{ "type": "event", "message_id": 3, "kind": "plan", "payload": { "steps": [
  { "text": "讀文章和當前實現", "status": "completed" },
  { "text": "改 harness.yaml", "status": "in_progress" },
  { "text": "跑測試並開 PR", "status": "pending" }
] } }
```

- 服務當普通回報處理：寫進 [run_events](../tables/session_runs.md)(`kind = plan`)、續租。不加表、不加字段：一條消息的最新計劃就是它最後一條 `plan` 回報。
- `GET /api/sessions/{id}` 的 `events` 裏照常返回。

## 前端

會話頁每條 `user` 消息下面，如果有 `plan` 回報，取最後一份顯示成勾選列表(✓ 完成、轉圈 進行中、○ 未開始),放在"進度 N 條"摺疊框上面。會話 `running` 時跟着輪詢刷新;跑完以後留着，回看時能看到最後停在哪一步。

## 實現步驟

已完成。

1. `harness.Spec` 規則加 `plan` / `plan_add` / `plan_update` / `final`;`harness.State` 攢當前清單，清單變了回調一次(帶測試:TodoWrite 整份、TaskCreate + TaskUpdate 增量、`final` 不再出進度)。
2. daemon 把回調發成 `plan` 回報;自定義 harness 的 `{"type":"plan"}` 行也轉成 `plan`。
3. 服務接受 `plan` 這個 kind(和 `progress` 一樣處理)。
4. 前端顯示勾選列表;skill 加一句"先列計劃"。
5. e2e:fakeharness 的 claude 模式輸出 TodoWrite / TaskCreate / TaskUpdate,檢查會話裏的 `plan` 回報。
