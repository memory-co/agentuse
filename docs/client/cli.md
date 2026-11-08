# Client CLI：agentuse

`agentuse` 在执行设备上运行 Client，连接 Server 并调用本机 harness。它还提供远端 conversation 管理和 run 内直接可用的 `context` 工具。

## 快速开始

### 接入 Server

先在执行设备上安装并登录需要使用的 Codex 或 Claude Code；内置适配器还需要 Python 3.9+。Server 管理页面中新建设备后，复制生成的安装命令执行，它会完成 Client 安装、设备注册和后台服务配置。

已经安装二进制时，可以手动注册并在前台启动：

```bash
agentuse register --server https://agentuse.example.com --token 替换为设备安装令牌
agentuse start
```

另开终端检查配置和凭据、查看可用 harness、打开本机页面：

```bash
agentuse status
agentuse harnesses
agentuse ui --open
```

回到 Server 管理页面，在设备上启用需要的 harness，再创建 Agent、发送消息。注册命令只保存设备身份；已运行的 Client 需要重启后读取新配置。

### 本地调试

未注册 Server 时也可以启动本机页面：

```bash
agentuse start
```

另开终端：

```bash
agentuse ui --open
```

在页面中选择“本地调试”、创建 conversation、选择可用 harness 并执行。默认监听 `127.0.0.1:7801`。本节后面的 `conversation` 命令访问远端 Server；本地执行通过本机页面或 Client 的 `/api/docs` 操作。

## 命令一览

| 命令 | 作用 |
|---|---|
| `agentuse register` | 用设备安装令牌注册 Server |
| `agentuse start` | 启动 Client，连接已启用的 Server，同时支持本地调试 |
| `agentuse daemon` | 自动安装的后台服务入口，需要已有平台注册信息 |
| `agentuse ui` | 输出本机页面登录链接，`--open` 同时打开浏览器 |
| `agentuse status` | 检查所选 Server 的设备凭据，显示本机服务和页面信息 |
| `agentuse harnesses` | 探测本机 harness，支持 `--json` |
| `agentuse context …` | 读取本机某个 run 的输入、读写附件，或进入其 shell |
| `agentuse conversation …` | 发送消息、读取会话、查看 run、等待或取消执行 |
| `agentuse help` | 查看命令一览 |
| `agentuse version` | 显示版本 |

参数支持 `--flag value`、`-flag value` 和 `--flag=value`。除 `context` 的选择参数、`conversation` 支持参数交错外，参数放在位置参数之前。`--config` 按各命令说明使用。

## 配置与运行目录

默认路径如下：

| 路径 | 用途 |
|---|---|
| `~/.agentuse/endpoints.yaml` | 一个或多个 Server 的注册信息 |
| `~/.agentuse/setting.yaml` | 本机页面和执行目录设置 |
| `~/.agentuse/harness/` | harnessctl 适配器包 |
| `~/.agentuse/tmp/` | conversation、原生 session、执行记录和 workspace |
| `~/.agentuse/ui.addr` | 运行中的本机页面地址 |
| `~/.agentuse/ui.token` | 本机页面登录令牌 |

`register`、`start`、`daemon`、`status` 和 `harnesses` 用 `--config FILE` 指定注册配置文件，默认 `~/.agentuse/endpoints.yaml`。`setting.yaml` 和 `harness/` 从该配置文件所在目录读取。

例如：

```bash
agentuse register --config /opt/agentuse/endpoints.yaml \
  --server https://agentuse.example.com --token 替换为设备安装令牌
agentuse start --config /opt/agentuse/endpoints.yaml
agentuse harnesses --config /opt/agentuse/endpoints.yaml
```

此时本机设置文件为 `/opt/agentuse/setting.yaml`，适配器目录为 `/opt/agentuse/harness/`。执行目录由设置中的 `session.work_root` 决定；`ui.addr` 和 `ui.token` 仍位于当前用户的 `~/.agentuse/`。

### 本机设置

首次启动会在需要时创建 `setting.yaml`。可按下面的结构修改：

```yaml
local_http:
  enabled: true
  host: 127.0.0.1
  port: 7801
session:
  work_root: ~/.agentuse/tmp
  quota:
    max_bytes: 21474836480
```

`quota` 为可选项，上例是 20 GiB；省略时不限容量。配置配额后会清理可回收的空闲记录；运行中或仍有待确认数据的记录受保护。`work_root` 必须是可用的独立绝对目录，支持 `~/` 开头。

