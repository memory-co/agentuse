# Client endpointService：启动时固定的输入来源

endpointService 管理 endpoints.yaml，向 client 提供已注册来源的身份。它不创建连接、不领取任务、不选择 native run_id，也不维护在线状态或执行尝试。

## 文件

```yaml
endpoints:
  - name: company
    server_url: https://company.example.com
    enabled: true
    device_id: registered-device
    device_key: device-credential
```

endpoint_id 从规范化地址推导，不写入 YAML：例如 https://company.example.com 对应 https---company.example.com。:// 使用 ---，其他不安全路径字节编码，端口、路径等均参与标识，不能只按 host 区分。

来源地址和设备凭据在启动时校验并固定。运行中修改、注册、启停、移除或换凭据不改变当前进程；必须重启。不发送重读通知或定期扫描，不根据磁盘差异动态创建/销毁连接。

注册 CLI 可以原子更新文件，但不会修改已运行 client 的快照。文件缺失表示没有远端注册，仍可使用 local。保存前检查地址合法、凭据完整及规范化地址不重复。

## 内置 local

local 始终存在，不声明在 endpoints.yaml 中，不连接中心。它提供本机输入入口，使用同一 sessionService、harnessService、公共事件和 native run 查询；不创建 local 专用的 run/attempt、结果账本或执行协议。

local conversation 的任意标题保存在 conversation.yaml，目录使用 UUID；标题不能作为文件路径。远端 conversation 标识来自其来源，namespace 保持隔离。

## 查询与边界

| 接口 | 行为 |
|---|---|
| List / Views | 返回启动快照及 local；Views 不含 device_key |
| GetByID | 解析本机来源，不切换执行中任务的身份 |
| 注册 / 保存 / 移除 | 更新 YAML，重启后生效 |

同一来源下的实际 run UUID仍由 harnessctl 分配，不能用 server 派发 ID 替代。不同 endpoint 的工作目录、session 和公开查询互相隔离；凭据只传给该来源的调用，不通过修改进程全局环境切换。

现有 server 的旧版派发、续租、确认和业务 CLI 上下文属于 run/server_*.go 的临时协议边界；endpointService 不解释这些业务状态。删除来源配置不删除本机历史，不代表授权重新执行旧输入。
