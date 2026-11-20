# Antigravity CLI（agy）適配器

獨立分發包，使用 harnessctl/3。需要 Linux、Python 3.9+ 和已登入的 Google Antigravity CLI。本機驗證版本為 agy 1.5.13。適配器以 [ACP v1 stdio](https://antigravity.google/docs/cli/acp/) 呼叫 `agy acp`。

```sh
A=/path/to/agy/harnessctl
"$A" init /tmp/agy-events.jsonl --cwd /path/to/workspace
printf '%s' 'Your prompt' | "$A" send /tmp/agy-events.jsonl
"$A" status /tmp/agy-events.jsonl
"$A" cancel /tmp/agy-events.jsonl
"$A" reply /tmp/agy-events.jsonl REQUEST_ID allow
```

- `init` 可綁定 `--model`、`--agents-md` 和可重複指定的 `--skills-dir`。資源以快照保存於事件檔旁。適配器把指令與技能索引附加到 agy 的 session system prompt，模型按需讀取索引指向的 `SKILL.md`。
- 適配器保存原生會話 ID，回收後透過 `session/resume` 恢復。執行中收到的輸入會依序排到後續 run。
- 工具執行遵循 agy 的本機權限設定。若 ACP 發出確認請求，`reply` 支援單次 `allow` 或 `deny`。目前未接入 agy 的問答表單。
- 事件以 [EVENT.md](EVENT.md) 和 [event.schema.json](event.schema.json) 為準。文字分段以 log 回報，最終正文與結果寫為 message 和 result。

AgentUse Client 內建此適配器，登記名稱為 `agy`。獨立使用時只須複製本目錄。
