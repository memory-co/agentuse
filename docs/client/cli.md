# Client CLI：agentuse

`agentuse` 在执行设备上运行 Client，连接 Server 并调用本机 harness。它也提供远端会话、工作线程和附件的命令。

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

在页面中选择“本地调试”、创建 conversation、选择可用 harness 并执行。默认监听 `127.0.0.1:7801`。本节后面的 `session`、`work`、`attachment` 命令访问远端 Server；本地执行通过本机页面或 Client 的 `/api/docs` 操作。

## 命令一览

| 命令 | 作用 |
|---|---|
| `agentuse register` | 用设备安装令牌注册 Server |
| `agentuse start` | 启动 Client，连接已启用的 Server，同时支持本地调试 |
| `agentuse daemon` | 自动安装的后台服务入口，需要已有平台注册信息 |
| `agentuse ui` | 输出本机页面登录链接，`--open` 同时打开浏览器 |
| `agentuse status` | 检查所选 Server 的设备凭据，显示本机服务和页面信息 |
| `agentuse harnesses` | 探测本机 harness，支持 `--json` |
| `agentuse session …` | 发送消息、读取会话、查看 run、等待或取消执行 |
| `agentuse work …` | 协调者管理工作线程 |
| `agentuse attachment …` | 上传、列出和下载远端会话附件 |
| `agentuse help` | 查看命令一览 |
| `agentuse version` | 显示版本 |

参数支持 `--flag value`、`-flag value` 和 `--flag=value`。除 `session`、`attachment` 特别支持参数交错外，参数放在位置参数之前。`--config` 按各命令说明使用；`work` 命令从工作环境变量读取地址和凭据。

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

## 远端命令的地址、会话与凭据

`session`、`attachment` 共用以下选择规则：

| 值 | 来源，按优先级排列 |
|---|---|
| Server 地址 | `--server` → `AGENTUSE_SERVER` → 注册配置中选中的来源 |
| 请求令牌 | `--token` → `AGENTUSE_TOKEN` → `AGENTUSE_WORK` → 所选来源的设备密钥 |
| 注册配置文件 | `--config` → `AGENTUSE_CONFIG` → `~/.agentuse/endpoints.yaml` |
| conversation ID | `--session` → `AGENTUSE_SESSION`；`session` 命令的显式位置参数优先 |

当地址或令牌仍需从配置补全时，会读取注册配置；显式提供地址时选择对应的注册来源。如果同时提供 Server 地址和用户或工作令牌，则无需本机注册即可调用相应的远端命令。

| 凭据 | 可用于哪些操作 |
|---|---|
| 用户令牌 `ut_…` | 按用户权限读取、发送、取消及操作附件 |
| 工作令牌 `wt_…` | 在令牌所属工作内读取、发送、取消及操作附件 |
| 设备密钥 `dk_…` | 读取已投递给本设备的会话及其附件；发送消息和取消 run 需要用户或工作令牌 |

手动操作远端会话的常见配置：

```bash
export AGENTUSE_SERVER="https://agentuse.example.com"
export AGENTUSE_TOKEN="ut_替换为用户令牌"
```

## session：远端会话

这里的 `SESSION_ID` 是 Server 的 conversation ID，即 `/api/sessions/{id}` 中的 ID。它与 Client 本机页面中的原生 harness session ID 含义不同。

### 发送消息

```bash
agentuse session send SESSION_ID [参数] "消息"
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--agents` | 空 | 逗号分隔的 Agent ID，对应 `to_agent_ids` |
| `--users` | 空 | 逗号分隔的用户 ID，对应 `to_user_ids` |
| `--session` | `AGENTUSE_SESSION` | 省略位置参数时使用的 conversation ID |
| `--server`、`--token`、`--config` | 见上节 | 远端地址和凭据 |

```bash
agentuse session send SESSION_ID --agents coder,reviewer "检查实现与风险"
agentuse session send SESSION_ID --users alice "请查看结果"
agentuse session send SESSION_ID --agents coder --users alice "请修复并记录结果"
agentuse session send SESSION_ID "保存一条备注"
```

