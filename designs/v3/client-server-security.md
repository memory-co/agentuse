# Client–Server 安全通道与执行期授权

> 状态：已实现，设备协议为 `agentuse-client/3`，原生适配器仍为 harnessctl/3。本文仅覆盖 Go Client 与 Server；浏览器、用户 SDK 和管理员 CLI 沿用用户身份。旧设备需要按下文重新注册；版本化数据库由 `0003_device_security` 原地升级。

## 目标与通信边界

Client 的 WS 控制链路增加可关闭的内层加密，默认开启。无论外层是 `ws://` 还是 `wss://`，开启时均具有独立的机密性和设备身份认证；已有 HTTPS/WSS 的部署可以显式关闭内层加密。

长期设备身份只用于建立设备连接和管理设备自身证书。消息投递时通过已认证 WS 发放 HMAC 签名票据，受理前只供限定的资源准备；accepted 回传真实 run_id 后，Server 在该 run 保存令牌哈希和授权截止时间。每个 run 使用一个访问令牌，执行结束或授权到期后无法继续拉取内容，不创建独立 grant 表。

| 通道 | 承载内容 | 本稿变更 |
|---|---|---|
| Client–Server WS | 消息投递、公共事件、心跳、提问相关事件、回答、取消 | 增加安全握手、投递票据、run 绑定确认与凭据恢复 |
| Client–Server HTTP | 固定 Agent/Skill 版本、Skill ZIP、上下文、对话操作、附件与文件 | 保留现有 URL 和文件流方式，设备业务访问按准备票据或已绑定 run 令牌校验 |
| 浏览器与 Server | 页面、用户 HTTP API、浏览器通知 WS | 保持现有认证与传输方式，不要求设备证书 |
| 本机 context 与 Client | 执行身份和本机工具调用 | 凭据由 Client 持有，context 通过 Client 选择当前执行的授权 |

Skill 和附件的文件字节不搬入 WS，不为文件增加一种 WS 消息。本稿也不加密磁盘缓存。HTTP 文件请求使用 `http://` 时，正文及 Bearer 令牌仍可被链路观察者读取；WS 内层加密和令牌过期不能替代 HTTP 的传输加密。需要保护这些请求内容的部署应使用 HTTPS；独立的 HTTP 应用层加密不在本稿内。

## 一个 WS 加密开关

Server 的 `settings.yaml` 增加：

```yaml
security:
  ws_encryption: true
  prepare_timeout: 30m
  run_timeout: 24h
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

Server 在创建设备或重置时生成独立的 Ed25519 密钥对，`setup_token` 就是 OpenSSH 私钥正文。Server 只持久化公钥指纹，私钥只通过 HTTPS 或本机管理响应交给使用者。使用者在可信的 Client 终端粘贴正文。安装脚本与命令行记录无需携带私钥。

```text
Client 导入 OpenSSH 私钥
  → 固定 Server CA 指纹，或通过已验证的 WSS 建立首次信任
  → 在安全通道内提交 CSR 和本次连接的签名
  → Server 核对签名及公钥指纹，签发当前代次的设备证书
  → Client 保存私钥与证书，建立已认证的设备连接
