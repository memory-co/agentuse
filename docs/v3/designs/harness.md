# harnessService：将不同 Harness 收敛为标准执行协议

> v3 目标设计，尚未据此修改代码或数据库。YAML 的管理和解析在 server；client 只实现标准执行协议，不内置具体 harness 的识别逻辑。关联字段不声明数据库外键，由 service 校验。

## 职责与边界

harnessService 负责理解不同 harness 的探测、启动、输入输出、插话、提问和取消方式，并将其转换为统一的执行描述及交互动作。

| Server 的 harnessService | 设备上的通用 client |
|---|---|
| 管理、解析、校验 harness YAML | 不读取 YAML，不内置 harness 名称列表 |
| 将配置编译为标准执行描述 | 按描述准备目录、环境和进程 |
| 生成探测命令并解释探测结果 | 执行探测，返回 exit code、stdout、stderr |
| 将业务动作映射到具体输入或协议请求 | 读写进程流、传递协议帧、承接本地回调 |
| 将具体输出解释成统一进度、结果、问题和用量 | 回传原始输出、帧、回调以及通用执行状态 |
| 确定能力和业务动作是否被执行端接受 | 不因本地写入成功就声称业务输入已被消费 |

目标是 **server 理解 harness，client 提供通用执行能力**。Client 处理 command、参数、通道和协议帧，不依据 `codex / claude` 等名称选择分支；名称和配置版本是 server 的配置管理信息。

## 标准执行描述

YAML 是 server 的配置来源，不是下发给 client 的运行时协议。harnessService 将其编译为版本化 JSON 执行描述，包含：

- 要启动的程序、argv、环境变量、工作目录和准备文件。
- 通道类型：进程 stdin/stdout/stderr、JSONL、JSON-RPC、本地 HTTP/hook 回调。
- 通用的建连、帧编码、请求/响应关联、数据转发和清理规则。
- 所需执行协议版本及通用执行能力。

Client 使用通用执行器解释这些描述。持续连接、JSON-RPC 请求关联和 hook 回调接入也是标准原语，不留给某个 harness 的专用适配器。需要新的交互方式时，先定义可复用的协议原语；不能以新增 harness 为由在 client 增加名称判断。

业务输出规则、工具消息含义和动作映射由 server 掌握。标准执行描述只表达可执行动作，不把任意 YAML 或 harness 专有代码打包丢给 client。

## 统一业务交互

下面是标准动作的语义，不是要求所有 harness 都提供全部能力。

| 动作 | 统一结果 | Server 如何适配 |
|---|---|---|
| probe | capabilities / unavailable | 下发探测描述，解释原始结果，登记可用性和版本 |
| start | started → progress / result / error | 下发执行描述及任务输入，把原始输出归一化 |
| steer | accepted / rejected | 转换补充输入；收到 harness 消费确认后才 accepted |
| question → answer | question → answered / expired | 将具体工具提问转成标准问题，将用户答案转回具体协议 |
| cancel | cancelled / error | 请求终止或结束协议会话，确认执行结束 |

交互消息通过 `run_id + attempt_id` 定位当前执行，插话再关联 run_prompt 的 ID，答案关联当前问题。旧 attempt 的消息不能影响新执行。

**通道已接收、请求已发送、业务已消费是三个状态。** 比如 stdin 写入成功或 RPC 发出，只表示传输成功；收到对应消费确认才能把 steer 标为 accepted。没有可确认插话能力的 harness 不声明该能力，由 runService 安排后续执行。

## Client 需要的通用能力

Client 支持启动/结束进程、持续读写流、JSONL 帧、JSON-RPC 双向请求响应、本地 HTTP/hook 回调及转发、文件准备和清理。Client 上报的是这些协议能力及版本，不是写死的 harness 支持列表。

本地 callback 可由执行描述生成配置，并携带当前 run/attempt 的关联信息；client 接到后转交 server，等待标准响应再回给本机程序。持续进程和本地连接对象保存在 client，具体工具状态由 server 的动作映射处理。

这使 Claude 风格 hook 和 Codex 风格持续 RPC 都能落到同一套原语上。统一协议不要求每个 harness 具有相同功能；可用动作由 YAML 声明能力与 client 支持的协议能力共同决定。

## 探测与执行流程

1. Client 连接设备入口，上报执行协议版本、通用能力和 ready 状态。
2. harnessService 为需要探测的配置生成标准 probe 描述；deviceService 交给对应 client。
3. Client 原样执行并回传探测结果；harnessService 解释结果，deviceService 更新 device_harnesses。Client 不自行遍历内置 harness YAML。
4. Agent 选择 device 和 harness；runService 建立任务时固定配置版本和编译后的执行描述。
5. Client 领取 run，收到标准执行描述、任务输入、上下文与文件引用，不接收 YAML；按通用原语执行并持续回传输出和交互。
6. harnessService 将原始交互转换为标准业务事件，runService 推进 run 状态。插话、答案和取消按相反方向转换成执行动作，再交 client 完成。

Client 不支持描述所要求的版本或原语时明确拒绝；配置版本改变后，按新版本重新探测，不能沿用旧版本的可用结论。排队任务和执行重试使用建立 run 时固定的执行描述。

## 配置与表结构

YAML 及其内容摘要只由 server 保存和解析。共享部分保留标准执行描述、命令、事件和协议版本的数据结构，不再放内置 harness YAML 或专用适配器。

`harnesses` 保存配置定义；`runs.execution_spec` 保存编译后的 JSON 描述。harness_revision 固定原配置版本，使 server 仍可按该 run 的配置解释交互；执行期间不使用最新同名配置替换旧规则。harnesses 当前版本更新时，旧 YAML 必须随对应 run 一并留存，保存在 runs.harness_config_yaml，仅供 server 解释，不下发 client。

```sql
CREATE TABLE harnesses (
    name TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT,
    owner_id TEXT,
    config_yaml TEXT NOT NULL,
    config_revision TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
```

name 是 server 侧唯一配置标识，kind 为 builtin/custom，owner_id 是自定义配置归属。config_yaml 是完整正文，config_revision 是其内容摘要。

## 相关设计

[Server 总览](server.md) · [deviceService](device.md) · [agentService](agent.md) · [runService](run.md)