只有 `--agents` 中的收件人触发执行。用户收件人必须已经拥有会话读取权限；两组都为空时保存备注。正文里的 `@名称` 不会自动指定收件人。

成功时输出包含 `message`、`runs`、`deliveries` 的 JSON。实际 run ID 在 Client 上报 accepted 后确定；后续消息属于当前 run 还是新 run，也由适配器确认。每次成功发送都会创建新消息。

### 读取、列 run 和等待

```bash
agentuse session read SESSION_ID [--json]
agentuse session runs SESSION_ID [--json]
agentuse session wait SESSION_ID [--timeout 10m]
```

`read` 默认按顺序显示共享消息。`runs` 默认每行显示 run ID、Agent ID 和状态。`read/runs --json` 以及 `wait` 输出包含 `session`、`messages`、`runs` 的 JSON 对象。

`wait` 等待会话进入 `idle` 或 `resolved`，默认超时 30 分钟。超时只结束等待，执行可以继续。命令成功结束等待后，仍需查看 `runs[].status` 判断执行成功、失败或取消；会话空闲本身不表示全部 run 成功。

设置会话后，可以省略位置参数：

```bash
export AGENTUSE_SESSION="替换为conversation的UUID"
agentuse session read
agentuse session send --agents coder "补充一个测试"
agentuse session wait --timeout 5m
```

### 取消执行

```bash
agentuse session cancel SESSION_ID RUN_ID
```

向指定 run 发送取消请求并输出 Server 当前记录的 run JSON。最终状态由 Client 的 result 事件确认，可以继续用 `runs`、`wait` 或页面查看。

适配器实际执行的是对应本地 session 的取消操作，作用于该 session 当前及排队工作。需要回答执行中的问题时，使用 Server 页面或对应 API。

`session` 各子命令都接受前述远端公共参数。`session` 和 `attachment` 允许参数出现在位置参数前后；使用 `--` 可终止参数解析，例如发送以 `-` 开头的正文。

## attachment：远端会话附件

```bash
agentuse attachment upload FILE --session SESSION_ID [参数]
agentuse attachment list --session SESSION_ID [参数]
agentuse attachment download ATTACHMENT_ID --session SESSION_ID --output FILE [参数]
```

| 参数 | 默认值或来源 | 说明 |
|---|---|---|
| `--session` | `AGENTUSE_SESSION` | conversation ID，必填 |
| `--server`、`--token`、`--config` | 见远端命令公共规则 | Server 地址和凭据 |
| `--name` | 上传文件的 basename | 上传时覆盖显示文件名 |
| `--output` | 无 | 下载目标文件，下载时必填 |
| `--force` | `false` | 下载时允许覆盖已存在的目标文件 |
| `--run` | `AGENTUSE_RUN` | 使用设备密钥时，可记录附件来源的 run ID |
| `--json` | `false` | 输出 JSON |

```bash
agentuse attachment upload ./input.csv --session SESSION_ID --name data.csv --json
agentuse attachment list --session SESSION_ID
agentuse attachment download ATTACHMENT_ID --session SESSION_ID --output ./result.csv
```

上传只接受普通文件，成功后默认输出附件 ID、文件名、大小和下载路径。上传连接中断时，Server 可能已经保存文件，先用 `list` 核对再决定是否重新上传。

`list` 自动读取全部分页，`--json` 输出附件数组。下载会检查大小和 SHA-256，默认不覆盖已有文件；`--json` 输出附件元数据和保存路径。

使用设备密钥时，Server 校验该会话是否曾向本设备投递消息。`--run` 或 `AGENTUSE_RUN` 可标记附件来源的 run，不会替代设备或用户凭据。

## work：协调者的工作线程