```

持有当前私钥可重新导入同一设备。领取私钥的响应只在创建或重置时返回，响应不缓存。重置替换公钥指纹并撤销已有证书。HTTP 地址的注册命令单独携带可信的 Server CA 指纹。Client 在发送签名前，通过指纹验证握手附带的 CA。HTTPS 地址也可通过验证外层证书建立首次信任。

Server 自己保存身份 CA 私钥、Server 通道证书及签名密钥；这些材料持久保存，不能每次启动重新生成。设备证书绑定 `device_id`、凭据代次和 Client 身份用途，由 CA 签发；初版使用标准 X.509 证书及 Ed25519 密钥。

设备私钥只在受信管理响应中交付，之后由 Client 保存。Server 用公钥指纹匹配设备，并保存证书序列号、有效期、凭据代次和撤销状态。认证连接同时检查证书链和当前设备授权。

`endpoints.yaml` 继续保存 Server 地址、设备 ID 和非秘密的证书文件引用；私钥与设备证书保存在该 endpoint 的独立私有目录，文件权限为 `0600`。endpointService 保存这些注册材料，runService 使用启动快照建立连接。续签原子替换证书材料，不改变 endpoint、conversation、session 或 run 身份。

证书有效期为 90 天，剩余 30 天时通过已认证 WS 申请续签，CSR 必须证明持有相应私钥。续签保留旧证书至其原始到期时间，避免续签回复丢失导致设备失联；重置仍撤销所有序列号。到期前无法续签时停止建立新连接；已有连接也不能无限越过证书有效期。证书已过期时可重新导入当前私钥，取得新证书。设备重置或删除后，旧私钥也无法再次注册。

重置或删除设备时，在同一事务撤销其证书、增加凭据代次并撤销全部短期授权，提交后关闭旧 WS。吊销和授权过期不伪造 run 的终态。Server 信任根更换需要旧可信身份授权的更新或用户重新安装可信注册材料，不从未认证 HTTP 响应静默替换信任根。

重置前已发出的目标授权保留旧代次，重新注册不会自动把它们提升到新代次；恢复这些目标的数据访问需要用户明确重新授权。恢复合法的执行事实补报与重新授予数据访问权限分开处理。

## WS 握手、加密和协议版本

目标设备协议使用 `AgentUse-Protocol: agentuse-client/3`。这只升级 Client–Server 通信；原生 `harnessctl/3`、accepted/run/input 的语义不变。实现时两端一起升级，旧 `/2` 明确返回版本不支持，不回退到旧 Bearer 认证。

1. Client 打开 `/api/client/ws`，在 Upgrade 及 URL 中不携带长期秘密或业务访问令牌。
2. Client 发随机挑战；Server 返回签名策略，覆盖 Client 挑战、Server 挑战、连接随机 ID、Server 身份、协议版本及 `ws_encryption`。Client 校验安装时固定的信任、签名与挑战匹配，防止重放旧的关闭策略。
3. 开启加密时，把 WS 二进制数据流包装为 `net.Conn`，使用 Go 标准库建立 TLS 1.3，验证固定的 Server CA、Server 身份与 ServerAuth 用途，以及设备证书的 ClientAuth 用途。仅首次注册允许无设备证书，但只能进入注册操作，不能心跳、领取消息或调用业务接口。
4. 关闭内层加密时，Client 在 WSS 内提交设备证书和私钥签名，签名覆盖本次双向挑战、连接 ID、规范化 Server 地址和协议策略；Server 验证签名、一次性挑战及设备当前状态。注册仍在已验证的 WSS 内完成。
5. 认证完成后才接收 heartbeat、登记设备在线或发送业务消息。挑战只使用一次，握手超时关闭连接；未认证连接设置数量、大小和速率上限。

双方挑战至少使用 32 字节安全随机数。握手签名采用固定域分隔和规范化字段编码，覆盖完整的策略及连接绑定；签名验证使用固定结构字段顺序的规范编码（当前为固定 Go struct 的 JSON），不对任意 map 或收到的原始 JSON 文本重新排序后猜测签名内容。Server 策略证书链也必须验证预期身份和签名用途。

内层 TLS 提供密钥协商、对称会话密钥、完整性及前向保密，不自制逐条消息的非对称加密协议。外层 WSS 的部署不需要向反向代理配置设备 mTLS；设备认证在 Server 内的专用连接完成。

策略握手占用明确的初始 WS 消息，完成后切换数据流，不能把缓冲的握手字节误读为 TLS 记录。业务编码统一为“4 字节大端长度 + UTF-8 JSON”，解密后的单条 JSON 上限沿用 8 MiB，先检查长度再分配。TLS 记录和 WS 帧均不作为业务消息边界；关闭 WS 压缩，保持单读者和串行写入。关闭内层加密时也沿用同一业务分帧，避免两份消息解析实现。

外层 WS 的地址、控制帧、长度和时序仍可见。握手失败不会降级；断线仍按现有持久收据恢复，不重新执行来源输入。

## HMAC 签发密钥与票据

Server 的 `access_signing_key` 是仅由 Server 持有的 HMAC 密钥，作用类似 JWT 使用 HS256 时的 `jwtKey`。使用至少 32 字节安全随机密钥，持久保存于 Server 私有文件，不能每次启动重新生成；多 Server 实例使用同一组密钥和 key ID。它与设备私钥、CA 私钥和 WS 会话密钥分开，不发送给 Client、不写入业务响应或日志。

Client 可以知道算法、读取票据中的公开字段，也可以计算 `SHA256(token)`。它无法在不知道签发密钥的情况下，为自行修改的设备、消息、版本或期限生成有效签名。绑定时由 Server 自行计算哈希，不能直接接受 Client 声明的 `access_token_hash`。

票据使用固定版本、key ID、规范化编码的载荷和 HMAC-SHA256，验证使用恒定时间比较，不接受 Client 自选签名算法。HMAC 提供真实性和完整性，不加密载荷；本稿采用类似 JWT 的签发机制，但不把准备期的截止时间混同于 JWT 的通用 `exp`。

```text
token = at1.<kid>.<base64url(payload)>.<base64url(HMAC-SHA256(ServerKey, signed_bytes))>
signed_bytes = 固定用途域分隔符 + 版本 + kid + 编码后的 payload
access_token_hash = SHA256(完整 token)
```

载荷固定绑定 `server_id`、`device_id`、`credential_generation`、`conversation_id`、`message_id`、`agent_id`、`agent_version_id`，以及 Server 生成的随机 nonce、`bind_before` 和 `access_limit`。`bind_before` 限制受理前的准备及首次绑定窗口，`access_limit` 是该次来源授权的绝对上限；真实 run_id 尚不存在，不提前填写。

首次派发时，将这些非秘密的签发参数固定在现有消息目标元数据中，签名完成后经已认证 WS 发出票据。重投递使用同一组参数和票据，不因为重连、重新签名或 Server 重启获得新的准备窗口。无需新建票据或授权表；HMAC 验证不依赖独立的票据登记记录，但仍须校验现有消息确实派发给该设备、目标未明确拒绝及凭据代次仍有效。

只有载荷和期限等非秘密参数保留在消息目标中，完整 token 与签名不写入 dispatch 快照、消息正文或公共事件。Client 在比较业务快照、保存收据和记录日志前移除临时凭据。签发密钥轮换保留仍需验证和恢复的旧 key ID，或由管理员明确撤销其对应授权；丢失密钥不能通过自动生成新密钥恢复旧权限。

## init：复用投递票据，不创建 run

init 初始化可复用的 Client session，不创建 runs，也不预分配原生 run_id。Server 在消息投递时下发上述票据，例如：

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
  "access": {"token": "at1.…", "bind_before": 1800000000}
}
```

