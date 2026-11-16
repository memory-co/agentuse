# 数据库配置与升级

Server 使用 GORM 访问元数据数据库，用 gormigrate 管理有版本的结构变更。默认仍为 SQLite，数据库文件是 `~/.agentuse/server.db`。Client 继续使用自己的文件存储。

## 选择数据库

| 参数 | 环境变量 | 默认值 | 含义 |
|---|---|---|---|
| `--db-driver` | `AGENTUSE_DB_DRIVER` | `sqlite` | `sqlite`、`mysql`、`postgres` |
| `--db` | `AGENTUSE_DB` | `~/.agentuse/server.db` | SQLite 文件路径，或所选数据库的 DSN |

SQLite 不需要外部服务：

```bash
agentuse-server start
agentuse-server start --db /data/agentuse/server.db
```

`:memory:` 创建临时 SQLite 数据库，进程退出后不保留数据。切换数据库驱动时，必须同时设置对应的 DSN；Server 不会创建 MySQL 或 PostgreSQL 数据库。

MySQL 示例（先创建 `agentuse` 数据库）：

```bash
export AGENTUSE_DB_DRIVER=mysql
export AGENTUSE_DB='agentuse:password@tcp(127.0.0.1:3306)/agentuse?charset=utf8mb4'
agentuse-server start
```

PostgreSQL 示例：

```bash
export AGENTUSE_DB_DRIVER=postgres
export AGENTUSE_DB='postgres://agentuse:password@127.0.0.1:5432/agentuse?sslmode=disable'
agentuse-server start
```

上例是本地连接；远端连接的 TLS 参数由 DSN 指定。当前回归使用 MySQL 8.4 和 PostgreSQL 17。数据库账号需要在目标库中执行建表、索引及结构变更。

## 无 CGO 构建

SQLite 使用纯 Go 的 `github.com/ncruces/go-sqlite3/gormlite`；MySQL 和 PostgreSQL 分别使用 GORM 的 MySQL、PostgreSQL 驱动。gormigrate 复用同一 GORM 连接，不额外加载数据库驱动。Server 和 Client 制品均以 `CGO_ENABLED=0` 构建，不依赖系统 SQLite 动态库或 C 编译器。

## 结构版本

启动连接数据库后，Server 先取得迁移锁，再执行尚未记录的迁移；全部成功才开始提供 HTTP/WebSocket 服务。`schema_migrations` 保存已完成的版本，当前基线是 `0001_initial`。重复启动不会重建表、重复回填或覆盖数据；数据库包含当前二进制不认识的迁移时拒绝启动。

迁移定义见 [migrations.go](../../server/internal/database/migrations.go)，初始表结构见 [models.go](../../server/internal/database/models.go)。已发布的迁移保持不变，后续升级新增有序迁移，明确处理字段重命名、删除、数据回填及数据库特有的约束。业务表不会在每次启动时执行 `AutoMigrate`。

本次是新的结构基线，**不迁移此前的 SQLite schema 10 或更早的数据**。已有业务表但没有迁移版本的数据库会报 `unsupported database schema: reset the database before starting this version`。按项目约定使用新的空数据库，重新创建资源和注册设备。此后从新基线升级时，使用版本迁移保留已有数据；不要每次发布都删除数据库。

MySQL 的 DDL 会隐式提交，不能承诺失败时整批回滚。迁移失败时 Server 停止启动，记录已完成版本；需要处理失败原因后重试，不自动删除表或数据。

## 事务与数据规则

跨业务操作共享同一个 GORM 事务，结果、最终消息及轮次边界一起提交，通知在提交后发出。本次保留原先元数据事务串行执行的语义：SQLite 使用单连接和 `BEGIN IMMEDIATE`；MySQL/PostgreSQL 在事务开始时锁定 `store_locks` 的固定行。锁由数据库管理，覆盖多个连接，不依赖进程内互斥锁。

查询中的 JSON 方言差异集中在 [dialect.go](../../server/internal/database/dialect.go)。查询需要访问的路由和附件快照使用 SQLite TEXT、MySQL JSON、PostgreSQL JSONB；原始执行事件及 result 使用文本保存，保留原始内容。消息目标仍存于原消息，不增加投递表。

各数据库保持相同的唯一性和空值规则：每个对话只允许一个活动轮次，每个子对话轮次只通知父侧一次。附件完整相对路径仍支持 4096 字节；数据库索引规范化路径的 SHA-256，避免 MySQL 索引长度限制，不截短实际路径。`run_controls.seq` 是数据库生成的内部排序号，替代 SQLite 隐式 rowid；公开控制 ID、run/input 身份保持原有含义。

数据库备份需要与文件后端一起规划；只备份元数据不包含会话附件、Skill 包等文件字节。文件后端可使用本地目录或 S3，与数据库类型独立。

## 三库回归

普通测试默认使用 SQLite：

```bash
CGO_ENABLED=0 go test ./server/internal/database ./server/internal/service/test ./server/internal/controller
```

外部数据库使用同一套测试，每个测试自动创建独立数据库并清理，因此测试账号还需要创建、删除数据库的权限：

```bash
AGENTUSE_TEST_DB_DRIVER=mysql \
AGENTUSE_TEST_DB_DSN='root:password@tcp(127.0.0.1:3306)/agentuse' \
CGO_ENABLED=0 go test -count=1 ./server/internal/database ./server/internal/service/test ./server/internal/controller

AGENTUSE_TEST_DB_DRIVER=postgres \
AGENTUSE_TEST_DB_DSN='postgres://postgres:password@127.0.0.1:5432/agentuse?sslmode=disable' \
CGO_ENABLED=0 go test -count=1 ./server/internal/database ./server/internal/service/test ./server/internal/controller
```

测试覆盖并发初始化、迁移重入与未知版本、约束、消息派发、accepted、最终结果、失败回滚、父子会话、文件路径和并发配额。CI 对三种数据库分别执行这些用例及无 CGO 构建。

升级记录由迁移 `0002_server_upgrades` 新增，`0001_initial` 保持不变，已有数据继续保留；表结构及约束见 [自升级文档](upgrades.md)。
