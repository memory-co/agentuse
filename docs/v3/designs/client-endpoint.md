# Client endpointService：已注册的 Server

> v3 目标设计。只管理 `~/.agentuse/endpoints.yaml`，记录 client 向哪些 server 注册了。不使用 SQLite，不负责连接、领取或执行调度。

## 职责

CLI 完成注册，取得该 server 的 device_id、device_key 后，交给 endpointService 保存。runService 读取启用的 endpoint，自己负责连接、心跳、领取和回报。

endpointService 提供新增、查询、修改、启用/停用和移除操作。一个 endpoint 对应一个规范化 server 地址及其当前注册身份，不保存尚未完成注册的临时状态。

## 内置 local

`endpoint_id: local` 默认存在，由 endpointService 统一提供，不写入 endpoints.yaml。它不注册设备、不连接中心，供用户创建 conversation 并调试本机 Codex、Claude Code 或自定义 harness。local 执行复用 client 的 session/run/harness 流程。

endpointService 的 List / GetByID 统一解析 endpoint 身份与模式，Views 提供不含密钥的页面数据；Registrations 专门读写文件中的平台注册。runService 消费这些结果，不自行拼装 local 选项或建立另一套 endpoint 列表。

## endpoints.yaml

```yaml
endpoints:
  - name: 公司平台
    server_url: https://company.example.com
    enabled: true
    device_id: device-company
    device_key: <该平台签发的设备密钥>
  - name: 个人平台
    server_url: https://personal.example.com
    enabled: true
    device_id: device-personal
    device_key: <该平台签发的设备密钥>
```

| 字段 | 含义 |
|---|---|
| name | 展示名称 |
| server_url | 此注册对应的 server 地址 |
| enabled | 是否允许 runService 使用这份注册领取新任务 |
| device_id / device_key | 此 server 签发的设备身份/凭据，不跨 endpoint 共用 |

文件缺失表示尚未注册任何平台，首次写入创建文件。保存前校验规范化地址及其推导 ID 不重复、地址合法、身份与凭据完整；名称修改不改变 ID，换成另一套 server 则新增 endpoint。

文件是唯一配置来源，没有 endpoints 表或并存的中心 JSON 配置。CLI 写入使用文件锁和原子替换，文件权限为 0600，父目录仅当前用户可访问；状态输出和本机页面不显示密钥。内存中的查找索引可从文件重建，不是另一份事实来源。

## endpoint_id 生成规则

endpoints.yaml 不声明 id 或 endpoint_id，只保存 server_url 等注册信息。endpoint_id 在加载时由 server_url 推导，不生成 UUID，也不写回 YAML。例如 `https://company.example.com` 对应 `https---company.example.com`，其中 `://` 替换为 `---`。YAML 是注册信息的唯一持久化来源，地址、ID 和本地目录始终能对应。

先规范化 URL：只接受 HTTP/HTTPS，scheme 和主机名小写，移除默认端口及末尾 `/`，保留非默认端口与有意义的路径前缀，不接受用户名、密码、query 或 fragment。再将 `://` 替换为 `---`；其余字符中仅保留 ASCII 字母、数字、`.`、`-`、`_`，其他字符按 UTF-8 字节进行大写百分号编码，包括 `%`、`:`、`/`，避免路径分隔符与替换碰撞。例如 `http://localhost:8090` 对应 `http---localhost%3A8090`。不同规范化地址不得映射为同一 ID。

同一地址只能保存一份当前注册；名称、启停或设备凭据变化不改变 endpoint_id。修改 server 地址视为移除旧 endpoint 并新增新 endpoint，不自动移动旧目录。

## 与 runService 的边界

endpointService 不保存在线状态、连接实例、重连、领取授权或执行槽位。runService 读取文件后维护运行时连接，只在启动时加载 YAML 快照。运行期间不监听、重读或按文件差异重建连接。

本机端口与临时目录容量策略归 [settingService](client-setting.md) 的 setting.yaml，不放到 endpoints.yaml。

新增、停用、移除 endpoint 或修改凭据均在重启 client 后生效。运行中的连接始终使用启动快照；先正常停止 client，再编辑配置并重新启动。endpointService 不根据文件变化取消执行或关闭 session。

删除注册不删除 session/run 状态文件或原生历史。同一规范化地址重新注册仍使用相同 endpoint_id 和目录；旧文件不代表新凭据拥有执行授权，runService 必须先向 server 对账再决定能否继续，不自动执行旧 run。所有业务请求仍使用该次执行绑定的 endpoint 上下文，不能切换成当前选中的平台。

## 相关设计

[Client 总览](client-main.md) · [settingService](client-setting.md) · [runService](client-run.md) · [sessionService](client-session.md) · [Server deviceService](server-device.md)

配置生命周期统一为：启动时加载 setting.yaml、endpoints.yaml 和 harness 包，运行期间固定使用快照。修改、增删、启停和凭据变更一律重启生效；没有热更新、周期重读、重新探测或 active_work_root / restart_required 双态字段。自动重连、任务控制和 quota 检查属于运行行为，不重新加载配置。