准备阶段直接复用这张票据，不再签发一种独立的 init token。这里的“一次性”指只能完成一次 run 绑定，并非只能请求一个 HTTP 接口；一次 init 可以读取配置并下载多个 Skill 包。

在尚无该消息/Agent 的 run_inputs 关联、目标确已派发、未被明确拒绝且未超过 `bind_before` 时，票据只允许：

- 读取该目标固定的 Agent 配置及其明确引用的 Skill 元数据、ZIP，不能枚举或改读 latest。
- 读取本次初始化所需的来源 conversation 元数据，以及按真实 Client session 边界计算的 complete_messages。

准备票据不开放普通文件下载、任意历史读取、输出上传、发送消息、创建子对话或其他业务写入。消息附件仍只随投递携带元数据，执行期按需下载；准备失败按原有规则回报错误，不生成 failed run。

准备窗口初始建议为首次派发后 30 分钟，由 Server 策略配置；bind_before 还必须不晚于 access_limit，防止派生输入的准备阶段突破来源授权上限。用户发起的消息由 Server 在首次派发时固定 access_limit，设备派生的消息继承来源上限；重投递不重置这些时间。过期后不回退到长期设备凭据。对 Client 是否正在 init 不建立服务端阶段事件或状态机，只检查现有消息路由、票据期限及是否已受理。

## accepted：在 run 中保存哈希和截止时间