通过 `local_http.enabled: false` 关闭本机页面。`AGENTUSE_UI_ADDR` 环境变量可覆盖监听地址，例如：

```bash
AGENTUSE_UI_ADDR=127.0.0.1:7901 agentuse start
```

显式设置 `AGENTUSE_UI_ADDR=''` 可关闭页面。配置、注册来源和适配器变化在重启 Client 后生效。使用同一执行根目录的两个 Client 不能同时运行。

### 多个 Server

对不同地址重复执行注册：

```bash
agentuse register --server https://server-a.example.com --token 替换为A的安装令牌
agentuse register --server https://server-b.example.com --token 替换为B的安装令牌
agentuse start
```

`start` 连接配置中所有 `enabled: true` 的来源。可以在 `endpoints.yaml` 中修改显示名称和 `enabled`，再重启 Client。注册写入的字段为 `name`、`server_url`、`enabled`、`device_id`、`device_key`；本地调试入口自动存在。

`status` 默认检查第一个启用的注册来源；设置 `AGENTUSE_SERVER` 可选择另一个已注册的来源：

```bash
AGENTUSE_SERVER=https://server-b.example.com agentuse status
```

`start` 仍按配置连接全部已启用来源。

## register

```bash
agentuse register --server URL --token SETUP_TOKEN [--config FILE]
```

| 参数 | 默认值或来源 | 说明 |
|---|---|---|
| `--server` | 无，必填 | Server 地址 |
| `--token` | 环境变量 `TOKEN`，必填 | 在 Server 页面生成的设备安装令牌 |
| `--config` | `~/.agentuse/endpoints.yaml` | 写入平台注册信息的文件 |

安装令牌用于一次注册，成功后保存设备 ID 和设备密钥。为新 Server 注册会追加来源；对同一地址注册会更新其注册信息。随后重启 Client。

## start 和 daemon

```bash
agentuse start [--config FILE]
agentuse daemon [--config FILE]
```

两者都在前台持续运行，通过 `Ctrl+C` 或 `SIGTERM` 停止。`start` 允许尚未注册时进行本地调试；`daemon` 是安装脚本使用的后台服务入口，需要已有平台注册信息。

自动安装时通常已经由 systemd、launchd 或后台进程运行 `daemon`，可先用 `agentuse status` 查看。修改配置后重启对应进程。

## ui

```bash
agentuse ui
agentuse ui --open
```

输出运行中的本机页面登录链接。`--open` 在 Linux 调用 `xdg-open`，在 macOS 调用 `open` 打开浏览器。请先启动 Client，并启用本机页面。

命令读取当前用户的 `~/.agentuse/ui.addr` 和 `ui.token`。它也接受 `--config FILE`，但该参数不会切换这两个文件的位置；使用自定义配置启动的 Client 同样写入这两个文件。

登录本机页面后，访问该地址下的 `/api/docs` 可查看和调试 Client API；`/api/openapi.json`、`/api/openapi.yaml` 提供 OpenAPI。

## status

```bash
agentuse status [--config FILE]
```

显示所选 Server、设备 ID、配置路径、设备密钥检查结果、安装的后台服务状态和本机页面地址。

设备密钥检查通过 HTTP 完成，用于确认注册凭据是否有效。查看实际连接和执行状态，可进入本机页面。

## harnesses

```bash
agentuse harnesses [--config FILE] [--json]
```

默认显示 harness 名称、命令路径和版本；未安装或禁用时显示对应状态。`--json` 输出探测结果数组。

Client 使用启动时的适配器和可用性快照。安装、升级或启停 harness 后，重启 Client，再在 Server 页面确认设备上报并启用对应 harness。

## context：run 内的输入与文件

AI 在 Client 启动的执行环境中直接使用 `context`，不需要提供 Server、conversation、session 或 run ID：

```bash
context input
context attachment list
context attachment read FILE_ID
context attachment upload ./report.pdf
context --help
```

`input` 输出当前 run 已受理的输入及来源 message ID。`attachment list` 直接查詢整個 conversation 的 Server 附件；`read` 按需下載、校驗並保存到 workspace/<relative_path>，返回本地路徑和 MIME 類型；兩者不需要活躍 run。圖片再用原生工具讀取。`upload` 自动关联当前 conversation/run，返回附件 ID 和可在回复中引用的 `download_path`，Server 用户可直接查看或下载。所有结果为 JSON。

