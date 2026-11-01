# claudecode harnessctl 适配器

本目录就是完整的独立分发包，可以整体复制到任意路径运行。仅依赖 Linux、Python 3.9+ 标准库及已安装并配置认证的原生 `claude`，不依赖父目录、其他适配器或 AgentUse 模块。

```sh
/path/to/claudecode/harnessctl doctor
/path/to/claudecode/harnessctl start /tmp/task.jsonl --cwd /path/to/workspace "检查代码"
/path/to/claudecode/harnessctl send /tmp/task.jsonl "重点检查错误处理"
/path/to/claudecode/harnessctl status /tmp/task.jsonl
/path/to/claudecode/harnessctl close /tmp/task.jsonl
```

协议为 harnessctl/0，见 [完整协议规范](https://github.com/memory-co/agentuse/blob/main/harnessctl/README.md)。适配器能力和依赖见本目录 manifest.json。入口只加载本目录 lib/ 中的代码；运行状态位于调用方指定的事件文件及其 .d/ 目录，安装包按只读使用。