Client 从 harnessctl 公共 accepted 取得实际 run_id 后，在上报信封中带回票据。票据是临时传输字段，不修改原生 accepted JSON，也不写入 harnessctl 事件文件或 Server 的 run_events.payload。

Server 先核对 HMAC、来源设备和固定路由，再按既有 accepted 规则在同一事务内处理：

1. 核对来源已投递，保存或确认实际 run、run_inputs 和原始 accepted。消息/Agent 不得改绑其他 run，真实 run UUID 仍由适配器决定。
2. 如果是新 run，且票据仍符合首次绑定条件，由 Server 计算并保存 `runs.access_token_hash = SHA256(token)`；固定 `runs.access_expires_at = min(Server 首次绑定时间 + run 授权超时, 票据 access_limit)`。
3. 如果是已有 run 的补充输入，继续使用该 run 原有的 token、哈希和截止时间，不因为另一张消息票据更换授权，不给一个 run 保存多条访问凭据。
4. 提交后回复 accepted ACK，并通过同一已认证 WS 告知 Client 该 run 的访问凭据和实际截止时间。回滚时绑定及授权也一并回滚。

```text
消息投递 → HMAC 票据 → 受理前限定资源准备
                        ↓ accepted(实际 run_id, 票据)
              新建/核对 run + run_inputs
                        ↓ 同一事务首次绑定
              runs.access_token_hash
              runs.access_expires_at
                        ↓
              该 run 生命周期内复用同一 token
```

Server 对同一 `(message_id, agent_id)` 的受理检查和非空 token 哈希的唯一约束共同防止重复绑定。对于指向已有 run 的补充票据，其来源一旦进入 run_inputs，就不再具有准备期权限；执行期间只认该 run 已绑定的 token。

查询必须先判断来源是否已受理：一旦存在 run_inputs，便进入 run 校验，不论请求声称自己在 init，还是票据的 `bind_before` 尚未到期，都不能退回准备规则。原票据在首次绑定后可以继续用于 run，但此时授权期限以 runs.access_expires_at 为准，不能因为准备窗口已过而提前失效，也不能仅凭 HMAC 验证通过而跳过 run 状态检查。

合法执行事实与是否仍能授予数据访问分开。晚到的 accepted 即使已错过绑定窗口，仍按原有设备身份、派发关系及幂等规则保存实际执行事实，但不能自动建立或恢复 HTTP 访问授权；需要用户重新授权后发起新执行；当前版本没有给迟到 run 补发或延长授权的接口。重复 accepted 不重设哈希或截止时间，也不使终态 run 重新获得访问权。

## run 的有效期、结束与恢复

run 只维护一个访问令牌，直到 run 结束或授权截止，不再每 5 分钟签发、刷新或维护逐条消息的 grant。初始建议 run 授权超时为 24 小时，由 Server 设置，使用 Server 时钟；已受理排队的时间也计算在内。

| Server 可确认的情况 | 授权行为 |
|---|---|
| 已发送，尚未 accepted | 准备票据在 bind_before 内仅可读取初始化白名单 |
| 新 run 首次合法 accepted | 在同一事务保存 token 哈希和固定截止时间 |
| 同一 run 接受多条消息 | 复用 run 原凭据；每张补充票据的准备权限在其输入受理后结束 |
| running / waiting | 截止前继续使用原凭据，不需要周期续签 |
| result 原子提交 | 根据 run 终态拒绝后续访问，与结果及最终回复使用同一事务边界 |
| 过期或管理员主动撤销 | 拒绝 HTTP 数据访问，不伪造执行结果；主动撤销可将截止时间设为当前 Server 时间 |
| 断线、重启、重复 accepted | 不改变 run 截止时间、不重新执行输入、不恢复终态授权 |
| 设备重置、删除或凭据撤销 | 设备代次检查使旧票据和 run 授权失效，提交后断开旧连接 |

Server 不以上报 heartbeat、更新状态、换票据或绑定新消息作为延长截止时间的依据。恶意 Client 一直不报 result，也只能在固定期限内访问。当前版本不提供延长已有 run 授权期限的接口；设备自己无权延长。需要重新授权时由用户发起新的执行，不能自动恢复过期 run 的访问。

