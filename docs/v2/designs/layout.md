# 仓库目录整理方案

> 状态：已按六步完成实现。参考：[Go 大工程的代码目录分析](https://twwy.net/blog/go-project-layout/)。

## 一、结论

client / server 两个顶层目录，下面都按 **cli / frontend / controller / service / store** 组织，两边结构一致：

```
server/  cli  frontend  controller  service  store  (+ hub)
client/  cli  frontend  controller  service  store
```

几条约定：

1. **用 `internal/` 让编译器守住 client / server 的边界。** 目录分成 `server/internal/…` 和 `client/internal/…` 后，client 想 import server 的代码，编译直接失败。两边共用的东西（daemon ⇄ 服务的协议、harness.yaml）留在仓库根的 `internal/`，HTTP API 的类型统一用 `sdk/golang`。
2. **两边都要有 store 层。** server 的 store 是 SQLite 主库；client 的 store 是本机账本，记录这台机器收到过的命令和 token 用量。
3. **同一个词，两边的角色要写清楚：**

| 层 | server | client |
|---|---|---|
| cli | `agentuse-server` 的命令 | `agentuse` 的命令 |
| frontend | 管理网页 | 本机只读页面：命令清单、token 统计 |
| controller | HTTP API，可读可写 | 本机 HTTP，**只有 GET** |
| service | 业务编排，**由 controller 调用** | 常驻主循环：连服务端、领活、跑 harness、汇报、写账本，**自己运行，不被 controller 调用** |
| store | SQLite 主库 | SQLite 本机账本，分成 Writer（给 service）和 Reader（给 controller） |

client 的 service 和 server 的 service 不一样：它是现在的 `internal/daemon` 整包平移过来的，`agentuse daemon` 命令跑的就是它。读代码时记住一点就够了：**client 的 controller 不调用 service，两者只通过 store 交换数据。**

## 二、现状和问题

```
cmd/agentuse/            cmd/agentuse-server/       两个 10 行的 main
internal/cli/            两个二进制的命令行混在一个包里
internal/server/         HTTP 路由 + handler + WebSocket hub + setup.sh + 托管网页
internal/store/          SQLite，同时承担了大量业务（run 领取 / 租约 / 重排、work、配额）
internal/daemon/         连接循环、配置、harness 探测与执行、codex、插话、提问
internal/protocol/       daemon ⇄ 服务的消息
internal/attachment/     附件类型 + 又一个 HTTP 客户端
harness/                 内置 harness.yaml + 解析（server 和 daemon 都用）
frontend/                React 网页，embed 进 server
sdk/golang/ …            对外 SDK，也是一套 HTTP 客户端和 API 类型
```

当前所有代码都在同一个 `internal/` 下，编译器拦不住任何跨端引用。已经出现的问题：

| 问题 | 位置 |
|---|---|
| 用户电脑上的 CLI 依赖服务端存储层：`agentuse session/work` 用 `store.Session`、`store.Run` 解析 HTTP 响应 | `internal/cli/session.go`、`work.go`、`client.go` |
| 两个二进制的命令放在一个包里，`agentuse` 也会链接 `server`、`store` | `internal/cli/` |
| 同一套 HTTP API 有三个客户端：`cli.apiClient`、`attachment.Client`、`sdk/golang.Client` | |
| 业务逻辑一部分在 `server`（handler 里），一部分在 `store`（事务里），没有固定归属 | `server/runs.go`、`store/run_lifecycle.go`、`store/run_work.go` |
| `server` 包同时是 HTTP 层和 daemon 长连接层 | `server/hub.go` |
| 本机没有任何执行记录：run 结束后只剩会话目录，token 用量只上报了 `cost_usd`，`Dispatch` 里也没有发起人 | `internal/daemon/harnesses.go` 的 `Result`、`internal/protocol` 的 `Dispatch` |

## 三、目标结构

```
agentuse/
├── go.mod                      仍是单 module（理由见第六节）
├── server/                     ── agentuse-server，服务端
│   ├── cmd/agentuse-server/    main.go，只调 cli.Main
│   ├── internal/
│   │   ├── cli/                start / login / user；管理命令通过 sdk/golang 调 HTTP API
│   │   ├── controller/         HTTP 路由、鉴权中间件、handler、setup.sh / download、托管网页
│   │   ├── hub/                daemon 的 WebSocket 长连接：握手、收发、派活、叫醒
│   │   ├── service/            业务编排：session 消息与 @、run 生命周期、work、daemon 注册、附件配额
│   │   └── store/              SQLite 主库：建表、查询、事务原语
│   └── frontend/               管理网页（React + Vite），embed.go 把 dist/ 嵌进二进制
├── client/                     ── agentuse，用户电脑
│   ├── cmd/agentuse/           main.go，只调 cli.Main
│   ├── internal/
│   │   ├── cli/                register / daemon / status / harnesses / ui / session / work / attachment
│   │   ├── controller/         本机只读 HTTP：只有 GET，只拿得到 store.Reader
│   │   ├── service/            常驻主循环（现在的 internal/daemon 整包平移）：daemon.json、连接与重连、
│   │   │                       收 dispatch、准备会话目录和附件、跑 harness、插话、提问、汇报、对账、写账本
│   │   └── store/              本机账本 ~/.agentuse/ledger.db：Writer（给 service）、Reader（给 controller）
│   └── frontend/               本机只读页面：命令清单、token 统计；embed 进 agentuse
├── internal/                   ── client 和 server 共用，对仓库外不公开
│   ├── protocol/               daemon ⇄ 服务的注册接口和 WebSocket 消息
│   ├── harness/                内置 harness 描述（claude/、codex/ 的 harness.yaml）+ Spec 解析
│   ├── attachment/             附件文件名、ID 校验（去掉其中的 HTTP 客户端）
│   ├── command/                两个 CLI 共用的子命令分发、flag、表格 / JSON 输出
│   └── version/                Version，构建时用 -ldflags 注入
├── sdk/                        golang / nodejs / python，对外 SDK；两个 CLI 也用 sdk/golang
├── test/e2e/                   e2e 场景（需要两个二进制，所以留在顶层）
├── docs/
├── install.sh  Makefile  README.md  LICENSE
```

说明：

- cli 留在各自一侧，不提到顶层：两个 CLI 只有一半命令是调 sdk 的（server 的 login / user，client 的 session / work / attachment）。另一半直接用本端内部代码：`start` 要打开 store、起 controller 和 hub；`daemon` 要起 service 和本机页面；`register` / `status` / `harnesses` / `ui` 要读写 daemon.json、探测本机 harness。连调 sdk 的那几个 client 命令也和本机绑在一起：session / attachment 没传参数时从 daemon.json 读服务地址和密钥，work 读的是 service 起 harness 时设好的环境变量。cli 放到顶层的话就 import 不了 `server/internal`、`client/internal`，只能把这些 internal 去掉，client / server 的编译期边界也就没了。
- `server/frontend`、`client/frontend` 都是 npm 工程加一个 embed 包，不放进 internal。
- `server/internal/hub` 是 server 独有的一层：daemon 的 WebSocket 长连接是第二种入口，和 HTTP controller 平级。
- client 的 service 先保持一个包，不往下拆。以后如果它变得太大（比如再加几种 harness），优先在包内按文件整理；真要拆，再在 `service/` 下建子包（比如 `service/harness`），不新增顶层的层。
- Go 社区更常见的叫法是 `handler`（对应 `net/http.Handler`）。这里统一用 `controller`，两边一致即可。

### 术语

client 上「服务」这个词容易混，文档和代码注释里统一这样叫：

| 叫法 | 指什么 |
|---|---|
| service、主循环 | `client/internal/service` 这个包，由 `agentuse daemon` 启动 |
| 后台服务 | `setup.sh` 把 `agentuse daemon` 装成的 launchd / systemd 服务 |
| 本机页面 | `client/internal/controller` + `client/frontend` 提供的只读网页 |
| daemon | 服务端视角下的「一台用户电脑」，对应 `daemons` 表和 `agentuse daemon` 命令 |

## 四、本机页面

### 4.1 数据从哪来

**本机自己记，不去问服务端。** 用户要的是「这台机器实际接收过什么」，这份记录应该由这台机器自己保存：服务端的数据可能被删除，也可能不完整（比如派过来但被本机拒掉的命令），断网时也能看。

```
服务端 ──run──▶ service（收到时写一行；跑 harness；结束时回填状态、token、耗时）
                  │
                  ▼ store.Writer
            ~/.agentuse/ledger.db
                  ▲ store.Reader（只读连接）
                  │
            controller（只有 GET）◀── 浏览器
```

账本第一版只建一张 `runs` 表（以 `run_id + attempt_id` 为键）：

| 字段 | 来源 |
|---|---|
| 收到时间、结束时间、状态（running / done / failed / canceled / rejected）、错误 | service |
| 服务地址、daemon_id | daemon.json |
| 发起人（用户 id / 名字）、agent、session、work、harness | `protocol.Dispatch`（**需要扩展**，见 4.4） |
| prompt 摘要（默认前 200 字）、插话次数、附件数 | `Dispatch` / `Steer` |
| input / output / cache_read / cache_write tokens、cost_usd | service 从 harness 输出解析（**需要扩展**，见 4.4） |

完整 prompt 默认不存。它本来就在会话目录里，账本只保留能索引到那里的信息。另外保留天数可以在 daemon.json 里配置，默认 90 天。

### 4.2 「只读」怎么保证

三道防线，前两道由编译器和数据库保证，不依赖开发者自觉：

1. **类型层**：client 的 store 对外分成 `Writer` 和 `Reader` 两个接口。`controller.New` 只接受 `store.Reader`，controller 包里拿不到任何写方法。
2. **数据库层**：Reader 用一条单独的连接打开 `file:ledger.db?mode=ro`，并设置 `PRAGMA query_only=1`。即使有人在 Reader 里写了 UPDATE 也会执行失败。账本开 WAL，service 写的时候读不会被阻塞。
3. **HTTP 层**：只注册 `GET` 路由，其他方法一律 405；不返回任何 CORS 头。

### 4.3 进程和安全

- **跑在 `agentuse daemon` 进程里。** 这个进程已经被后台服务托管、一直在线，用户随时打开就能看，不用再管一个进程。`cli daemon` 启动时同时起 service 和 controller，两者共用一个 store 实例，但分别只拿 Writer 和 Reader。daemon.json 里新增 `ui_addr`，默认 `127.0.0.1:7801`，设为空就关掉本机页面。
- **只监听回环地址**，并校验 `Host` 头只能是 `127.0.0.1:<port>` / `localhost:<port>`，防止 DNS rebinding 让外部网页读到数据。
- **访问令牌**：多用户机器上，其他系统账号也能连本机回环端口，而账本里有 prompt 摘要。启动时在 `~/.agentuse/ui.token`（权限 0600）生成一个令牌，`agentuse ui` 打印（或直接打开）带令牌的链接；浏览器第一次访问后写入 cookie。做法和 Jupyter 一样。
- `agentuse status` 增加一行，显示本机页面的地址。

### 4.4 需要配合改动的地方

| 改动 | 位置 | 说明 |
|---|---|---|
| `Dispatch` 增加发起人和 work 信息 | `internal/protocol`、`server/internal/service` | 现在只有 `agent_id`、`session_id`，回答不了「来自谁」。增加 `sender`（id、name）、`work_id`。只增加字段，旧 daemon 会忽略 |
| `Result` 增加 `usage` | `client/internal/service` | Claude 从 stream-json 的 `result.usage` 取，Codex 从 app-server 的 token usage 通知取；自定义 harness 可以在结果里自己带 `usage`，不带就留空 |
| `done` / `failed` 的 payload 带上 usage | `internal/protocol` | 结果本来就会上报，顺带让服务端也拿到 token 数，以后服务端想做统计不用再改协议 |

### 4.5 本机网页

`client/frontend` 是一个独立的 Vite 工程，技术栈和 `server/frontend` 相同（React + shadcn/ui），第一版只有两页：

- **命令清单**：按时间倒序，按发起人 / agent / 状态 / 时间过滤，点开看详情；有会话目录时显示路径。
- **统计**：按天、按发起人、按 agent、按 harness 汇总 token 和次数。

两个前端先各自独立。等真正重复的组件多起来，再用 npm workspaces 抽出一个共用包，不提前抽。

## 五、依赖规则

```
server/cmd ──▶ server/internal/cli ──▶ controller, hub ──▶ service ──▶ store
                                           │                              ▲
                                           └──── 简单读：可以直接查 ───────┘

client/cmd ──▶ client/internal/cli ──┬──▶ service ────▶ store.Writer
                                     └──▶ controller ─▶ store.Reader

server/**, client/** ──▶ internal/*, sdk/golang
```

1. **client 和 server 互不 import。** Go 的 internal 规则会保证这一点，不需要靠 code review。
2. **根 `internal/*` 不 import client 或 server。** 它只放跨进程的约定，不放业务。不叫 `common`、`util`、`shared`，每个包按「提供什么」命名。
3. **server 内部单向依赖**：controller / hub → service → store；store 不 import 上层。写操作和跨表编排必须经过 service；纯读的接口（列表、详情）允许 controller 直接查 store，这是参考文中 multica 读路径的写法，不用为了分层再加一层转发。
4. **client 的 controller 和 service 互不 import**：两者唯一的交集是 store，而且一个只拿 Reader、一个只拿 Writer。装配在 `cli daemon` 里完成。这和第 3 条是同一个原则：client 的 controller 只读，所以永远走「直接查 store」那条路。
5. **hub 和 service 的双向联系用接口解开**：server 的 service 在新消息、取消、插话时要叫醒 daemon，就在 service 里定义 `Notifier` 接口，由 hub 实现，在 `cli start` 里装配。
6. **HTTP API 的类型只有一份，就是 `sdk/golang`**：两个 CLI 删掉自己的 apiClient，改用 `sdk/golang.Client`。这样 SDK 自己也一直有人在用，接口变了第一时间就会发现。

## 六、为什么仍用单 module

文中 etcd 用 go.work 把 14 个 module 串起来，前提是这些 module 各自独立发版、依赖轻重差别很大。agentuse 现在的情况：

- Go 代码约 1 万行，client 和 server 一起发版；README 里也写了「服务端和 daemon 要一起升级」。
- 两边的第三方依赖基本相同：client 的 store 也用 SQLite（同一个纯 Go 驱动 `modernc.org/sqlite`，`CGO_ENABLED=0` 交叉编译不受影响），拆 module 不能减少任何依赖。

所以保持单 module。以后如果 `sdk/golang` 要单独打版本号给外部用户 `go get`，再给它加一个 go.mod，那是第一个值得拆出去的 module。

> 账本没有选 JSONL 追加文件：写起来更简单，但按发起人、按天聚合，以及分页、保留期清理都要自己实现，也做不到数据库层的只读。现在的 `agentuse` 本来就因为链接了 server 的 store 而带着 SQLite（约 11 MB），所以用 SQLite 不会让二进制变大。

## 七、文件去向

### server

| 现在 | 去向 |
|---|---|
| `cmd/agentuse-server/main.go` | `server/cmd/agentuse-server/main.go` |
| `internal/cli/server.go` | `server/internal/cli/`（start、bootstrapAdmin、user） |
| `internal/cli/client.go` | `server/internal/cli/`（只保留 login 和 server-cli.json，apiClient 换成 sdk/golang） |
| `internal/server/server.go` | `server/internal/controller/`（routes、writeJSON / readJSON、storeErr） |
| `internal/server/api.go` | `server/internal/controller/`（鉴权、act-as、各 handler）；其中的业务判断移到 service |
| `internal/server/runs.go`、`attachments.go` | `server/internal/controller/`（HTTP 部分）+ `service/`（附件配额、清理、创建 session 的编排） |
| `internal/server/setup.go`、`web.go` | `server/internal/controller/` |
| `internal/server/hub.go` | `server/internal/hub/` |
| `internal/store/run_lifecycle.go`、`run_work.go` | 业务规则（领取、租约、结束、重排、对账、work 令牌）移到 `service/`；SQL 和事务原语留在 `store/` |
| `internal/store/*` 其余 | `server/internal/store/` |
| `frontend/` | `server/frontend/` |

### client

| 现在 | 去向 |
|---|---|
| `cmd/agentuse/main.go` | `client/cmd/agentuse/main.go` |
| `internal/cli/agentuse.go`、`session.go`、`work.go`、`attachment.go` | `client/internal/cli/`；`store.*` 类型和 `attachment.Client` 换成 sdk/golang |
| `internal/daemon/*`（全部文件，包括 config.go） | `client/internal/service/`，包名 `daemon` 改为 `service` |
| —— | 新建 `client/internal/store/`、`client/internal/controller/`、`client/frontend/` |

### 共用

| 现在 | 去向 |
|---|---|
| `internal/protocol/` | 不动 |
| `harness/` | `internal/harness/`（yaml 跟着走，`//go:embed` 是相对路径，不受影响） |
| `internal/attachment/types.go` | 不动；`client.go` 删掉 |
| `internal/cli/common.go` | `internal/command/`（dispatch、flag、表格）；`Version` 移到 `internal/version/` |

## 八、迁移步骤

每一步单独一个 PR，`make test`、`make e2e` 和前端构建都要通过。

1. **先消除跨端依赖**（不移动目录）
   - client 侧 CLI 改用 `sdk/golang`，删掉对 `store` 的引用和 `attachment.Client`；server 侧管理命令同样改用 `sdk/golang`。
   - `Version` 移到 `internal/version`，Makefile 和 e2e 里的 `-X` 路径一起改。
   - 这一步不做，后面一移目录就会编译失败；先做也方便单独 review。
2. **拆 cli**：`internal/cli` 拆成 client、server 两部分，共用部分放进 `internal/command`。
3. **移动目录**（纯 `git mv` 加改 import，不改逻辑）
   - `cmd/*` → `{client,server}/cmd/`；`internal/{server,store}` → `server/internal/`；`internal/daemon` → `client/internal/service`；`frontend/` → `server/frontend/`；`harness/` → `internal/harness/`。
   - 同步修改：Makefile（`./cmd/…`、`frontend` 路径）、`.github/workflows/*`（`cache-dependency-path`、`working-directory`）、`test/e2e/e2eutil`（二进制的包路径）、README 的「布局」一节、docs 里指向代码的链接。
   - `install.sh` 和 release 产物的文件名不变，用户这边感知不到。
4. **server 拆层**：hub 独立成包；server 包改名为 controller；定义 `service.Notifier`。
5. **本机页面**（依赖第 3 步）
   - 协议：`Dispatch` 增加 sender、work_id；`Result` 增加 usage；client 的 service 解析 Claude、Codex 的 token 用量。
   - 新建 `client/internal/store`（Writer / Reader、保留期清理），service 在收到和结束时写账本。
   - 新建 `client/internal/controller`、`client/frontend`、`agentuse ui`；Makefile 的 `web`、`dist` 和 CI 都要先构建 `client/frontend`，因为 `agentuse` 也要 embed 网页。
   - e2e 用 fakeharness 跑一次 run，断言本机页面的 API 能查到这条记录和 token 数，并断言非 GET 请求返回 405。
6. **逐步抽出 server 的 service**：按 run 生命周期 → session 消息与 @ → work → daemon 注册 → 附件配额的顺序，一次搬一块。store 现在是单连接加 `BEGIN IMMEDIATE`，很多规则写在事务里，搬的方式是：store 提供 `Tx(ctx, func(q) error)` 和细粒度查询，事务边界由 service 决定。

第 1–3 步是机械改动，风险低，可以很快合入。第 4、5、6 步互不依赖，可以并行。

## 九、待确认

- [x] 共用代码放根 `internal/`（对外不公开），还是放 `pkg/`（像 multica 那样表示「跨进程约定」，但对外公开）。方案倾向 `internal/`：能不公开就不公开。
- [x] 本机页面的默认端口（暂定 7801）、是否默认开启（暂定开启，只监听 127.0.0.1）。
- [x] 账本存不存完整 prompt：暂定只存前 200 字摘要，完整内容去会话目录看。
- [x] 「来自哪」要细到什么程度：暂定为服务地址 + 发起人 + agent / session / work。如果一台机器以后要连多个服务，账本已经按服务地址区分，不需要再改表。
- [x] 两个前端什么时候抽共用组件包：暂定等重复明显以后再说。

实现采用根 `internal/`、默认开启的回环端口 7801、200 字摘要和 90 天保留期。两个前端保持独立；本机发起人字段表示会话创建者。SQL、schema 和行解码保留在 server store，事务编排、通知与附件文件生命周期由 service 负责。
