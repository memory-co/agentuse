# Server upgradeService

upgradeService 管理单次 Server 升级，从 GitHub 查询正式版、准备并校验本地文件、替换、请求进程接管，持久保存版本历史。controller 负责管理员鉴权和异步 HTTP 接口，CLI 与网页调用同一接口；CLI 启动层负责停止服务及以新二进制接管当前 PID。不会从网页接收任意下载地址或可执行路径。

## 状态与文件

`POST /api/upgrades` 先保存 `checking` 记录，再启动独立于 HTTP 请求的后台工作。状态流转为 `checking → downloading → prepared → installing → restarting → succeeded`；无可用新版本为 `unchanged`，失败为 `failed`。只有下一次成功初始化的 Server 版本匹配目标版本时才确认成功。

官方来源固定为 `memory-co/agentuse` 的 latest release。准备目录为 `~/.agentuse/upgrades/<id>/`，包括本平台 Server、四个平台 Client、校验文件、替换清单和备份。全部校验完成后才在安装目录准备临时文件，Client 分发文件先替换，Server 最后替换；每个目标以同目录 rename 原子替换。替换及进程接管失败恢复原文件；数据库仍按版本迁移管理，不承诺自动数据库降级。

启动配置和日志继承，保持 PID 以适配前台、nohup 和 systemd 等启动方式。重启参数去掉设置密码、重置令牌等一次性选项。启动时检查未完成记录：目标版本成功启动则完成记录，其他中断记录恢复文件并标记失败；仍由存活进程拥有的操作不被另一进程接管。

## 数据库

新增迁移 `0002_server_upgrades`，保留 `0001_initial` 不变。以下仅定义本服务拥有的表，其他业务表沿用各自设计。

```sql
CREATE TABLE server_upgrades (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL,
    instance_key TEXT NOT NULL,
    owner_pid INTEGER NOT NULL,
    active_marker INTEGER,
    from_version TEXT NOT NULL,
    target_version TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    release_url TEXT NOT NULL DEFAULT '',
    downloaded_bytes INTEGER NOT NULL DEFAULT 0,
    total_bytes INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    completed_at INTEGER
);
CREATE UNIQUE INDEX server_upgrades_id ON server_upgrades(id);
CREATE UNIQUE INDEX server_upgrades_active ON server_upgrades(instance_key, active_marker);
CREATE INDEX server_upgrades_instance ON server_upgrades(instance_key, seq);
```

`instance_key` 由主机及可执行文件路径计算，数据库本身限定 Server 数据范围。`active_marker` 在执行中固定为 1，结束后置 NULL，通过跨库唯一索引保证同一安装只执行一个升级。`seq` 是历史分页的稳定顺序，公开 ID 为 UUID。记录原版本、目标版本、发起管理员、时间、下载字节数及错误；只修改 `server_upgrades`，不向 conversation 插入运行或升级消息。

API、CLI、阶段说明与使用限制见 [自升级文档](../../docs/server/upgrades.md)。
