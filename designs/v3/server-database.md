# Server 元数据数据库

Server 使用 GORM 和 gormigrate，支持 SQLite、MySQL、PostgreSQL。默认 SQLite；三种驱动均可在 `CGO_ENABLED=0` 下运行。具体参数与连接示例见 [数据库文档](../../docs/server/database.md)。Client 的文件存储不参与本次调整。

## 分层与模型

service 继续持有业务逻辑、事务边界和各表的写入权限，跨服务操作传递同一个 `*gorm.DB` 事务。store 管理数据库连接、GORM 模型、迁移、方言和投影扫描。API/SDK DTO 不带 GORM 持久化标签，不把数据库关联或默认软删除行为引入协议。

普通写入和冲突处理使用 GORM；复杂投影可保留显式 SQL，通过 GORM 绑定参数。JSON 对象展开、属性提取和附件引用查询的三库差异只在 [dialect.go](../../server/internal/store/dialect.go) 中实现，业务层不包含 SQLite 专有函数。消息目标仍属于 messages.agent_targets，不恢复 message_agents、Work 或预分配 run/input。

路由元数据及附件快照在 SQLite 使用 TEXT，MySQL 使用 JSON，PostgreSQL 使用 JSONB。原始 run_events.payload、run_requests.payload 和 runs.result 使用文本，避免 JSONB 改写原始事件字节。model、usage 仍由已保存的 result 投影，不重复增加数据库列。

所有时间继续使用 Unix 秒，字段声明禁用 GORM 自动时间戳。可空字段使用指针；不会因 GORM 更新时忽略零值而丢失明确的 0。Agent/Skill 的逻辑删除仍使用现有 deleted_at 业务规则。

## 初始化与升级

连接 → 取得迁移锁 → 检查版本 → 执行未完成迁移 → 释放锁 → HTTP/WebSocket 监听。迁移入口为 [migrations.go](../../server/internal/store/migrations.go)，初始模型在 [models.go](../../server/internal/store/models.go)。业务表不在每次启动时 AutoMigrate。

当前基线 `0001_initial` 只用于新库；此前 schema 10 等未版本化数据不读取、不转换，也不自动删除。新基线之后新增有序迁移，保持已有迁移不变。迁移中需要固定当时的结构，不能通过不断变化的 API DTO 推导历史建表。数据库存在未知迁移版本时拒绝启动。

SQLite 的 IMMEDIATE 事务覆盖版本检查、建表及版本写入。PostgreSQL 使用同一固定连接上的 advisory lock，并在事务中执行迁移。MySQL 使用按数据库命名的 GET_LOCK，在同一连接释放；DDL 隐式提交，不能用普通事务代替迁移锁或承诺整批回滚。步骤失败时阻止启动，不自动清表，后续步骤需设计可重入性或明确恢复流程。

## 元数据事务

本次保留原有串行元数据事务语义，覆盖消息序号、Agent/Skill 版本推进、派发快照、事件去重、run 结果与最终回复、轮次结束和文件配额。SQLite 保持单连接和 IMMEDIATE 事务；MySQL/PostgreSQL 的事务先 SELECT store_locks 固定行 FOR UPDATE，提交或回滚时释放。

锁在数据库中生效，不依赖 Go mutex，多个连接也不能同时通过“查询后写入”的业务判断。它没有把元数据事务优化成按 conversation 并发；若后续细化锁范围，需要一起验证父子会话和跨服务清理的锁顺序。MySQL 固定开启 clientFoundRows，使匹配但未改变值的更新计数与另外两库一致。原子单条语句仍可直接执行。通知与文件补偿保持原有提交时序。

## 三库约束

- 字符串标识显式限定长度；MySQL 业务表使用 utf8mb4_bin，身份及名称区分大小写。
- 子会话通知使用 conversation_id、author_conversation_id、author_round_seq 联合唯一索引；普通消息的后两项为 NULL，因此无需部分索引。
- conversation_rounds.active_marker 是生成列，活动时为 1，结束后为 NULL；联合唯一索引保证每个 conversation 最多一个活动轮次。
- 附件完整相对路径不截短，path_hash 保存其 SHA-256，使用 conversation_id、path_hash 联合唯一索引。路径与目录冲突按同一套 Go 规则比较，避免数据库字符串函数与字符计数差异。
- run_controls.seq 是数据库生成的内部排序号，替代隐式 rowid；公开控制身份继续使用 id。

业务表的逻辑结构分别在所属设计中维护：[conversation](server-conversation.md)、[文件](conversation-files.md)、[run](server-run.md)、[Agent](server-agent.md)、[Skill](server-skill.md)、[设备](server-device.md)、[用户](server-user.md)。本稿仅定义数据库基础设施表：

| 表 | 字段与用途 |
|---|---|
| schema_migrations | id 为迁移版本主键，由 gormigrate 写入，只记录已完成步骤 |
| store_locks | id 为整数主键，固定一行 id=1；用于元数据事务的数据库行锁 |

## 验证

同一套 schema、service 和 HTTP 测试分别运行于 SQLite、MySQL 8.4、PostgreSQL 17，测试各自创建并清理独立数据库。数据库失败注入通过各自的真实触发器完成，不跳过回滚场景。覆盖并发初始化、迁移重入、数据保留、未知版本、空值与大小写、活动轮次唯一性、消息与执行、父通知、文件路径和并发配额。构建与业务测试均在无 CGO 模式下验证；竞态检测使用 Go race 所需的构建环境。
