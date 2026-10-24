# harness 描述文件

## 一句话

仓库顶层加一个 `harness/` 目录，**一个 harness 一个子目录**,里面的 `harness.yaml` 写清楚这个 CLI 的命令入口点位：怎么探测、第一条消息怎么起、之后怎么续接、输出怎么读出进度 / 结果 / 会话 id。daemon 和服务都从这里读，加一个新 harness 只要加一个目录，不用再写 Go。以后这个 harness 专属的 hook、plugin、skills 也放进它自己的目录。

```
harness/
├── embed.go            // go:embed,和 frontend/embed.go 一样编进二进制
├── claude/             // Claude Code
│   ├── harness.yaml    // 命令入口点位(必须有)
│   ├── hooks/          // 以后：Claude Code 的 hook,比如插话
│   ├── plugins/        // 以后：随 agentuse 带的 plugin
│   └── skills/         // 以后：随 agentuse 带的 skills
└── codex/
    └── harness.yaml
```

目录名就是 harness 的 `name`。目前只有 `harness.yaml` 是必须的，其余子目录按需加，这份设计只定 `harness.yaml`。

## 为什么

现在 harness 的知识散在代码里：

- `internal/store/harnesses.go`:内置 harness 的名字、显示名、说明(写进 [harnesses](../tables/harnesses.md) 表)。
- `internal/daemon/harnesses.go`:默认命令、`--version` 探测、拼参数(`-p` / `--resume` / `exec … resume`)、`runClaude` / `runCodex` 两套解析。
- [connect](connect.md#本机怎么跑-harness) 里再抄一遍命令表。

三处要一起改，加 Gemini CLI、opencode 之类就得写一套新的 Go 解析。收进 `harness/<name>/` 以后，这个目录就是这个 harness 的全部事实。

## harness.yaml 长什么样

### claude/harness.yaml

```yaml
name: claude
display_name: Claude Code
description: Anthropic Claude Code CLI(claude -p)

# 本机可执行文件，daemon.json 的 harnesses.claude.command 可以覆盖
command: claude

# 探测：能在 PATH 里找到 command 就算可用;version 的输出第一行记成 daemon_harnesses.version
detect:
  version: [--version]

# 每条消息起一次进程。{prompt} {resume} {args} 是占位符，见下文
run:
  new:    [-p, "{prompt}", --output-format, stream-json, --verbose, "{args}"]
  resume: [-p, "{prompt}", --output-format, stream-json, --verbose, --resume, "{resume}", "{args}"]

# stdout 每行一个 JSON,按规则从上往下匹配
output:
  format: jsonl
  session_id: session_id            # 任何一行带这个字段就记下来
  rules:
    - match: { type: assistant }
      each: message.content         # 对数组里每一项再匹配
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

# 收尾：没出现 result 行就算失败;有 result 但退出码非 0 也算失败
finish:
  require_result: true
```

### codex/harness.yaml

```yaml
name: codex
display_name: Codex
description: OpenAI Codex CLI(codex exec)

command: codex

detect:
  version: [--version]

run:
  new:    [exec, --json, --skip-git-repo-check, "{args}", "{prompt}"]
  resume: [exec, --json, --skip-git-repo-check, "{args}", resume, "{resume}", "{prompt}"]

output:
  format: jsonl
  rules:
    - match: { type: thread.started }
      session_id: thread_id
    - match: { type: item.completed, item.type: agent_message }
      progress: "{item.text}"
      output: item.text             # 最后一条 agent_message 作为结果输出
    - match: { type: item.completed, item.type: command_execution }
      progress: "🔧 {item.command}"
    - match: { type: turn.completed }
      ok: true
    - match: { type: turn.failed }
      error: error.message
    - match: { type: error }
      progress: "{message}"
      error: message

# 收尾：要 turn.completed 且退出码 0 才算成功
finish:
  require_ok: true
```

## 字段

| 字段 | 说明 |
|---|---|
| `name` | 唯一名字，等于 [harnesses](../tables/harnesses.md).name,必须和目录名一致 |
| `display_name` / `description` | 服务启动时写进 harnesses 表,`kind = builtin` |
| `command` | 默认可执行文件;daemon.json 里配了 `command` 就用配的 |
| `detect.version` | 探测版本用的参数，不写就不探测 |
| `run.new` | 第一条消息的参数(不含 command 本身) |
| `run.resume` | 之后每条的参数，派活消息里有 `resume` 时用 |
| `output.format` | 目前只有 `jsonl`:stdout 每行一个 JSON,不是 JSON 的行当文本进度 |
| `output.session_id` | 全局规则：哪个字段是 harness 的会话 id |
| `output.rules` | 逐行匹配的规则列表，见下 |
| `finish` | 进程退出后怎么判成败 |

### 占位符

| 占位符 | 换成 |
|---|---|
| `{prompt}` | 消息内容里的 `prompt`(为空直接报错，不起进程) |
| `{resume}` | 上次回报的 harness 会话 id |
| `{args}` | daemon.json 里这个 harness 的 `args`,**展开成多个参数**;没配就去掉 |

占位符必须独占一个参数，不做字符串拼接，免得 prompt 里的内容被当成参数解析。

### 规则

每行 JSON 从上往下找**所有**命中的规则(不是第一条就停),一条规则里:

| 键 | 作用 |
|---|---|
| `match` | 字段路径 → 期望值，全部相等才命中;路径用 `.` 取嵌套字段 |
| `each` | 命中后取这个数组，对每一项用子 `rules` 再匹配 |
| `progress` | 回报一条进度 `{"text": …}`;`{路径}` 取字段，对象 / 数组按 JSON 原文，超长截断 |
| `session_id` | 记下 harness 会话 id |
| `output` | 记下结果输出，后出现的覆盖前面的 |
| `error` | 记下错误信息 |
| `ok` | `true` 表示看到了成功标记 |
| `result` | 一次给全：`ok`(写成 match 那样的条件)、`output`、`error`、`cost_usd`,都是字段路径 |

只做相等匹配和取字段，不引入表达式语言。两个现有 harness 都够用;真遇到写不出来的 harness,再给它在 Go 里留一个具名解析器(`output.format: <名字>`),而不是把 yaml 变复杂。

### finish

| 键 | 含义 |
|---|---|
| `require_result: true` | 必须见过 `result`,否则按退出码 + stderr 报失败(claude) |
| `require_ok: true` | 必须见过 `ok: true` 且退出码 0 才成功(codex) |
| 都不写 | 退出码 0 就成功 |

harness 没报会话 id 时沿用续接的那个，这条对所有 harness 都一样，不用写。

## 谁读它

- **服务**:启动时把每个 `harness/*/harness.yaml` 的 `name` / `display_name` / `description` upsert 进 harnesses 表(`kind = builtin`),代替 `internal/store/harnesses.go` 里写死的列表。目录删了的内置 harness 不自动删行，避免 agent、会话引用断掉。
- **daemon**:探测和运行都按 yaml 来,`runClaude` / `runCodex` 合成一个通用的 jsonl 执行器。daemon.json 的 `harnesses.<name>` 仍然可以改 `command`、加 `args`、`disabled`,写法不变。
- **文档**:[connect](connect.md#本机怎么跑-harness) 的命令表改成指向 `harness/<name>/harness.yaml`,不再手抄。

整个 `harness/` 用 `go:embed` 编进 `agentuse` 和 `agentuse-server`,不需要用户机器上有这个目录;改里面的东西要发新版本才生效，和改代码一样。

## 自定义 harness

不变：用户在服务上注册名字，在 daemon.json 里配命令，按 stdin `{"session_id","resume","input"}` / stdout `{"type":"progress"|"result",…}` 的约定跑([harnesses](../tables/harnesses.md))。`harness/` 只放内置的。

## 以后

- **hook / plugin / skills**:放在 `harness/<name>/` 下对应的子目录里，daemon 跑这个 harness 时带上(比如插话要靠 Claude Code 的 hook)。怎么带上(写进会话目录、还是通过命令行参数指过去)由 `harness.yaml` 里再加一节来描述，到时候再定。
- **用户自己的目录**:允许 daemon 读 `~/.agentuse/harness/<name>/harness.yaml`,让手搓的 harness 不用改成 agentuse 的 stdin/stdout 约定。先不做。

## 实现步骤(定稿后)

1. 加 `harness/` 目录、`claude/harness.yaml`、`codex/harness.yaml`、`embed.go` 和加载 / 校验代码(带测试：两个 yaml 能解析、`name` 和目录名一致，规则对现有 stream-json / codex JSONL 样例跑出和现在一样的结果)。
2. 服务用 yaml 写 harnesses 表。
3. daemon 换成通用执行器，删掉 `runClaude` / `runCodex`。
4. 更新 connect.md 的命令表。