上传只接受普通文件，不自动发消息或结束执行。Server 在接收及落库时检查 run 的归属与活动状态，已结束 run 无法继续上传。连接中断时可能已经保存；先 `list` 核对，工具不自动重试上传。人类在 Server 页面或通过 API/SDK 上传输入文件，Client AI 通过 context 读取；输出由 AI 上传后在 Server 展示。

一个本机 session 可以有多个 run。默认通过公开的 `harnessctl status` 确认正在执行的 run，不选择“最后一条 accepted”；排队 run 不会替换当前输入。当前 run 后续受理的补充消息可以通过再次调用 `context input` 读取。

人类可以直接查看，或进入绑定某个 run 的 shell：

```bash
agentuse context --path /绝对路径/context.json --run RUN_ID input
agentuse context --path /绝对路径/context.json --run RUN_ID attachment list
agentuse context --path /绝对路径/context.json --run RUN_ID attachment read FILE_ID
agentuse context enter --path /绝对路径/context.json --run RUN_ID
# 进入后：
context input
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--path` | `AGENTUSE_CONTEXT` | 本机 session 下 Client 生成的 `context.json` |
| `--run` | 绑定环境中的 run，否则询问适配器当前 run | 该 session 中实际 accepted 的 run UUID；读取历史时显式指定 |

文件位于 `<session.work_root>/<endpoint_id>/<conversation_id>/sessions/<session_id>/context.json`。`enter` 将工作目录切换到该 conversation 的 workspace，并为 shell 固定所选 run；退出 shell 即退出上下文。记录尚未回收且文件完整时，停止 Client 后也可以显式读取历史 run。

裸 `context` 使用已经绑定的上下文，不接受 `--path`、`--run`、`--server`、`--token` 或管理命令。Work 编排和协调工具由 Server 提供，见 [Server Work 手册](../server/work.md)。

本地调试同样提供输入读取；远端输入还包含 Server message ID 和附件快照。完整的输出字段和图片读取示例见 [执行上下文手册](context.md)。旧 `agentuse session` 命令已移除；远端管理使用下面的 `agentuse conversation`。

## 远端命令的地址、会话与凭据

`agentuse conversation` 使用以下选择规则：

| 值 | 来源，按优先级排列 |
|---|---|
| Server 地址 | `--server` → `AGENTUSE_SERVER` → 注册配置中选中的来源 |
| 请求令牌 | `--token` → `AGENTUSE_TOKEN` → 所选来源的设备密钥 |
| 注册配置文件 | `--config` → `AGENTUSE_CONFIG` → `~/.agentuse/endpoints.yaml` |
| conversation ID | `--conversation` → `AGENTUSE_CONVERSATION`；`conversation` 命令的显式位置参数优先 |

当地址或令牌仍需从配置补全时，会读取注册配置；显式提供地址时选择对应的注册来源。如果同时提供 Server 地址和用户令牌，则无需本机注册即可调用相应的远端命令。

| 凭据 | 可用于哪些操作 |
|---|---|
| 用户令牌 `ut_…` | 按用户权限读取、发送、取消及操作附件 |
| 设备密钥 `dk_…` | 读取已投递给本设备的会话及其附件；发送消息和取消 run 需要用户令牌 |

手动操作远端会话的常见配置：

```bash
export AGENTUSE_SERVER="https://agentuse.example.com"
export AGENTUSE_TOKEN="ut_替换为用户令牌"
```

## conversation：远端会话

这里的 `CONVERSATION_ID` 是 Server 的 conversation ID，即 `/api/sessions/{id}` 中的 ID。它与 Client 本机页面中的原生 harness session ID 含义不同。

### 发送消息

```bash
agentuse conversation send CONVERSATION_ID [参数] "消息"
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--agents` | 空 | 逗号分隔的 Agent ID，对应 `to_agent_ids` |
| `--users` | 空 | 逗号分隔的用户 ID，对应 `to_user_ids` |
| `--conversation` | `AGENTUSE_CONVERSATION` | 省略位置参数时使用的 conversation ID |
| `--server`、`--token`、`--config` | 见上节 | 远端地址和凭据 |

```bash
agentuse conversation send CONVERSATION_ID --agents coder,reviewer "检查实现与风险"
agentuse conversation send CONVERSATION_ID --users alice "请查看结果"
agentuse conversation send CONVERSATION_ID --agents coder --users alice "请修复并记录结果"
agentuse conversation send CONVERSATION_ID "保存一条备注"
```

