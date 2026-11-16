# Client–Server 安全通道与执行期授权（设计草案）

> 状态：待实现。本稿描述下一版设备通信和授权，不表示当前代码已支持这些配置、证书或令牌。当前仍使用 `agentuse-client/2`、一次性 `st_…` 注册令牌和长期 `dk_…` Bearer 凭据。本文仅覆盖 Go Client 与 Server；浏览器、用户 SDK 和管理员 CLI 沿用各自的用户身份。

## 目标与通信边界

Client 的 WS 控制链路增加可关闭的内层加密，默认开启。无论外层是 `ws://` 还是 `wss://`，开启时均具有独立的机密性和设备身份认证；已有 HTTPS/WSS 的部署可以显式关闭内层加密。

长期设备身份只用于建立设备连接和管理设备自身证书。每次消息投递通过已认证的 WS 发放短期访问令牌，允许 Client 在该输入的有效执行期间调用限定的 HTTP 业务接口。执行结束或授权到期后，旧令牌无法继续向 Server 拉取内容。

| 通道 | 承载内容 | 本稿变更 |
|---|---|---|
| Client–Server WS | 消息投递、公共事件、心跳、提问相关事件、回答、取消 | 增加安全握手及短期授权的分发、刷新和撤销通知 |
| Client–Server HTTP | 固定 Agent/Skill 版本、Skill ZIP、上下文、对话操作、附件与文件 | 保留现有 URL 和文件流方式，设备业务访问改用短期令牌 |
| 浏览器与 Server | 页面、用户 HTTP API、浏览器通知 WS | 保持现有认证与传输方式，不要求设备证书 |
| 本机 context 与 Client | 执行身份和本机工具调用 | 凭据由 Client 持有，context 通过 Client 选择当前执行的授权 |

Skill 和附件的文件字节不搬入 WS，不为文件增加一种 WS 消息。本稿也不加密磁盘缓存。HTTP 文件请求使用 `http://` 时，正文及 Bearer 令牌仍可被链路观察者读取；WS 内层加密和令牌过期不能替代 HTTP 的传输加密。需要保护这些请求内容的部署应使用 HTTPS；独立的 HTTP 应用层加密不在本稿内。

## 一个 WS 加密开关

Server 的 `settings.yaml` 增加：

```yaml
security:
  ws_encryption: true
```

省略时为 `true`，不是根据 URL scheme 猜测默认值。配置在启动时加载，修改后重启 Server；浏览器通知 WS 不读取此设置。

| Server 设置 | Client 实际连接 | 处理 |
|---|---|---|
| `true` | `ws://` | 建立内层 TLS 后认证和传输业务消息 |
| `true` | `wss://` | 外层 WSS 加内层 TLS |
| `false` | `wss://` | 使用外层 TLS，设备证书通过应用握手证明持有私钥 |
| `false` | `ws://` | 拒绝；关闭内层加密的使用场景是已有 HTTPS/WSS 的部署 |

Client 不增加第二个独立开关。Server 通过签名的握手策略声明当前设置；Client 验证安装时固定的 Server 信任后使用它。网络错误、TLS 错误和旧版本响应均不能触发自动降级。

关闭内层加密时，Server 必须配置 HTTPS `public_url`，Client 必须实际连接并正常验证 WSS 的主机名和证书。反向代理负责外层 TLS，代理到 Server 的路径也属于该部署的可信边界；Server 不依据任意请求携带的 `X-Forwarded-Proto` 决定连接是否可信。

## 设备身份与首次信任

现有 `dk_…` 是共享 Bearer 凭据，不是私钥或证书。目标改为每个 endpoint 一套设备私钥和证书；注册仍保持“一串安装码”的体验。

安装码包含版本、Server 地址、Server 身份 CA 的公开证书和一次性注册令牌。它不是设备私钥，Base64 编码也不提供保密性。安装码经用户可信渠道取得；Client 程序本身也应来自可信安装渠道，不能依赖明文 HTTP 下载的脚本证明自身可信。

