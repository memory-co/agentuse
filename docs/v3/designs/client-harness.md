# Client harnessService：harnessctl 命令与事件桥接

harnessService 只接收准备好的 UTF-8 文本、工作目录、适配器和固定事件地址。它不接收 caller run/attempt 身份，不生成执行 UUID，不根据 token 选择协调者或其他业务角色。

## 适配器包与启动快照

```text
~/.agentuse/harness/<name>/<revision>/
  manifest.json
  harnessctl
  EVENT.md
  event.schema.json
  lib/...
```

内置包从 client 二进制释放；自定义 <name>/local/ 优先，local/disabled 可禁用。启动时校验协议、入口、平台及声明的依赖，固定可用性和内容版本；local 包复制到内容版本目录，防止运行中编辑改变随后执行的代码。修改包或依赖配置后重启 client，不提供重新探测或热切换。

AgentUse 来源注册名沿用 codex、claude，独立包分别为 codex、claudecode。每个独立包可单独分发，不依赖 AgentUse Go 模块。当前内置包需要 Linux、Python 3.9+ 和已配置的原生 CLI。

## 五个命令（harnessctl/3）

```text
harnessctl init <events.jsonl> [--cwd DIR] [--model MODEL]
                [--agents-md FILE] [--skills-dir DIR ...]
harnessctl send <events.jsonl>
harnessctl reply <events.jsonl> <request_id>
harnessctl cancel <events.jsonl>
harnessctl status <events.jsonl>
```

init 绑定配置并持久化资源快照，不启动原生进程或模型。驱动先调用 init --cwd，再通过 stdin 向 send 提交正文；reply 的答案也走 stdin，不通过 shell 拼接。首次 send 自动建立原生上下文；后续 send 自动复用。client 不调用 start、resume、steer 或 queue。

Client 的 session API 使用 send/reply/cancel，status API 返回公开状态。Server 的 prompt/reply/cancel 只传业务输入与控制，不选择原生投递策略。

外部指令和技能由适配器的 init 统一接收：--agents-md 指令快照、可重复的 --skills-dir 技能根目录保存在 events.jsonl.d/resources/，不写入共享 workspace。原始资源删除后仍可恢复相同快照；改配置使用新的事件地址。完整约定见 [harnessctl 协议](../../../harnessctl/README.md)。当前 client 自动调用 init --cwd；CLI 资源配置可直接在独立适配器使用，本次未增加 client UI 的资源配置入口。

## 五种公共事件

| 类型 | 含义 |
|---|---|
| accepted | 完整输入、input_id 和适配器确认的 run_id；据此登记执行 |
| message | 以 message_id 标识的完整正文快照 |
| request | question/permission 的 pending/replied/expired 完整状态 |
| result | 一个 run 的终态、input_ids、正文和计量 |
| log | 可丢的进度与诊断，不参与关键业务判断 |

前四种可靠保存。所有字段和状态约束以仓库 harnessctl/EVENT.md、event.schema.json 为准。Client 只解析这些公共记录，不解释原生 Codex RPC、Claude control_request、hook 或 .d/ 私有文件。

run_id、input_id、request_id 都属于适配器。Codex 确认的补充输入可以进入同一 run；完成竞态可能转成新 run。Claude 忙时通过 priority=next 向原生提交，按消费回执将补充输入归入同一 run；在上一轮结束后才消费时产生新 UUID。send 等待消费边界期间，client 继续读取事件并处理 reply/cancel。client 只观察结果，不预言或要求某一种归属。

## 驱动与失败处理

驱动在内存中串行处理 session 的输入、回复和取消，读取完整 JSONL 行；末尾半行留待下次读取。每个 accepted 都交给相同的 run 索引机制。驱动窗口结束可以包含多个 run，它不是额外的执行身份，也没有自己的 UUID。

回复使用 request_id。权限答案是 allow/deny；问题答案是 question id 到文本或文本数组的 JSON 对象。无效 reply 返回错误，不能因此悄悄终止仍在等待回答的交互。

取消后使用公开 status 确认没有前台工作，确认失败则保留目录占用。启动恢复也使用公开命令，不通过私有 PID 或原生 ID 猜测状态。后台运行进程的启动、空闲退出和恢复完全由适配器决定。

## 权限与输入来源

内置 Codex 创建和恢复时使用 approvalPolicy=never、sandbox=danger-full-access；Claude 使用 bypassPermissions。普通操作不经过额外确认，真实问题及原生例外权限请求才经 reply。认证、组织策略或执行失败不靠伪造批准绕过。

本地输入按用户给出的文本或 JSON 文本发送，不额外注入协调者 prompt。远端业务指令由来源边界准备，server 的协调者规则由 server 下发。来源凭据不作为角色判断依据。

每次输入来源的环境可能不同，适配器只存环境摘要；空闲时可内部重建进程并恢复上下文，让新凭据生效，忙时拒绝更换环境。这是每次调用的执行上下文，不是配置热加载。

启动恢复时，runService 根据 sessionService 的元数据提供需要恢复的 harness、revision 和事件地址清单，harnessService 只通过对应适配器确认取消。harnessService 不查询 session.yaml、不遍历业务目录，也不依赖 sessionService；包安装使用 fileutil 的通用路径与文件工具。
