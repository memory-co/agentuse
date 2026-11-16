# Server 自升级

管理员可从管理页面的「系统升级」操作，也可使用 `agentuse-server upgrade`。两种入口调用同一个异步服务，升级记录保存在数据库中，Server 重启后仍可查看。

## 页面与 CLI

页面显示当前版本、GitHub 最新正式版本、下载进度、升级阶段及历史记录。「升级到最新版」会自动下载、校验、替换并重启；重启期间页面短暂断开并自动重新连接。

CLI 使用 `login` 保存的管理员凭据，或通过 `--server`、`--token` 指定运行中的 Server：

```bash
agentuse-server upgrade check
agentuse-server upgrade
agentuse-server upgrade --wait
agentuse-server upgrade status
agentuse-server upgrade status 升级ID
agentuse-server upgrade history --json
```

默认立即返回升级 ID；`--wait` 等待最终结果，默认最长 20 分钟，可用 `--timeout 30m` 调整。停止 CLI 等待不会取消服务端任务。`--json` 输出结构化结果，适合脚本调用。

升级适用于 Linux/macOS 的正式版本二进制。开发构建不自动判断升级顺序。运行 Server 的用户需要能写入当前可执行文件所在目录、Client 分发目录及升级准备目录；权限或文件准备失败会记录为失败，服务继续运行。

## 执行流程

1. 从 `memory-co/agentuse` 的 GitHub latest release 查询最新正式版本。
2. 下载本平台 Server、四个平台 Client 和 `SHA256SUMS`，放到 `~/.agentuse/upgrades/<升级ID>/`。
3. 校验文件长度和 SHA-256，运行候选 Server 的 `version` 检查版本号。
4. 备份原文件，在各目标目录内准备临时文件，然后原子替换；Server 文件最后替换。
5. 停止接受 HTTP 请求，关闭数据库，以新可执行文件接管原 PID。启动参数、环境变量、日志输出和进程管理器的归属保留，`settings.yaml` 重新读取。
6. 新 Server 完成初始化并取得监听地址后，确认这次升级成功。

`--admin-token-stdin`、`--reset-admin-token` 是一次性操作，不会带入升级重启，因此不会重新读取密码或重置管理员凭据。临时分配的监听端口也会保持不变。Client 按现有协议重新连接，Server 不主动重启执行设备上的 Client 进程。

下载、校验失败不替换现有文件；替换或进程接管失败时恢复备份。升级中断后，下一次启动会核对并记录结果。备份用于文件恢复，不替代数据库迁移或数据库降级方案。升级目录保留准备文件和原文件，便于排查；数据库历史记录独立保存。

## 异步 API

以下入口仅允许管理员，支持 cookie 或 Bearer token：

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/api/upgrades` | 当前版本、运行平台、活动升级及历史记录；支持 `cursor`、`limit`（1–100，默认 20） |
| GET | `/api/upgrades/latest` | 查询 GitHub 最新正式版本及是否可升级 |
| POST | `/api/upgrades` | 无请求体；立即返回 `202` 和升级记录，`Location` 指向记录地址 |
| GET | `/api/upgrades/{id}` | 读取升级进度及最终结果 |

同一 Server 安装同时只执行一个升级，重复提交返回进行中的同一记录。异步工作不依赖发起请求的连接；查询进度不会重复查询 GitHub。

状态依次为 `checking`、`downloading`、`prepared`、`installing`、`restarting`，成功启动后为 `succeeded`。异常为 `failed`；当前版本已是最新或高于最新正式版时为 `unchanged`。`completed_at` 非空表示本次操作已经结束。

历史字段包括 `from_version`、`target_version`、`status`、`downloaded_bytes`、`total_bytes`、`release_url`、`error`、发起用户及开始、更新、结束时间。Client 运行、conversation 消息与 Server 升级记录分别管理。

Go SDK 提供 `ServerUpgrades`、`LatestServerRelease`、`StartServerUpgrade`、`GetServerUpgrade`；Node.js/Python SDK 提供对应接口。完整 OpenAPI 可在 Server 的 `/api/docs` 查看。
