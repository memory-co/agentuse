# codex harnessctl 适配器

本目录是完整独立分发包，入口仅加载本目录 lib/。依赖 Linux、Python 3.9+ 标准库以及已安装并配置认证的原生 codex，不依赖父目录、其他适配器或 AgentUse。

```sh
A=/path/to/codex/harnessctl
printf '%s' "$prompt" | "$A" send /tmp/task.jsonl --cwd /path/to/workspace
"$A" send /tmp/task.jsonl '补充要求'
"$A" status /tmp/task.jsonl
"$A" reply /tmp/task.jsonl request-id allow
"$A" cancel /tmp/task.jsonl
```

公开命令只有 send、cancel、status、reply。send/reply 省略正文参数时从 UTF-8 stdin 读取。事件文件路径是唯一会话身份；首次发送自动启动，空闲回收后自动恢复原生上下文。无需管理 start、投递策略、session ID 或进程生命周期。cancel 停止当前工作和待处理输入，保留对话历史。

协议为 harnessctl/2，见 [完整协议规范](https://github.com/memory-co/agentuse/blob/main/harnessctl/README.md)。问题回复使用统一 question id 到文本/数组的 JSON 对象，适配器转换原生格式。能力和依赖见 manifest.json。运行状态保存在事件文件旁的 .d/，分发包按只读使用。

JSONL 完整事件契约见 [EVENT.md](EVENT.md)，机器格式见 [event.schema.json](event.schema.json)。写入前由本包校验，不依赖外部模块。

公共事件仅 accepted、message、request、result、log 五种。前四种可靠落盘；日志可丢，不承担正文、审批或终态。run_id 由本适配器分配 UUID，确认的插话共享当前 run，使用方不能指定。

默认执行权限为自动放行：thread/start 和 thread/resume 均显式设置 approvalPolicy=never、sandbox=danger-full-access，不继承本机较保守的默认值。普通工具执行不进入 reply 链路；用户问题仍以 request(kind=question) 等待 reply。若原生仍发出显式权限请求，作为例外保留 permission 请求，不擅自吞掉或伪造批准。组织策略拒绝、认证错误和执行失败不会通过自动 reply 绕过。