只有 `--agents` 中的收件人触发执行。用户收件人必须已经拥有会话读取权限；两组都为空时保存备注。正文里的 `@名称` 不会自动指定收件人。

成功时输出包含 `message`、`runs`、`deliveries` 的 JSON。实际 run ID 在 Client 上报 accepted 后确定；后续消息属于当前 run 还是新 run，也由适配器确认。每次成功发送都会创建新消息。

### 读取、列 run 和等待

```bash
agentuse conversation read CONVERSATION_ID [--json]
agentuse conversation runs CONVERSATION_ID [--json]
agentuse conversation wait CONVERSATION_ID [--timeout 10m]
```

`read` 默认按顺序显示共享消息。`runs` 默认每行显示 run ID、Agent ID 和状态。`read/runs --json` 以及 `wait` 输出包含 `session`、`messages`、`runs` 的 JSON 对象；HTTP API 的字段名仍为 `session`。

`wait` 等待会话进入 `idle` 或 `resolved`，默认超时 30 分钟。超时只结束等待，执行可以继续。命令成功结束等待后，仍需查看 `runs[].status` 判断执行成功、失败或取消；会话空闲本身不表示全部 run 成功。

设置会话后，可以省略位置参数：

```bash
export AGENTUSE_CONVERSATION="替换为conversation的UUID"
agentuse conversation read
agentuse conversation send --agents coder "补充一个测试"
agentuse conversation wait --timeout 5m
```

### 取消执行

```bash
agentuse conversation cancel CONVERSATION_ID RUN_ID
```

向指定 run 发送取消请求并输出 Server 当前记录的 run JSON。最终状态由 Client 的 result 事件确认，可以继续用 `runs`、`wait` 或页面查看。

适配器实际执行的是对应本地 session 的取消操作，作用于该 session 当前及排队工作。需要回答执行中的问题时，使用 Server 页面或对应 API。

`conversation` 各子命令都接受前述远端公共参数。`conversation` 允许参数出现在位置参数前后；使用 `--` 可终止参数解析，例如发送以 `-` 开头的正文。

## 执行环境变量

Client 为远端 Agent 的命令准备相应上下文，便于在执行中调用上述 CLI：

| 环境变量 | 用途 |
|---|---|
| `AGENTUSE_SERVER` | 当前 Server 地址 |
| `AGENTUSE_CONFIG` | 当前 Client 注册配置文件 |
| `AGENTUSE_ENDPOINT` | 当前来源标识；本地调试为 `local` |
| `AGENTUSE_CONVERSATION` | Server conversation ID；远端管理命令的默认目标 |
| `AGENTUSE_SESSION` | Client 本机 session ID，与 conversation ID 不同 |
| `AGENTUSE_CONTEXT` | 当前 session 的 `context.json` 路径 |
| `AGENTUSE_CONTEXT_RUN` | `agentuse context enter` 为 shell 固定的 run UUID |
| `AGENTUSE_AGENT` | 当前 Agent ID |
| `AGENTUSE_RUN` | 适配器可设置的实际 run ID，用于附件来源标记 |
| `AGENTUSE_MESSAGE_ID` | 原生进程启动时的来源消息；读取当前输入应使用 `context input` |

实际 run ID 由适配器产生，`AGENTUSE_RUN` 是否已设置取决于适配器的执行上下文。`context` 优先使用进入 shell 时固定的 `AGENTUSE_CONTEXT_RUN`，其次为适配器提供的 `AGENTUSE_RUN`，否则询问原生 status；不依赖启动时的消息环境变量判断当前输入。

用户手动运行远端命令时，可用 `AGENTUSE_TOKEN` 提供用户令牌。`AGENTUSE_CONFIG` 的默认值读取适用于 `conversation`；其他接受配置文件的命令使用显式 `--config`。

## 帮助、输出与退出码

```bash
agentuse help
agentuse start --help
agentuse register --help
agentuse context --help
agentuse conversation help
agentuse version
```

程序级 `help` 也可写为 `-h`、`--help`；`version` 也可写为 `-v`、`--version`。`--json` 按各命令说明使用。

| 退出码 | 含义 |
|---|---|
| `0` | 命令成功或显示帮助 |
| `1` | 连接、鉴权、运行或存储错误 |
| `2` | 命令或参数用法错误 |

命令结果写入 stdout，错误和运行日志写入 stderr。`status` 展示检查结果，自动化判断具体连接情况时应读取相应检查信息。
