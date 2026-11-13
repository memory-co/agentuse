# codex harnessctl 适配器

> 当前实现为 harnessctl/3（包版本 0.4.1）。必须先 init；send 不再接受配置参数，旧版命令协议不兼容。

本目录是完整独立分发包，入口仅加载本目录 lib/。依赖 Linux、Python 3.9+ 标准库以及已安装并配置认证的原生 codex，不依赖父目录、其他适配器或 AgentUse。

```sh
A=/path/to/codex/harnessctl
"$A" init /tmp/task.jsonl --cwd /path/to/workspace \
  --agents-md /configs/AGENTS.md \
  --skills-dir /skills/common --skills-dir /skills/team
printf '%s' "$prompt" | "$A" send /tmp/task.jsonl
"$A" send /tmp/task.jsonl '补充要求'
"$A" status /tmp/task.jsonl
"$A" reply /tmp/task.jsonl request-id allow
"$A" cancel /tmp/task.jsonl
```

公开命令为 init、send、cancel、status、reply。init 绑定 cwd/model 和可选资源，保存快照，不启动原生执行进程、不调用模型、不产生 run；send 只接收事件地址和正文，不接受配置参数，未 init 返回 4。send/reply 省略正文参数时从 UTF-8 stdin 读取。事件文件路径是唯一会话身份；首次发送自动启动，空闲回收后自动恢复原生上下文。无需管理 start、投递策略、session ID 或进程生命周期。cancel 停止当前工作和待处理输入，保留对话历史。

命令协议为 harnessctl/3，见 [完整协议规范](https://github.com/memory-co/agentuse/blob/main/harnessctl/README.md)。问题回复使用统一 question id 到文本/数组的 JSON 对象，适配器转换原生格式。能力和依赖见 manifest.json。运行状态保存在事件文件旁的 .d/，分发包按只读使用。

init 的 --agents-md 保存为私有快照，创建和恢复原生 thread 时以 developerInstructions 注入。--skills-dir 可重复，技能快照在创建 thread 前通过 skills/extraRoots/set 注册，并用 skills/list 确认发现。整个资源目录放在事件地址旁的 .d/resources/，不在 cwd 创建指令文件或技能目录，不改用户全局配置。原生进程重建时重新应用同一快照。

JSONL 完整事件契约见 [EVENT.md](EVENT.md)，机器格式见 [event.schema.json](event.schema.json)。写入前由本包校验，不依赖外部模块。

公共事件仅 accepted、message、request、result、log 五种。前四种可靠落盘；日志可丢，不承担正文、审批或终态。run_id 由本适配器分配 UUID，确认的插话共享当前 run，使用方不能指定。

每个 run 的 result 包含 model（string 或 null），记录原生确认的主执行模型；无法确认或排队后未执行时为 null。status.model 仍表示 init 绑定的配置选择，含义与 result.model 不同。

默认执行权限为自动放行：thread/start 和 thread/resume 均显式设置 approvalPolicy=never、sandbox=danger-full-access，不继承本机较保守的默认值。普通工具执行不进入 reply 链路；用户问题仍以 request(kind=question) 等待 reply。若原生仍发出显式权限请求，作为例外保留 permission 请求，不擅自吞掉或伪造批准。组织策略拒绝、认证错误和执行失败不会通过自动 reply 绕过。