```text
Client 本地生成设备密钥对
  → 按安装码固定的 Server 信任验证握手
  → 在受保护连接内提交一次性注册令牌和 CSR
  → Server 事务内消费注册令牌，绑定设备、公钥和凭据代次
  → Server 返回设备证书
  → Client 保存私钥与证书，重新建立已认证的设备连接
```

Server 自己保存身份 CA 私钥、Server 通道证书及签名密钥；这些材料持久保存，不能每次启动重新生成。设备证书绑定 `device_id`、凭据代次和 Client 身份用途，由 CA 签发；初版使用标准 X.509 证书及 Ed25519 密钥。

Client 私钥只在本机生成和保存，不发给 Server，不写入安装命令、WS JSON、日志或原生 Agent 环境。Server 保存设备公钥标识、证书序列号、有效期、凭据代次及撤销状态。证书链有效不等于设备仍获授权：每次建立连接还必须检查设备存在、代次与撤销记录。

`endpoints.yaml` 继续保存 Server 地址、设备 ID 和非秘密的证书文件引用；私钥与设备证书保存在该 endpoint 的独立私有目录，文件权限为 `0600`。endpointService 保存这些注册材料，runService 使用启动快照建立连接。续签原子替换证书材料，不改变 endpoint、conversation、session 或 run 身份。

建议证书有效期为 90 天，剩余 30 天时通过已认证 WS 申请续签，CSR 必须证明持有相应私钥。到期前无法续签时停止建立新连接；已有连接也不能无限越过证书有效期。证书已失效的设备由用户重新授权注册，不能通过未认证接口无限自助恢复。

重置或删除设备时，在同一事务撤销其证书、增加凭据代次并撤销全部短期授权，提交后关闭旧 WS。吊销和授权过期不伪造 run 的终态。Server 信任根更换需要旧可信身份授权的更新或用户重新安装可信注册材料，不从未认证 HTTP 响应静默替换信任根。

重置前已发出的目标授权保留旧代次，重新注册不会自动把它们提升到新代次；恢复这些目标的数据访问需要用户明确重新授权。恢复合法的执行事实补报与重新授予数据访问权限分开处理。

## WS 握手、加密和协议版本

目标设备协议使用 `AgentUse-Protocol: agentuse-client/3`。这只升级 Client–Server 通信；原生 `harnessctl/3`、accepted/run/input 的语义不变。实现时两端一起升级，旧 `/2` 明确返回版本不支持，不回退到旧 Bearer 认证。

1. Client 打开 `/api/client/ws`，在 Upgrade 及 URL 中不携带长期秘密或业务访问令牌。
2. Client 发随机挑战；Server 返回签名策略，覆盖 Client 挑战、Server 挑战、连接随机 ID、Server 身份、协议版本及 `ws_encryption`。Client 校验安装时固定的信任、签名与挑战匹配，防止重放旧的关闭策略。
3. 开启加密时，把 WS 二进制数据流包装为 `net.Conn`，使用 Go 标准库建立 TLS 1.3，验证固定的 Server CA、Server 身份与 ServerAuth 用途，以及设备证书的 ClientAuth 用途。仅首次注册允许无设备证书，但只能进入注册操作，不能心跳、领取消息或调用业务接口。
4. 关闭内层加密时，Client 在 WSS 内提交设备证书和私钥签名，签名覆盖本次双向挑战、连接 ID、规范化 Server 地址和协议策略；Server 验证签名、一次性挑战及设备当前状态。注册仍在已验证的 WSS 内完成。
5. 认证完成后才接收 heartbeat、登记设备在线或发送业务消息。挑战只使用一次，握手超时关闭连接；未认证连接设置数量、大小和速率上限。