设备通过当前 run 授权派生的消息、子任务及自动父通知，应把来源授权上限固定到消息安全元数据中，新票据的 access_limit 不能超过它。只有用户的新授权可以开启更大的预算，不能通过给自己发消息或创建子任务无限延长访问；无需为此创建根授权表或根执行 ID。父 run 正常结束不提前取消仍获授权的子 run；子轮次触发父侧新投递时，仍受来源上限约束。

Client 丢失内存中的 token 时，可在已认证 WS 上请求恢复指定的 run。Server 校验其设备、原凭据代次、已存在的非空授权、run 状态和截止时间，再用首次绑定来源的固定签发参数重建相同票据并返回。HMAC 密钥和非秘密参数足以重建签名，不需要明文令牌表；以现存哈希核对重建结果，不能生成新票据替换绑定或顺带延长有效期。

恢复只服务仍获授权的实际 run，不能凭长期设备身份兑换任意历史任务权限。尚未 accepted 的目标仍按原消息重投递恢复同一准备票据；已终态、已过期、已撤销或从未取得有效绑定的 run 只恢复事实收据，不自动补发授权。

授权过期不阻止经设备 WS 补报合法 accepted、result 及其重放，否则会留下无法确认的执行事实。原有路由和事件去重继续生效；访问授权不引入新的 send、run、input、任务 ID 或业务重试入口。

## HTTP 接入和权限检查

设备 HTTP 请求携带 `Authorization: Bearer at1.…`。Server 校验签名、预期 Server 身份、设备当前代次及来源，查询现有 run_inputs 判断它处于准备期还是已绑定：

- 未受理：验证固定派发参数、bind_before 和初始化白名单，不因此新建 run。
- 已受理：必须匹配该 run 已保存的哈希、未终态状态及 access_expires_at；HMAC 签名有效本身不构成业务许可。

绑定后的权限按 run 现有的 device、conversation、Agent、固定版本、实际输入与来源关系计算，不复制成另一份授权表。请求中的 session/run、来源或目标路径仍须匹配；`X-Agentuse-*` 是待验证的声明，不是独立凭据。

票据载荷中的 message_id 在绑定后标记首次授权来源，不把一个多输入 run 限制为只能操作首条消息。请求指定其他输入消息时，通过 run_inputs 核对它确实属于同一 run；不能因为消息 ID 不同而切换回准备期校验，也不能跨到另一个 run。

| 能力 | run 许可范围 |
|---|---|
| 配置和 Skill | 固定 Agent 版本及其明确引用的 Skill 版本 |
| 对话与 complete_messages | 来源 conversation，沿用既有共享历史和 session 路由检查 |
| 文件列表与下载 | 来源 conversation 的文件空间；消息附件选择不是文件 ACL |
| 输出上传 | 归属该实际 run，run 活动且授权有效；继续禁止写 user_input/ |
| 嵌套对话与关联文件 | 按现有 collaborative、父子关系、明确目标和用户所有权规则校验 |

资源、上下文、conversation、文件和附件入口均执行相同的阶段和 run 校验，不保留“签名有效就直接放行”或“失败后退回 dk”的路径。不能用新消息的准备票据访问同一设备上其他活动 run。

浏览器和人类 SDK 继续使用用户身份，可以按用户权限读取已结束任务的文件。run 令牌不能调用管理、升级、用户和设备配置接口，也不能通过 X-Agentuse-User 取得用户全部权限。设备健康和证书续签通过已认证 WS 完成，长期身份不提供读取历史任务内容的 HTTP 后门。

授权终止后拒绝新请求；长文件流至少每秒复验状态和期限，失效后停止继续传输，已发送字节无法收回。上传在文件发布和业务提交前，在同一事务内复验，过期或撤销时清理候选文件。多 Server 实例以数据库和当前凭据代次为准，取消通知只是加速手段。