工作协调者运行时，Client 自动提供 `AGENTUSE_SERVER` 和 `AGENTUSE_WORK`。`work` 命令从这两个环境变量读取地址和工作令牌，权限限定在对应工作；不使用 `--config` 或 Server 管理员登录文件。

| 命令 | 作用 |
|---|---|
| `agentuse work agents` | 列出可使用的 Agent |
| `agentuse work threads` | 列出本工作线程 |
| `agentuse work new [参数] "消息"` | 创建线程并发送首条消息 |
| `agentuse work new --defer-start` | 创建空线程，供上传附件后发送消息 |
| `agentuse work send [参数] SESSION_ID "消息"` | 向线程发送消息 |
| `agentuse work read SESSION_ID` | 查看线程消息 |
| `agentuse work resolve SESSION_ID` | 将空闲线程标记为已完成 |
| `agentuse work help` | 查看工作命令说明 |

`new` 和 `send` 的参数放在消息或会话 ID 之前：

| 参数 | 适用命令 | 说明 |
|---|---|---|
| `--agents` | `new`、`send` | 逗号分隔的 Agent 收件人 |
| `--users` | `new`、`send` | 逗号分隔的用户收件人，须已有读取权限 |
| `--defer-start` | `new` | 只建空线程；不能同时提供消息或收件人 |

```bash
agentuse work agents
agentuse work new --agents coder "实现这个功能"
agentuse work send --agents reviewer --users alice SESSION_ID "请评审当前结果"
agentuse work read SESSION_ID
agentuse work resolve SESSION_ID
```

`new` 输出创建的线程 ID。指定 Agent 后，线程执行完会把结果汇总给协调者；只指定用户或未指定收件人时，只保存消息。`send` 输出消息发送结果 JSON；`agents`、`threads`、`read` 默认输出可读文本。

先建空线程、上传文件再执行：

```bash
agentuse work new --defer-start
# 将上一步输出的线程 ID 替换到以下命令。
agentuse attachment upload ./input.csv --session SESSION_ID --token "$AGENTUSE_WORK"
agentuse work send --agents coder SESSION_ID "分析附件中的数据"
```

## 执行环境变量

Client 为远端 Agent 的命令准备相应上下文，便于在执行中调用上述 CLI：

| 环境变量 | 用途 |
|---|---|
| `AGENTUSE_SERVER` | 当前 Server 地址 |
| `AGENTUSE_CONFIG` | 当前 Client 注册配置文件 |
| `AGENTUSE_ENDPOINT` | 当前来源标识；本地调试为 `local` |
| `AGENTUSE_SESSION` | Server conversation ID |
| `AGENTUSE_AGENT` | 当前 Agent ID |
| `AGENTUSE_RUN` | 适配器可设置的实际 run ID，用于附件来源标记 |
| `AGENTUSE_MESSAGE_ID` | 本次来源消息 ID |
| `AGENTUSE_WORK` | 仅协调者执行中提供的工作令牌 |

实际 run ID 由适配器产生，`AGENTUSE_RUN` 是否已设置取决于适配器的执行上下文。协调者向新建线程上传附件时，上例显式使用工作令牌，可访问尚未分配给设备的线程。

用户手动运行远端命令时，可用 `AGENTUSE_TOKEN` 提供用户或工作令牌。`AGENTUSE_CONFIG` 的默认值读取适用于 `session` 和 `attachment`；其他接受配置文件的命令使用显式 `--config`。

## 帮助、输出与退出码

```bash
agentuse help
agentuse start --help
agentuse register --help
agentuse session help
agentuse work help
agentuse version
```

程序级 `help` 也可写为 `-h`、`--help`；`version` 也可写为 `-v`、`--version`。`--json` 按各命令说明使用。

| 退出码 | 含义 |
|---|---|
| `0` | 命令成功或显示帮助 |
| `1` | 连接、鉴权、运行或存储错误 |
| `2` | 命令或参数用法错误 |

命令结果写入 stdout，错误和运行日志写入 stderr。`status` 展示检查结果，自动化判断具体连接情况时应读取相应检查信息。