双方挑战至少使用 32 字节安全随机数。握手签名采用固定域分隔和规范化字段编码，覆盖完整的策略及连接绑定；签名验证不得依赖普通 JSON 的字段顺序或重新序列化结果。Server 策略证书链也必须验证预期身份和签名用途。

内层 TLS 提供密钥协商、对称会话密钥、完整性及前向保密，不自制逐条消息的非对称加密协议。外层 WSS 的部署不需要向反向代理配置设备 mTLS；设备认证在 Server 内的专用连接完成。

策略握手占用明确的初始 WS 消息，完成后切换数据流，不能把缓冲的握手字节误读为 TLS 记录。业务编码统一为“4 字节大端长度 + UTF-8 JSON”，解密后的单条 JSON 上限沿用 8 MiB，先检查长度再分配。TLS 记录和 WS 帧均不作为业务消息边界；关闭 WS 压缩，保持单读者和串行写入。关闭内层加密时也沿用同一业务分帧，避免两份消息解析实现。

外层 WS 的地址、控制帧、长度和时序仍可见。握手失败不会降级；断线仍按现有持久收据恢复，不重新执行来源输入。

## 按消息投递发放访问令牌

权限单位是 `(message_id, agent_id)`，不是单独的 message，也不是 Server 预建的 run。一条消息可以发给多个 Agent，一个 run 可以接受多条消息。

Server 首次投递消息之前，先提交固定目标路由及该目标的授权记录，再经已认证 WS 发放高熵随机令牌，例如 `ma_…`。数据库只保存令牌哈希。它是可撤销的访问凭据，既不是文件解密密钥，也不是新的任务或执行 ID。

```json
{
  "type": "message",
  "id": "m:<message_id>:<agent_id>",
  "conversation_id": "<conversation_id>",
  "message_id": "<message_id>",
  "agent_id": "<agent_id>",
  "agent_version_id": "<fixed_version_id>",
  "content": "…",
  "attachments": [],
  "access": {"token": "ma_…", "expires_at": 1800000000}
}
```

`access` 是本次发送的临时信封字段，不属于不可变 dispatch 快照。Server 不把明文令牌存进 messages、原始执行事件或重放记录；Client 在比较业务快照、落盘收据及记录日志前移除该字段。相同消息重投递可以更换授权令牌，消息 ID、版本、正文、附件及 send 去重规则保持不变。

令牌绑定设备和凭据代次、来源 conversation、消息、目标 Agent 及其固定版本，并保存允许的操作范围。初始 run 尚不存在，不能要求先有 accepted 才允许读配置或 init；accepted 后，通过现有 `run_inputs` 和 runs 查询绑定，不另复制一个执行状态机。

| 能力 | 许可范围 |
|---|---|
| Agent/Skill 配置和 Skill ZIP | 此目标固定的 Agent 版本及其明确引用的 Skill 版本；不能枚举或改读 latest |
| 当前对话与 complete_messages | 来源 conversation，沿用既有共享历史和 session 路由检查 |
| 文件列表与下载 | 来源 conversation 的既有文件空间；消息附件选择不是文件 ACL，不收窄为本消息的附件 ID |
| 输出上传 | 来源 conversation，且必须已 accepted、实际 run 仍活动；继续禁止写 `user_input/` |
| 嵌套对话与关联文件 | 在有效执行来源下，按现有 collaborative、父子关系及用户所有权规则校验；跨对话必须明确目标 |

Server 根据 grant 恢复来源和设备；请求携带的 conversation、Agent、版本、session/run 及目标路径仍须与之匹配。`X-Agentuse-*` 是待验证的来源声明，不是独立授权。禁止设备令牌冒用管理员或通过 `X-Agentuse-User` 扩权。

长期设备证书不能直接兑换任意消息的 grant。Server 只能为已实际投递给该设备、仍在有效授权窗口中的目标发放或续签，也不能用新的 grant 让已结束的目标重新获得权限。

## 有效期、实际 run 和续期

