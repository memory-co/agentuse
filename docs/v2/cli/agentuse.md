# agentuse

普通用户在自己电脑上用的 CLI。日常不用手动敲：页面上的安装命令(`setup.sh`)会替你下载、注册并装成后台服务。下面这些命令是给排查问题和手动接入用的。

本机配置在 `~/.agentuse/daemon.json`(所有命令都可以用 `-config` 换路径),格式见 [apis/daemon](../apis/daemon.md#本机配置)。

## 命令

| 命令 | 说明 |
|---|---|
| `agentuse register -server URL -token st_…` | 注册这台电脑 |
| `agentuse daemon` | 前台运行 daemon |
| `agentuse status` | 看本机的注册和连接状态 |
| `agentuse harnesses` | 看本机检测到哪些 harness |
| `agentuse work …` | 工作的协调者用来开线程、看线程(harness 在跑时用，不是给人敲的) |
| `agentuse attachment …` | 上传、列出、下载 session 附件 |
| `agentuse version` | 版本 |

### register

```bash
agentuse register -server https://agentuse.example.com -token st_…
```

用页面上生成的一次性安装令牌换长期密钥，写进配置文件(保留已有的 `harnesses` 配置)。`-token` 不给时读环境变量 `TOKEN`。已经注册过的再注册一次，会覆盖成新的 daemon。

调用 [`POST /api/daemon/register`](../apis/daemon.md#post-apidaemonregister)。

### daemon

```bash
agentuse daemon
```

前台运行：连上服务、上报本机 harness、领活、执行、汇报;断线自动重连。Ctrl-C / SIGTERM 退出。

`setup.sh` 装的后台服务(launchd / systemd --user / nohup)跑的就是这条命令。日志打到 stderr。

### status

```
$ agentuse status
服务     https://agentuse.example.com
daemon   dm_5c1c8602536e55cb
配置     ~/.agentuse/daemon.json
服务连通 正常(密钥有效)
后台服务 systemd --user: active
```

只读：读配置、调 [`GET /api/daemon/me`](../apis/daemon.md#get-apidaemonme) 验证密钥(不建 WebSocket,不影响正在跑的 daemon)、看后台服务是否在跑。没注册时提示去页面复制安装命令。

### harnesses

```
$ agentuse harnesses
名字        命令                       版本
claude      /opt/node/bin/claude       2.1.310 (Claude Code)
codex       -                          未安装
my-harness  /usr/local/bin/my-harness
```

按 daemon 的检测逻辑列出本机 harness,和 daemon 连上时上报的一致。是否开启在页面上设置，这里不管。

## 不做的

- 不在这里开关 harness、开会话、管用户：这些在网页 / [HTTP API](../apis/README.md) 上做。
- 卸载先手动：停掉后台服务、删 `~/.agentuse/`。以后可以加 `agentuse uninstall`。

### work

```
agentuse work agents                                       # 能用哪些 agent
agentuse work threads                                      # 这个工作的线程和状态
agentuse work new --agents code-helper "改 daemon 的重连逻辑"  # 开线程，打印线程 id
agentuse work new --agents code-helper --defer-start         # 先建空线程，上传附件后再 send
agentuse work send --agents code-helper ss_xxx "再补个测试"                       # 给线程发消息(在跑就是插话)
agentuse work read ss_xxx                                  # 看线程的消息
agentuse work resolve ss_xxx                               # 标记线程已完成
```

给[工作](../designs/work.md)的协调者用：读环境变量 `AGENTUSE_SERVER`、`AGENTUSE_WORK`(工作令牌，daemon 起协调者时设好;也认旧名字 `AGENTUSE_WORK_TOKEN`)调 [works](../apis/works.md#工作令牌wt_) 允许的那几个接口。没有这两个变量时报错退出。输出是给 harness 读的纯文本。

### attachment

给人和 harness 使用的会话文件命令，设计见 [attachment](../designs/attachment.md)，调用 [attachments API](../apis/attachments.md)。

```bash
agentuse attachment upload ./report.pdf
agentuse attachment upload ./report-v2.pdf --name report.pdf --json
agentuse attachment list --session ss_xxx --json
agentuse attachment download at_xxx --output ./report.pdf
```

- 所有子命令支持 `--session`、`--server`、`--token`、`--config`、`--json`。参数可以放在位置参数之前或之后。session 默认读 `AGENTUSE_SESSION`；server 按显式参数 → `AGENTUSE_SERVER` → daemon 配置选取；config 按显式参数 → `AGENTUSE_CONFIG` → `~/.agentuse/daemon.json` 选取。
- 认证按显式 `--token` → `AGENTUSE_TOKEN` → `AGENTUSE_WORK`→ daemon 配置中的 `key` 选取。前面提供了但无效就报错，不降级换身份。显式用户/工作令牌和 server 已足够时，不要求本机注册。
- harness 默认用当前 session。使用 daemon 密钥上传时，从 `AGENTUSE_MESSAGE_ID` 取活动消息 id，缺少时直接报错；仅对 dk_ 请求发送这个字段。用用户令牌可以显式指定有权访问的其他 session；工作令牌只能指定本工作会话。不会从当前目录名称猜 session。
- `upload` 接受一个普通文件路径，`--name` 可覆盖文件名；拒绝目录，流式上传，成功打印 id、文件名、大小、download_path；`--json` 输出 attachment 对象。不自动重试 POST，超时提示先检查 list，避免重复附件。
- `list` 自动取完分页，普通模式打印表格，`--json` 输出完整 attachment 数组。
- `download` 的 `--output` 必填，写临时文件、核对元数据大小和 SHA-256 后原子 rename。目标已存在就报错；显式 `--force` 才能覆盖。失败清理临时文件；`--json` 输出 `{"attachment": …, "output": "本地路径"}`，不混入文件字节。
- HTTP、文件读写、校验失败都非零退出，错误写 stderr。第一版 CLI 不提供删除命令，删除走页面或 API。

示例：本机用户上传输入文件，凭据也可以用环境变量提供：

```bash
agentuse attachment upload ./requirements.pdf --session ss_xxx --server https://agentuse.example.com --token ut_…
```

### session

```bash
agentuse session send ss_xxx --agents coder,reviewer "看看实现和风险"
agentuse session read ss_xxx --json
agentuse session runs ss_xxx
agentuse session wait ss_xxx --timeout 10m
agentuse session cancel ss_xxx sr_xxx
```

默认从 AGENTUSE_SESSION 取当前会话，显式 session 参数覆盖；支持 --server、--token、--config、--json。地址/凭据来源与 attachment 一致。send 的 --agents 为空时只记笔记；忙碌目标自动作为插话。read 返回统一的 session 消息，不按 agent 区分上下文。

harness 内部的 read/attachment 请求自动带 AGENTUSE_RUN、AGENTUSE_ATTEMPT，读配置里的 dk_，只在当前执行有效时可用；协调者优先用工作令牌。所有新执行都给出 AGENTUSE_AGENT、AGENTUSE_SESSION、AGENTUSE_MESSAGE_ID、AGENTUSE_RUN、AGENTUSE_ATTEMPT。session 管理和 send 仍需要用户或工作令牌。

work new/send 支持 --agents coder,reviewer（放在位置参数前）。协调者开的线程为 multi_agent。空线程上传文件后，用 work send --agents 指明第一轮目标。