凭据不放入 URL、重定向、下载地址、环境变量或日志。HTTP 使用 http:// 时，票据正文和 Bearer 凭据仍可能被截获，并在其有效范围内重放；HMAC 不提供加密或请求持有者证明。需要保护这些请求的部署使用 HTTPS，不能把 ws_encryption=true 展示成所有 Client 通信均已加密。

HTTP 写操作继续遵守原有幂等或读后核对规则，不因认证失败、断线或恢复授权就盲目重放 POST。

## Client 与 context 的生命周期

设备私钥由 Client 守护进程保管，准备票据和 run token 只保存在 Client 内存。原生 Agent、harnessctl、模型提示、context.json、工作目录及公共事件不携带这些凭据。完整主机失陷不在这一隔离边界内；同用户且可任意读文件或调试进程的原生程序仍可能取得私钥，0600 和清空环境变量不能替代操作系统或执行沙箱隔离。

裸 context 的远端操作经既有 Client 进程的受限本地 IPC 代理，CLI 不自行读取设备凭据直连 Server。IPC 使用私有 Unix socket 或 Windows named pipe，不依赖可关闭的 Web UI，也不增加常驻进程。Client 校验受管 session 绑定和实际输入收据，不接受调用方指定任意票据或借用其他 run；纯本地 status、历史和缓存读取不依赖远端授权。

资源准备时使用本次消息的准备票据。原生执行时，通过适配器公共 status 确定实际 run，使用该 run 的唯一访问凭据；不能选最近投递、最近 accepted、启动时的消息或另一个排队 run 的 token。若 accepted 的 Server ACK 尚未返回，等待授权绑定完成后再请求远端业务数据，不回退到准备权限或长期凭据。

Client 观察到本地 result 后立即停止该 run 的远端访问，事件补报由设备 WS 独立完成。Server 在 result 提交后完成权威阻断。原生进程跨 run 复用时，context 重新选择实际 run 的凭据，不能沿用上一轮 token。

已结束 run 的本地状态、历史和已下载文件仍可读取，但不能凭旧 token 或长期设备身份再次拉取远端内容。固定 Skill 缓存无需因授权结束删除；缓存命中不代表获准发送新的远端请求。父 run 结束后不能沿用旧 token 轮询子任务；子轮次通知触发新的消息和实际 run，再按新授权读取结果。

## 服务归属与持久化

| 所属 | 职责 |
|---|---|
| deviceService | 注册、证书签发和续签、凭据代次及设备撤销 |
| conversationService | 固定派发来源及准备票据参数，校验初始化白名单、对话和文件范围；不预建 run |
| runService | accepted 事务内保存唯一 token 哈希及截止时间，验证实际归属、run 终态和超时 |
| hub / controller | 安全握手、携带临时票据的信封和 ACK、WS 恢复及 HTTP 身份解析 |
| Client endpointService / runService | 分别保存私有注册材料、管理安全连接与当前 run 凭据；harnessService 不解释证书或票据 |

HMAC 签发和验证、证书及通道封装放入无业务数据库依赖的公共工具包，不新增 securityService、grantService 或 Work 授权层。跨 owner 修改仍使用同一 `*gorm.DB` 事务，业务消息、ACK 和断开连接在提交后发送。

后续有序 GORM/gormigrate 迁移增加设备 credential_generation 和 device_certificates 元数据，覆盖 SQLite、MySQL、PostgreSQL，保持已发布迁移不变。run 授权只在现有 runs 上增加：

| 字段 | 用途 |
|---|---|
| access_token_hash | Server 验证票据后自行计算的 SHA256；可空，非空值唯一，不能由 Client 直接填写 |
| access_expires_at | Server 固定的访问截止时间；可空，首次绑定后不因 Client 活动或恢复而延长 |

accepted 之前没有 runs 行，也无需提前保存 hash。首次签发参数位于已有 messages.agent_targets，真实归属由 run_inputs/runs 查询，不新增 message_access_grants、init 表、票据表或逐条消息的授权状态机。run 哈希和首次绑定来源关联应保留，终态依现有 status 阻断访问，避免清空记录后误当成尚未绑定的准备票据。