初始建议：单个令牌有效 5 分钟；Client 在剩余 1 分钟时按需刷新；受理前准备预算为首次投递后的 30 分钟；每个投递目标的总授权预算默认为首次投递后 24 小时。预算均由 Server 时钟计算，实现时提供服务端策略配置并验证长任务场景。

总预算在首次投递时固定，重连、换令牌、重启或重复 accepted 均不重置。预算用完后即使 Client 一直不提交 result，也不能无限续期；任务本身不因此被伪造为失败。合法超长任务需要用户显式延长预算，设备凭据无权自行延长。

设备通过有效 grant 创建的后续消息、子对话输入及其自动父通知，应继承引发它的用户授权预算上限，不能靠不断给自己发消息或生成子任务取得新的 24 小时。来源消息引用和预算上限作为消息的安全元数据保存，grant 记录 `authorization_root_message_id`，不另造执行 ID。只有用户或管理员的新授权可以建立或延长预算。父 run 正常结束只撤销自己的访问，不提前取消仍在预算内的子任务；子任务结果触发的父侧新投递仍受原预算约束。

| Server 可确认的情况 | 授权行为 |
|---|---|
| 已发送，尚未 accepted | 准备预算内可读本次初始化所需资源；不允许输出上传或需要活动 run 的协作写入 |
| accepted，实际 run 未终态 | 在总预算内按需续签；waiting 和排队受理不重置预算 |
| 同一 run 接受多条消息 | 各消息/Agent 保留自己的 grant；新增消息不会让已过期的旧 grant 自动恢复 |
| result 原子提交 | 通过 run_inputs 撤销该 run 下全部目标 grant，与结果及回复事务一起提交 |
| 已确定受理前失败或明确拒绝 | 与否定回执一起撤销该目标授权 |
| 受理不确定、WS 断线 | 不伪造终态，按原有收据恢复；无法确认 active 时不能无限越过准备预算续签 |
| 设备重置、删除或凭据撤销 | 撤销全部 grant 并断开设备连接 |

过期是访问授权的状态，不是执行事实。Server 仍应接收已派发目标的合法 accepted/result 重放，按原有路由和幂等规则处理；否则过期可能阻止结果确认，并留下无法结束的任务。晚到的 accepted 本身不延长预算，终态结果也不会重新开放 grant。

业务凭据刷新使用单独的 `type=access` 传输消息，指定 `op=refresh`、消息 ID、Agent ID 和临时请求关联 ID；Server 校验上述资格后返回新的 `access`，或明确的 `expired`、`closed`、`revoked`。关联 ID 只用于传输，不增加业务身份。刷新不执行 send，不建立 run，也不用于重放 HTTP 业务写入。

刷新事务撤销该目标的旧令牌并保存新哈希；明文新令牌只通过当前已认证 WS 返回。重连时可重新发放尚获授权目标的令牌，终态目标只恢复收据，不恢复授权。不提供长期设备身份直接调用的 HTTP 令牌兑换接口。

服务端持久化检查决定失效，不依赖 Client 收到撤销通知、定时清理或关闭进程。撤销后拒绝新 HTTP 请求；长文件流至少每秒复验授权及到期时间，失效后停止继续传输，已经发送的字节无法收回。上传在文件发布和业务提交前还须在事务内复验，过期或撤销时清理候选文件。流取消通知只是加速手段，多 Server 实例仍以数据库记录为准。

## HTTP 接入和绕过检查

设备 HTTP 业务请求改为 `Authorization: Bearer ma_…`。配置、上下文、conversation、文件和附件接口统一识别 grant，再交由各业务 owner 检查具体范围；不保留“失败后退回 dk”的路径。短期令牌也不放入 URL、重定向、下载地址、环境变量或日志。

