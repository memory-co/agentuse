# harness 描述文件

## 一句話

倉庫頂層加一個 `internal/harness/` 目錄，**一個 harness 一個子目錄**,裏面的 `harness.yaml` 寫清楚這個 CLI 的命令入口點位：怎麼探測、第一條消息怎麼起、之後怎麼續接、輸出怎麼讀出進度 / 結果 / 會話 id。daemon 和服務都從這裏讀，加一個新 harness 只要加一個目錄，不用再寫 Go。以後這個 harness 專屬的 hook、plugin、skills 也放進它自己的目錄。

```
harness/
├── embed.go            // go:embed,和 frontend/embed.go 一樣編進二進制
├── claude/             // Claude Code
│   ├── harness.yaml    // 命令入口點位(必須有)
│   ├── hooks/          // 以後：Claude Code 的 hook,比如插話
│   ├── plugins/        // 以後：隨 agentuse 帶的 plugin
│   └── skills/         // 隨 agentuse 帶的 skills,跑之前拷進會話目錄的 .claude/skills/
└── codex/
    └── harness.yaml
```

目錄名就是 harness 的 `name`。只有 `harness.yaml` 是必須的，其餘子目錄按需加。

## 爲什麼

現在 harness 的知識散在代碼裏：

- `server/internal/store/seed.go`:內置 harness 的名字、顯示名、說明(寫進 [harnesses](../tables/harnesses.md) 表)。
- `client/internal/service/harnesses.go`:默認命令、`--version` 探測、拼參數(`-p` / `--resume` / `exec … resume`)、`runClaude` / `runCodex` 兩套解析。
- [connect](connect.md#本機怎麼跑-harness) 裏再抄一遍命令表。

三處要一起改，加 Gemini CLI、opencode 之類就得寫一套新的 Go 解析。收進 `internal/harness/<name>/` 以後，這個目錄就是這個 harness 的全部事實。

## harness.yaml 長什麼樣

### claude/harness.yaml

```yaml
name: claude
display_name: Claude Code
description: Anthropic Claude Code CLI(claude -p)

# 本機可執行文件，daemon.json 的 harnesses.claude.command 可以覆蓋
command: claude

# 探測：能在 PATH 裏找到 command 就算可用;version 的輸出第一行記成 daemon_harnesses.version
detect:
  version: [--version]

# 每條消息起一次進程。{prompt} {resume} {args} 是佔位符，見下文
# prompt 不走參數，走 stdin(見 input)。不讓它彈權限確認;提問走 can_use_tool(見 question.md)
run:
  new:    [-p, --input-format, stream-json, --output-format, stream-json, --verbose,
           --permission-mode, bypassPermissions, --permission-prompt-tool, stdio, "{args}"]
  resume: [-p, --input-format, stream-json, --output-format, stream-json, --verbose,
           --permission-mode, bypassPermissions, --permission-prompt-tool, stdio,
           --resume, "{resume}", "{args}"]

# stdin 寫一行 JSON 作爲這條消息;看到 result 行後關掉 stdin,進程退出
input:
  format: stream-json
  message: { type: user, message: { role: user, content: "{prompt}" } }

# 起進程前把本目錄下的子目錄拷進會話目錄(每次覆蓋，保證是當前版本)
workdir:
  skills: .claude/skills

# stdout 每行一個 JSON,按規則從上往下匹配
output:
  format: jsonl
  session_id: session_id            # 任何一行帶這個字段就記下來
  rules:
    - match: { type: assistant }
      each: message.content         # 對數組裏每一項再匹配
      rules:
        - match: { type: text }
          progress: "{text}"
        - match: { type: tool_use }
          progress: "🔧 {name} {input}"
    - match: { type: result }
      result:
        ok: { subtype: success, is_error: false }
        output: result
        error: subtype
        cost_usd: total_cost_usd

# 收尾：沒出現 result 行就算失敗;有 result 但退出碼非 0 也算失敗
finish:
  require_result: true
```

### codex/harness.yaml

```yaml
name: codex
display_name: Codex
description: OpenAI Codex app-server，支持活動 turn 的實時插話
command: codex
transport: codex-app-server
instructions: AGENTS.md
detect:
  version: [--version]
```

Codex 使用 stdio JSON-RPC app-server，而非 exec JSONL。每次 run 經 initialize → thread/start → turn/start 創建新的原生會話，收到用戶補充後調用 turn/steer，要求 expectedTurnId 與當前 turn 相同。只有成功響應的 turnId 匹配，纔回報 steered；拒收或執行已經結束時保留未送達投遞，交給後續 run。

取消先發送 turn/interrupt，最多等待兩秒後結束 app-server。進度從 item 事件獲取，計劃從 turn/plan/updated 獲取；只有 turn/completed 的 completed 狀態視爲成功。

默認 workspace-write、網絡開啓、approval_policy=never。daemon.json 的 Codex args 支持 -m/--model、-s/--sandbox、-a/--ask-for-approval、-p/--profile、-c/--config、--enable/--disable、--search、--full-auto、--yolo，轉換成 app-server 參數和配置；未知參數明確報錯。command 自定義路徑及 AGENTUSE_* 環境變量照常生效。不支持的交互 RPC 返回錯誤，不自動批准。

協議依據：[OpenAI Codex app-server](https://learn.chatgpt.com/docs/app-server)。本機 Codex CLI 0.165.0 已通過真實運行驗證。

## 字段

| 字段 | 說明 |
|---|---|
| `name` | 唯一名字，等於 [harnesses](../tables/harnesses.md).name,必須和目錄名一致 |
| `display_name` / `description` | 服務啓動時寫進 harnesses 表,`kind = builtin` |
| `command` | 默認可執行文件;daemon.json 裏配了 `command` 就用配的 |
| `transport` | 缺省使用 JSONL 執行器；codex-app-server 使用原生 thread/turn RPC |
| `detect.version` | 探測版本用的參數，不寫就不探測 |
| `run.new` | 第一條消息的參數(不含 command 本身) |
| `run.resume` | 之後每條的參數，派活消息裏有 `resume` 時用 |
| `input` | 不寫:stdin 爲空,prompt 只能用 `{prompt}` 放進參數(codex)。`format: stream-json`:把 `message` 裏的佔位符換掉，序列化成一行寫進 stdin,stdin 保持打開，讀到 `result` 行後關閉 |
| `env` | 起進程時追加的環境變量，值裏的 `{message_id}` / `{session_id}` 換成這條消息、這個會話的 id |
| `workdir` | `internal/harness/<name>/` 下的子目錄 → 會話目錄裏的相對路徑，起進程前整個拷過去(覆蓋同名文件);不寫就什麼也不拷 |
| `output.format` | 目前只有 `jsonl`:stdout 每行一個 JSON,不是 JSON 的行當文本進度 |
| `output.session_id` | 全局規則：哪個字段是 harness 的會話 id |
| `output.rules` | 逐行匹配的規則列表，見下 |
| `finish` | 進程退出後怎麼判成敗 |

### 佔位符

| 佔位符 | 換成 |
|---|---|
| `{prompt}` | 消息內容裏的 `prompt`(爲空直接報錯，不起進程);在 `input.message` 裏是一個 JSON 字符串值，不用轉義 |
| `{resume}` | 上次回報的 harness 會話 id |
| `{args}` | daemon.json 裏這個 harness 的 `args`,**展開成多個參數**;沒配就去掉 |

在 `run` 裏佔位符必須獨佔一個參數，不做字符串拼接，免得 prompt 裏的內容被當成參數解析。

### 規則

每行 JSON 從上往下找**所有**命中的規則(不是第一條就停),一條規則裏:

| 鍵 | 作用 |
|---|---|
| `match` | 字段路徑 → 期望值，全部相等才命中;路徑用 `.` 取嵌套字段 |
| `each` | 命中後取這個數組，對每一項用子 `rules` 再匹配 |
| `progress` | 回報一條進度 `{"text": …}`;`{路徑}` 取字段，對象 / 數組按 JSON 原文，超長截斷 |
| `session_id` | 記下 harness 會話 id |
| `output` | 記下結果輸出，後出現的覆蓋前面的 |
| `error` | 記下錯誤信息 |
| `ok` | `true` 表示看到了成功標記 |
| `plan` / `plan_add` / `plan_update` | 讀出 harness 列的計劃，見 [plan](plan.md) |
| `final` | `true` 時命中這條後，同一層後面的規則不再匹配 |
| `result` | 一次給全：`ok`(寫成 match 那樣的條件)、`output`、`error`、`cost_usd`,都是字段路徑 |

只做相等匹配和取字段，不引入表達式語言。兩個現有 harness 都夠用;真遇到寫不出來的 harness,再給它在 Go 裏留一個具名解析器(`output.format: <名字>`),而不是把 yaml 變複雜。

### finish

| 鍵 | 含義 |
|---|---|
| `require_result: true` | 必須見過 `result`,否則按退出碼 + stderr 報失敗(claude) |
| `require_ok: true` | 必須見過 `ok: true` 且退出碼 0 才成功(codex) |
| 都不寫 | 退出碼 0 就成功 |

harness 沒報會話 id 時沿用續接的那個，這條對所有 harness 都一樣，不用寫。

## 誰讀它

- **服務**:啓動時把每個 `internal/harness/*/harness.yaml` 的 `name` / `display_name` / `description` upsert 進 harnesses 表(`kind = builtin`),代替 `server/internal/store/seed.go` 裏寫死的列表。目錄刪了的內置 harness 不自動刪行，避免 agent、會話引用斷掉。
- **daemon**:探測和運行都按 yaml 來,Claude 使用通用 JSONL 執行器，Codex 使用 app-server 適配器。daemon.json 的 `harnesses.<name>` 仍然可以改 `command`、加 `args`、`disabled`,寫法不變。
- **文檔**:[connect](connect.md#本機怎麼跑-harness) 的命令表改成指向 `internal/harness/<name>/harness.yaml`,不再手抄。

整個 `internal/harness/` 用 `go:embed` 編進 `agentuse` 和 `agentuse-server`,不需要用戶機器上有這個目錄;改裏面的東西要發新版本才生效，和改代碼一樣。

## 自定義 harness

不變：用戶在服務上註冊名字，在 daemon.json 裏配命令，按 stdin `{"session_id","resume","input"}` / stdout `{"type":"progress"|"result",…}` 的約定跑([harnesses](../tables/harnesses.md))。`internal/harness/` 只放內置的。

## 工作目錄

工作目錄由 daemon 鎖定，用戶不能指定：每個會話固定在 `~/.agentuse/work/<session_id>/`,第一條消息時建，之後每條都在這裏(Claude Code 按目錄找會話，換目錄就續不上)。會話、API、daemon.json 都不再有 `cwd` / `workdir`。

每次起進程前 daemon 按 `workdir` 把東西拷進去。claude 的會話目錄長這樣：

```
~/.agentuse/work/ss_xxx/
└── .claude/skills/
    └── agentuse/SKILL.md     // 來自 harness/claude/skills/agentuse/
```

`agentuse` 這個 skill 告訴 Claude 它跑在什麼環境裏：沒有人在終端前，不能中途提問;每條消息的最後一段回覆就是交給用戶的結果，要寫完整;這個目錄是這個會話專用的，可以隨便放文件，下一條消息還在。以後要按 agent 帶不同的 skill,再在這一步加。會話在[工作](work.md)裏時，這一步還會把工作的說明寫成 `CLAUDE.md`(codex 是 `AGENTS.md`),文件名由 harness.yaml 的 `instructions` 一項指定。

codex 暫時不拷東西，只是也在鎖定的目錄裏跑。

## 以後

- **插話**:已單獨設計，見 [steer](steer.md)。harness.yaml 用 `steer: claude-hooks` 打開。
- **提問**:已單獨設計，見 [question](question.md)。harness.yaml 用 `ask: claude-permission` 打開，claude 的參數裏 `--disallowedTools AskUserQuestion` 換成 `--permission-prompt-tool stdio`。
- **hook / plugin**:放在 `internal/harness/<name>/` 下對應的子目錄裏，跑時帶上;回調式 hook 由 daemon 在 stdin 註冊，需要落盤的再通過 `workdir` 拷進去。
- **用戶自己的目錄**:允許 daemon 讀 `~/.agentuse/harness/<name>/harness.yaml`,讓手搓的 harness 不用改成 agentuse 的 stdin/stdout 約定。先不做。

## 實現步驟

已完成：`internal/harness/` 目錄和加載代碼、服務用 yaml 寫 harnesses 表、daemon 通用執行器;stream-json 輸入和鎖定工作目錄也已完成：

1. `harness.Spec` 加 `input`、`workdir`,`embed.go` 把 `skills/` 等子目錄也編進去;加 `internal/harness/claude/skills/agentuse/SKILL.md`。
2. claude 的 `run` 改成上面的樣子;`runSpec` 按 `input` 寫 stdin,讀到 `result` 後關 stdin。
3. daemon 固定會話目錄、起進程前按 `workdir` 拷文件;去掉 daemon.json 的 `workdir`、派活消息的 `cwd`。
4. 服務去掉 `sessions.cwd`、`POST /api/sessions` 的 `cwd`,SDK 和前端新會話裏的工作目錄輸入框一起去掉(不做兼容，刪庫重建)。
5. 測試：假 claude 讀 stdin 一行再輸出，檢查 prompt 到達、stdin 被關、skill 拷進了會話目錄。
