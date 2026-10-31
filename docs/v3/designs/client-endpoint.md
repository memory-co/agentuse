# Client endpointService：已注册的 Server

> v3 目标设计。只管理 `~/.agentuse/endpoints.yaml`，记录 client 向哪些 server 注册了。不使用 SQLite，不负责连接、领取或执行调度。

## 职责

CLI 完成注册，取得该 server 的 device_id、device_key 后，交给 endpointService 保存。runService 读取启用的 endpoint，自己负责连接、心跳、领取和回报。

endpointService 提供新增、查询、修改、启用/停用和移除操作。一个 endpoint 是一份已注册身份，不保存尚未完成注册的临时状态。

## endpoints.yaml

```yaml
endpoints:
  - id: 66c645f1-f128-4c83-b29a-93bd1f181017
    name: 公司平台
    server_url: https://company.example.com
    enabled: true
    device_id: device-company
    device_key: <该平台签发的设备密钥>
  - id: cc9b6a6f-3d19-4cdd-b5e8-217eb15e7784
    name: 个人平台
    server_url: https://personal.example.com
    enabled: true
    device_id: device-personal
    device_key: <该平台签发的设备密钥>
```

| 字段 | 含义 |
|---|---|
| id | 本机生成的稳定 UUID，本地执行与 session 中称为 endpoint_id |
| name | 展示名称 |
| server_url | 此注册对应的 server 地址 |
| enabled | 是否允许 runService 使用这份注册领取新任务 |
| device_id / device_key | 此 server 签发的设备身份/凭据，不跨 endpoint 共用 |

文件缺失表示尚未注册任何平台，首次写入创建文件。保存前校验 ID 不重复、地址合法、身份与凭据完整；名称修改不改变 ID，换成另一套 server 则新增 endpoint。

文件是唯一配置来源，没有 endpoints 表或并存的中心 JSON 配置。CLI 写入使用文件锁和原子替换，文件权限为 0600，父目录仅当前用户可访问；状态输出和本机页面不显示密钥。内存中的查找索引可从文件重建，不是另一份事实来源。

## 与 runService 的边界

endpointService 不保存在线状态、连接实例、重连、领取授权或执行槽位。runService 读取文件后维护运行时连接，配置提交后的通知只表示“注册信息改变”。启动和定期核对同样以 YAML 为准。

本机端口、执行容量与临时目录策略归 [settingService](client-setting.md) 的 setting.yaml，不放到 endpoints.yaml。

停用 endpoint 后，runService 停止新领取并保留已有任务的续租和结果回报。正常移除先由 runService 停领收尾，再删除 YAML 项；endpointService 不直接取消执行或关闭原生 session。

删除注册不删除 session/run 状态文件或原生历史。重新注册产生新的本机 UUID，不自动接管旧执行命名空间。所有业务请求仍使用该次执行绑定的 endpoint 上下文，不能切换成当前选中的平台。

## 相关设计

[Client 总览](client-main.md) · [settingService](client-setting.md) · [runService](client-run.md) · [sessionService](client-session.md) · [Server deviceService](server-device.md)