浏览器和人类 SDK 继续使用自己的用户身份，可以按用户权限读取已结束任务的文件。设备 grant 不继承用户的全部权限，不能拿它调用管理、升级、用户或设备配置接口。设备健康与续签通过已认证 WS 完成，长期身份不再提供读取历史任务内容的 HTTP 后门。

HTTP 读请求因令牌轮换失败时，可以经 WS 获取仍合法的授权后重试。发送消息、创建子对话、上传等写操作仍遵守原有幂等或读后核对规则，不因 401/403 或断线盲目重放 POST。

HTTP 未加密时，`ma_…` 仍是可被截获并在有效期内重放的 Bearer 凭据；本稿降低其权限和存活时间，不承诺 HTTP 正文保密或持有者证明。HTTPS 是这些请求的传输保护，不能把证书身份或 `ws_encryption=true` 展示成“所有 Client 通信均已加密”。

## Client 与 context 的生命周期

设备私钥由 Client 守护进程保管，短期令牌只保存在 Client 内存，并经现有本机执行上下文入口代理 HTTP 请求。原生 Agent、harnessctl、模型提示、context.json、工作目录和公共事件均不携带这些凭据；只从 env 移除字符串不足以防止同用户权限的恶意进程读私有文件，完整主机失陷不在这一隔离边界内。

“恶意执行进程在结束后不能冒充设备”还依赖操作系统或执行沙箱隔离 Client 的私有目录、进程和其他 session 的 IPC 权限。同用户且具有任意文件读取或进程调试能力的原生程序可能取得长期私钥；`0600` 本身不能阻止它。缺少这层隔离时，本稿仍能撤销旧 grant，但不承诺阻止攻击者冒充整个 Client、接收未来任务或取得其他活动授权。

实现时，裸 context 的远端操作经既有 Client 进程的受限本地 IPC 代理，CLI 不再自行读取设备凭据直连 Server。IPC 采用私有 Unix socket 或 Windows named pipe，不依赖可以关闭的本机 Web UI，也不增加新的常驻进程。Client 从受管 session 绑定、公共 status 和实际输入收据校验来源，不接受调用方指定任意 grant 或借用其他 run；纯本地 status、历史和缓存读取不需要远端授权代理。

Client 资源准备期间使用本次投递的 grant。原生执行期间，context 仍通过适配器公共 status 确定当前 run，再从该 run 的实际受理输入中选择一个尚有效且具有所需范围的 grant。不能选最近投递、最近 accepted、启动时的消息，或另一个排队 run 的 grant。

Client 观察到本地 result 后立即停止向该执行提供远端访问，即使 Server ACK 尚未到达；事件补报由已认证 WS 独立完成。Server 在 result 提交时完成自己的权威撤销。进程跨 run 复用时，后续 context 调用重新选择实际执行授权，不能沿用上一轮令牌。

这是对当前 context 文档的目标变更：已结束 run 的本地状态、历史和已下载文件仍可读取，但不能再凭长期设备身份拉取远端历史或文件；后续新的合法投递可以按自己的范围访问这些资源。人类需要远端历史时使用自己的用户身份。下载缓存及固定 Skill 包无需因 grant 过期删除，但缓存命中不代表获准发送新的远端请求。

仅凭此前投递给协作 Agent 的 grant，也不能在父 run 结束后继续轮询子任务。子对话整轮结束后，沿用已有父通知创建新的消息投递，父侧凭这次新的授权读取子轮次结果，不延长旧 grant。

## 服务归属与持久化

| 所属 | 职责 |
|---|---|
| deviceService | 注册、证书签发及续签、凭据代次、设备撤销；不判断任务终态 |
| conversationService | 根据已保存的消息目标发放 grant，维护权限、预算和撤销记录，校验对话及文件范围 |
| runService | 在 accepted/result 事务内提供实际归属和终态事实，由 conversation 事件消费者完成授权绑定检查和撤销 |
| hub / controller | 安全握手、消息传输、认证入口及 HTTP 身份解析；不另存执行状态 |
| Client endpointService / runService | 分别保存私有注册材料、管理安全连接与当前授权；harnessService 不解释设备证书 |