hash 或截止时间为空表示没有 run 访问授权，不能因合法签名、重复 accepted 或重连自动补发。设备重置前来源参数的旧代次不被提升，重新注册也不自动恢复旧 run 权限；恢复执行事实与重新授予数据访问分开。

已注册的证书设备可继续连接。旧 `ar1.` 安装码随 `0004_device_public_key` 迁移停用，待注册设备需要重置并复制新私钥。旧 `dk_` 设备需要重新注册。两端协同升级，不保留 /2 设备 Bearer 或旧明文注册作为降级路径；旧 /api/client/register 交换流程退出设备注册路径。

## 开销与验证

TLS 和证书校验发生于连接建立，持续消息使用对称加密；长连接摊薄握手开销。已有 WSS 的部署可关闭内层 TLS，文件 HTTP 不因此经过 WS。每个 run 只保存一个哈希和截止时间，准备期使用 HMAC 和现有路由，省去授权表及周期续签查询；实际 CPU、握手和文件流校验负载仍需实测，本文不提供未经测量的性能承诺。

实现验收至少覆盖：

- 默认开启、on/off 与 ws/wss 四种组合；浏览器连接独立，握手错误不自动降级。
- 错误 Server 信任、篡改或重放策略、失效证书、设备重置及跨 Server 票据均失败。
- Client 可以计算 SHA256，却无法伪造 HMAC；修改设备、消息、版本或期限失败，Server 不信任 Client 上报的 hash。
- init 不建 run：同一票据在限定时间内可获取多个固定 Skill，只能访问准备白名单；重启、重投递不延长窗口。
- accepted 创建/核对 run、run_inputs 和绑定原子提交；重复受理不重置凭据与期限，同票据不能绑定两个 run。
- 一个 run 多输入复用唯一凭据；补充票据受理后不能继续使用准备权限，也不能替换 run 原凭据。
- 已绑定票据不受 bind_before 误伤，已终态票据不能趁准备窗口尚未到期退回 init；签名有效也必须核对 run 哈希和状态。
- 一条消息多个 Agent、已受理排队 run、进程跨 run 复用以及 context 等待 Server 绑定确认。
- 不上报 result 也会超时；派生消息和子任务不能突破来源上限，正常父任务结束不破坏仍合法的子任务。
- result/回复事务失败不提前终止授权；成功后阻断新请求，长流和并发上传观察终态或撤销。
- Server/Client 重启可恢复相同活动 run 凭据；过期、终态、撤销或空授权不复活，合法事实补报不被授权过期阻止。
- 所有设备 HTTP 入口移除长期身份旁路；本地缓存和人类读取维持各自规则。

相关设计见[协议](protocol.md) · [设备](server-device.md) · [Client endpoint](client-endpoint.md) · [Client context](client-context.md) · [run](server-run.md) · [数据库](server-database.md)。


## 实现与运维

Server 将 CA、通道证书和私钥写入 `~/.agentuse/security/identity.json`，将独立的 32 字节 HMAC 密钥写入同目录的 `access-signing.key`，均为私有文件。启动时复用现有材料；材料损坏时报错，不静默生成新信任根。多个 Server 实例必须使用相同材料及同一数据库；部署、备份和恢复时一起保留。更换 HMAC 密钥会使现有票据不能验证或恢复。

握手最多同时受理 64 个，令牌桶突发上限 128、每秒补充 16 个；策略、TLS、设备认证各有 15 秒期限，认证帧上限 64 KiB，业务帧上限 8 MiB。无认证握手不分配业务执行。Client 启动独立的私有 context IPC；Unix socket 位于当前用户私有临时目录，Windows 使用当前用户 ACL 的 named pipe。可关闭 Web UI，IPC 仍可用。每次代理请求由守护进程重新查询原生 status 并核对公开事件；显式指定历史或排队 run 不能冒充当前 run。

旧 `/api/client/register` 和 `/api/client/me` 返回 426；升级请求仅携带协议版本，设备证明在安全通道内完成。旧 `dk_` 不用于 WS 或业务 HTTP，使用旧 device_key 的 endpoints.yaml 需逐个用新设备私钥覆盖注册。现有 run 不回填访问凭据，历史文件由用户 API 读取。
