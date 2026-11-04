# fileService：按命名空间存储文件

> 通用 fileService、本地/S3 后端及下列文件入口已实现。fileService 不使用数据库表，不绑定 conversation、agent 或 skill 的业务模型。旧 /api/sessions/.../attachments 接口和 attachments 表仍独立存在，尚未迁移到这里；不能将新 fileService 理解为已完成旧附件链路替换。

## 职责

fileService 统一封装本地文件系统与 S3 协议存储。调用方提供**命名空间、相对路径和文件流**；fileService 负责完整写入、读取、列举及删除，业务 service 负责对象归属、授权、版本和清理时机。

```text
Put(namespace, path, stream, false)  普通完整写入，可按业务要求覆盖
Put(namespace, path, stream, true)   不存在时创建，已有路径不覆盖
Get(namespace, path)                  返回文件流
List(namespace, prefix)               列举某一前缀
Delete(namespace, path)               删除一个对象
DeletePrefix(namespace, prefix)       清理一个业务空间
```

不新增全局 files/attachments 元数据表。存储后端是字节内容和存在性的事实来源。AgentUse 不用一个 conversation_id 参数硬套所有文件，更不把 skill 包伪装为某个 conversation 的附件。

## 命名空间与目录

| 命名空间 | 业务拥有者 | 相对路径 | 写入约束 |
|---|---|---|---|
| conversations | conversationService | `<conversation_id>/<file_path>` | 对话文件可按相同路径覆盖 |
| skills | skillService | `<skill_id>/<skill_version_id>/skill.zip` | 每个版本一个独立路径，发布后只读 |

本地后端：

```text
<root>/
  conversations/<conversation_id>/inputs/data.csv
  conversations/<conversation_id>/reports/result.pdf
  skills/<skill_id>/<skill_version_id>/skill.zip
```

S3 后端：相同 key 前加可选 prefix，存入配置的 bucket；例如 `agentuse/skills/<skill_id>/<skill_version_id>/skill.zip`。命名空间来自 server 业务代码，不由上传请求随意指定以越权访问其他空间。

本地 root、S3 endpoint/bucket/prefix/凭据属于部署配置。业务表只存命名空间内的相对路径，client 使用业务下载 API，不感知实际后端或持有存储凭据。

## 文件能力与业务规则

| FileService 负责 | 业务 Service 负责 |
|---|---|
| 流式存储、读取及后端差异 | 谁能上传、下载哪个业务对象 |
| 普通写入和不存在时创建 | 哪些路径可覆盖，哪些版本发布后不可变 |
| 原子可见的完整对象；失败不暴露半个文件 | Skill ZIP 校验、SHA-256、版本号和依赖关系 |
| 路径边界及文件系统链接限制 | Conversation 删除、skill 版本保留、无引用对象清理 |

路径不接受绝对路径、.. 或通过符号链接越出指定空间。本地写入先写临时文件并完成落盘后提交；S3 只有完整上传成功才发布对象。PutIfAbsent 必须保留“已存在则不覆盖”的语义，不能用不受保护的先查后写冒充。

fileService 不解析 SKILL.md，不解压技能包，不知道 agent 当前版本，也不决定一个旧文件是否可删除。压缩包摘要和大小存于 skill_versions，是 skillService 的版本校验信息，不是 fileService 新建的元数据账本。

## HTTP 入口由业务对象定义

```text
PUT /api/conversations/{conversation_id}/files/{path...}
GET /api/conversations/{conversation_id}/files/{path...}

POST /api/skills
POST /api/skills/{skill_id}/versions
GET /api/skills/{skill_id}/versions/{version_id}/package
```

Conversation 入口先经 conversationService 校验归属，再操作 conversations 空间；messages.attachments 的相对路径数组是 conversation 元数据目标设计，旧附件消息引用仍由现有附件链路处理。

Skill 上传经 skillService 校验、分配不可变版本路径，并调用 fileService。下载同样由 skillService 校验用户或已分配输入的设备权限，然后返回文件流；不开放任意覆盖已发布 skill 包的通用 PUT 接口。

## 发布、删除与异常清理

上传 Skill 时先写新对象，确认完整后再提交版本表。SQLite 与文件后端没有跨系统事务；数据库提交失败时删除本次候选对象，异常中断可能留下未引用文件。当前未启用自动孤立对象清理。

删除 conversation 只清理 conversations 下该 conversation 的前缀，不清理 skills。软删除 agent/skill 不删除已发布且仍被版本引用的包。Skill 清理必须由 skillService 检查全部历史版本和正在发布的对象后进行，不能只保留 latest。没有显式版本淘汰流程时，只清理确认未发布且无版本记录引用的孤立对象，不删除任何已发布 skill_version 对应的包。

文件删除与 SQLite 删除不能当成一件原子操作。业务 service 保留错误、重试或补偿清理的责任，fileService 提供可重复执行的删除原语，不维护另一套业务数据库。

## 表结构

无。新增命名空间不需要新增 fileService 表；业务元数据由拥有它的 service 管理。

[Server 总览](server-main.md) · [skillService](server-skill.md) · [agentService](server-agent.md) · [conversationService](server-conversation.md) · [runService](server-run.md)

## 启动配置

默认使用本地 `~/.agentuse/files`，支持 `-files-dir` / `AGENTUSE_FILES_DIR`。选择 S3：

```bash
agentuse-server start -files-backend s3 \
  -s3-endpoint https://s3.example.com -s3-bucket agentuse \
  -s3-region us-east-1 -s3-prefix production
```

凭据读取 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY；参数对应 AGENTUSE_FILES_BACKEND、AGENTUSE_S3_ENDPOINT、AGENTUSE_S3_BUCKET、AGENTUSE_S3_REGION、AGENTUSE_S3_PREFIX 环境变量。部署配置在启动时读取，修改后重启。S3 兼容后端须支持条件 PutObject（If-None-Match: *）；不支持时发布失败，不降级覆盖。

Conversation 删除提交后清理对应文件前缀；失败写入 server 日志，当前由运维重试清理，不提供另一份文件元数据账本。Skills 前缀不随 conversation 删除。

实现使用 Go 1.25 的 os.Root 原子 Link/Rename，在限定根目录内发布文件；项目最低 Go 版本相应为 1.25。
