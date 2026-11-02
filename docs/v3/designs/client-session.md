# Client sessionService：固定事件地址与工作目录

一个 client session 对应一个固定的 events.jsonl 地址。原生 Codex thread 或 Claude session 的身份及恢复状态封装在 harnessctl 内，client 不保存也不选择原生 ID。

## 关系

```text
endpoint / conversation
  workspace/                       所有本机 session 共用的 cwd
  sessions/<client_session_uuid>/
    session.yaml
    events.jsonl                   传给 harnessctl 的唯一会话地址
    events.jsonl.d/                适配器拥有
    runs/<adapter_run_uuid>.jsonl  一个实际交互的引用
```

一个 conversation 可以有多个 Codex、Claude Code 或自定义 harness session，并可以同时在同一 workspace 中工作。工作文件共享，原生上下文各自独立，不自动获得并发文件事务。

一个 session 可以包含多个实际 run；run UUID 必须来自 accepted。同一次交互中的追加输入可能沿用该 UUID。Session 与进程不是同一实体：适配器空闲退出后仍可根据同一事件地址恢复上下文。

## 元数据

```yaml
id: 7877d33b-788f-4f6c-9ff0-987c2128064e
endpoint_id: local
conversation_id: 550e8400-e29b-41d4-a716-446655440000
agent_id: local-codex
harness: codex
harness_revision: package-revision
status: idle
created_at: 1793443200000
last_used_at: 1793443205100
updated_at: 1793443205100
```

id 只是目录定位用的 client UUID，不传给原生 CLI 充当其 session ID。路径来自启动配置和 endpoint/conversation，不冗余保存 work_root、目录映射、原生 ID 或 server 历史游标。时间为 Unix 毫秒。

## 选择、占用与控制

默认按 endpoint/conversation/agent/harness/revision 选择最近的有效空闲 session。显式向指定 session send 时使用该地址；不能因其忙碌就换成另一个 session 或自行决定新 run ID。

同一个受管 session 由一个驱动处理 send/reply/cancel，追加输入交给适配器裁定插话或排队。全部输入有结果并确认前台工作停止后，session 才恢复 idle。活动阶段为 running，问题是否等待回答从公共事件或 status 获取，不维护重复的问题账本。

包版本不同或历史不可恢复时，不静默更换既有 session 的适配器和事件地址。新会话创建新目录；旧引用仍可读取，显式恢复不可用版本时返回错误。原生全局历史由原生工具管理，不擅自删除 ~/.codex 或 ~/.claude。

cancel 取消 session 当前和排队工作，保留原生上下文；reply 使用公开 request_id。Native status 返回 state、当前 run_id、queued 和公开 requests。Client 不读取 .d/ 的 PID、锁或 native.json 来替代这些命令。

## 生命周期和回收

工作根目录是 setting.yaml 的 session.work_root，默认 ~/.agentuse/tmp。目录固定为 <root>/<endpoint_id>/<conversation_id>/，实际 cwd 是其 workspace/，session 元数据在同级 sessions/。修改根目录须停止并重启，需要保留历史时完整迁移目录。

启动时清理遗留前台工作并重建 run 引用；不自动重放旧输入。Session 生命周期状态不承担 server 派发或确认状态。旧版 server 的上下文游标和待回报数据隔离在 .server/。

声明 quota 后自动检查受管目录总量，按整个 conversation 最近使用时间从旧到新清理空闲目录。运行、等待回答、清理未确认、预留启动和旧协议待交付均保护目录。没有可回收空间时暂停新启动，不能杀任务来满足容量目标。

回收删除该 conversation 的 workspace、session、引用和输入资源，不删除 server 文件或原生工具的全局历史。旧路径重新出现不代表旧上下文可恢复；必须用新的 session 记录。目录标记只校验受管归属，不是另一张 workspace 映射表。
