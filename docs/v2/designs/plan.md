# 计划(plan step)

## 一句话

harness 回一条消息时，把它自己列的步骤清单(哪些做完了、正在做哪一步)作为一种单独的回报 `plan` 送上来，会话页把最新的一份显示成勾选列表：

```
✓ 读文章和当前实现
✱ 改 harness.yaml
○ 跑测试并开 PR
```

不需要让 Claude Code 跑什么额外模式：它遇到多步任务本来就会调用自带的待办工具，我们只是把这些调用读出来。

## Claude Code 怎么列计划

两种工具，看用户机器上的版本，两种都认：

| 工具 | 怎么用 | 我们怎么读 |
|---|---|---|
| `TodoWrite`(老) | 每次调用都带**整份**清单:`input.todos = [{content, status, activeForm}]` | 直接换成这一份 |
| `TaskCreate` / `TaskUpdate`(新) | 一次建一步:`TaskCreate {subject, …}`;改状态:`TaskUpdate {taskId, status}` | daemon 在这次运行里攒着：建一步就追加，id 按建的顺序从 `1` 编;`TaskUpdate` 按 id 改状态,`deleted` 就删掉这一步 |

`status` 只有 `pending` / `in_progress` / `completed` 三种，原样用。新工具的 id 按建的顺序编，已经对真实 Claude Code 验证过(见下)。

2026-10-27 用 Claude Code 2.1.311 实测：

- 这个版本已经没有 `TodoWrite`;`-p` 模式默认也没有 `TaskCreate` / `TaskUpdate`,要设 `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` 才有。
- 任务清单默认跟着 Claude 会话走，续接时编号接着上次的(第二条消息从 `#4` 开始)。所以 daemon 给每次运行设 `CLAUDE_CODE_TASK_LIST_ID=agentuse-<message_id>`,编号从 `1` 开始，和"按建的顺序编"对得上。
- 这两个环境变量写在 claude 的 harness.yaml 的 `env` 里。

一份清单只属于这一次运行(回一条 `user` 消息)。下一条消息 Claude 会重新列，不跨消息合并。

## harness.yaml

规则里加两个键：

| 键 | 作用 |
|---|---|
| `plan` | 一次给整份清单:`steps` 是数组路径,`text` / `status` 是数组里每一项的字段 |
| `plan_add` / `plan_update` | 增量：`plan_add: {id, text}` 追加一步(`id` 不写就按顺序编);`plan_update: {id, status}` 改一步 |
| `final: true` | 命中这条后，同一层后面的规则不再匹配(免得计划工具再出一行"🔧 TodoWrite …"进度) |

claude 的规则变成：

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

codex 现在没有对应的事件，不写;以后 codex 有了再加规则。自定义 harness 直接输出 `{"type":"plan","steps":[…]}` 行就行，daemon 原样转成 `plan` 回报。

为了让 Claude 更常列计划,[agentuse skill](../../../internal/harness/claude/skills/agentuse/SKILL.md) 里加一句：步骤多于两三步的任务，先用待办工具列出计划，边做边更新。

## 回报

daemon 每次清单变了就发一条 `plan` 回报，带**整份**清单(服务和前端不用自己拼):

```json
{ "type": "event", "message_id": 3, "kind": "plan", "payload": { "steps": [
  { "text": "读文章和当前实现", "status": "completed" },
  { "text": "改 harness.yaml", "status": "in_progress" },
  { "text": "跑测试并开 PR", "status": "pending" }
] } }
```

- 服务当普通回报处理：写进 [run_events](../tables/session_runs.md)(`kind = plan`)、续租。不加表、不加字段：一条消息的最新计划就是它最后一条 `plan` 回报。
- `GET /api/sessions/{id}` 的 `events` 里照常返回。

## 前端

会话页每条 `user` 消息下面，如果有 `plan` 回报，取最后一份显示成勾选列表(✓ 完成、转圈 进行中、○ 未开始),放在"进度 N 条"折叠框上面。会话 `running` 时跟着轮询刷新;跑完以后留着，回看时能看到最后停在哪一步。

## 实现步骤

已完成。

1. `harness.Spec` 规则加 `plan` / `plan_add` / `plan_update` / `final`;`harness.State` 攒当前清单，清单变了回调一次(带测试:TodoWrite 整份、TaskCreate + TaskUpdate 增量、`final` 不再出进度)。
2. daemon 把回调发成 `plan` 回报;自定义 harness 的 `{"type":"plan"}` 行也转成 `plan`。
3. 服务接受 `plan` 这个 kind(和 `progress` 一样处理)。
4. 前端显示勾选列表;skill 加一句"先列计划"。
5. e2e:fakeharness 的 claude 模式输出 TodoWrite / TaskCreate / TaskUpdate,检查会话里的 `plan` 回报。