通道封装、证书编码和验证使用无业务数据库依赖的公共传输工具包；不增加 securityService、grantService、Work 授权层或 Server harnessService。跨 owner 修改仍传递同一 `*gorm.DB` 事务，发布消息及断开连接在提交后进行。

目标数据结构由后续有序 GORM/gormigrate 迁移增加，不修改已发布的 `0001_initial` 或已有迁移；同时覆盖 SQLite、MySQL、PostgreSQL：

- devices 增加 `credential_generation`；旧 `key_hash` 不再参与设备业务认证。
- device_certificates：证书序列号、device_id、凭据代次、公钥摘要、起止时间及 revoked_at，归 deviceService。
- message_access_grants：token_hash 为唯一键，保存 device_id、凭据代次、message_id、agent_id、authorization_root_message_id、权限描述、created_at、expires_at、budget_expires_at、revoked_at，归 conversationService。固定版本和 run 关系分别由消息目标及 run_inputs/runs 校验，不复制其执行状态。

这些是安全元数据，不创建新消息、投递或 run 身份。轮换后的记录保留原始预算，不靠删除过期行重置它；删除消息时统一清理授权记录。后台清理只能回收记录，不能承担令牌及时失效的正确性。

已注册的旧设备需经用户重新授权绑定本地公钥，不能把 `dk_…` 当作证书私钥转换。切换后两端一起升级；不保留 `/2`、旧设备 Bearer 访问或旧明文注册的降级入口。当前 `/api/client/register` 的旧交换流程退出设备注册路径，设备注册在上述专用连接内完成。

## 开销与验证

TLS 握手和证书校验发生于建立连接，持续消息使用对称加密；WS 长连接摊薄握手开销。WSS 加内层 TLS 会产生两层加密，已有 WSS 的部署可以用开关关闭内层；文件 HTTP 流没有因此增加一层加密或经过 WS 转发。额外开销主要来自 TLS 记录与分帧、grant 查询和续签，不能在没有实测时承诺固定吞吐或 CPU 百分比。

实现验收至少覆盖：

- 开关缺省为开启；on/off 与 ws/wss 的四种组合；浏览器认证和通知 WS 独立。
- 未知 CA、错误 Server 身份、篡改或重放策略、失效证书、设备重置及跨 Server 凭据混用均失败；禁止错误后降级。
- 首次注册不发送明文注册令牌或私钥；同一个一次性令牌并发注册只有一个成功。
- accepted 之前能准备固定配置和 Skill；令牌不能读其他版本、其他设备或范围外对话，不能升级为用户身份。
- 一个 run 多条输入、一条消息多个 Agent、已受理排队 run、持久原生进程跨 run 复用及嵌套父通知的新授权。
- result 与回复失败时撤销也回滚；事务成功后旧 grant 立即拒绝新请求，长流、并发上传及其他 Server 实例观察撤销。
- 断线和重启不重复执行输入、不复活历史授权、不重置预算；授权过期不阻止合法执行事实补报。
- 设备自行派生消息、子任务或父通知不能突破原用户授权预算；正常父任务结束不破坏仍合法的子任务和通知。
- 所有设备业务 HTTP 入口均移除长期凭据旁路；终态后 context 无法远端拉取，本地缓存和用户读取保持各自规则。
- 记录开启/关闭内层 TLS 的握手耗时、消息延迟与 CPU、刷新和查询负载，以及受限长流授权检查的成本。

本轮只提交设计稿和关联入口，不修改运行代码、数据迁移、实际协议版本或当前使用手册。实现后再同步对应文档中的认证规则与测试。

相关设计：[协议](protocol.md) · [设备](server-device.md) · [Client endpoint](client-endpoint.md) · [Client context](client-context.md) · [run](server-run.md) · [数据库](server-database.md)。
