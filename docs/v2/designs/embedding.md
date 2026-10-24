# 嵌入到网站

agentuse 可以嵌进任何网站，给网站的用户提供「用自己电脑上的 agent 干活」的能力。它的定位像 **Redis**:网站自己部署一个 agentuse-server,只有网站的**后端**调用它，网站的用户不直接接触 agentuse。

```
 网站用户的浏览器                网站后端                        agentuse-server
┌──────────────┐  网站自己的   ┌──────────────┐  服务密钥 +      ┌──────────────┐
│  网站的页面  │ ────────────▶ │  网站的接口  │ ───────────────▶ │  /api/*      │
│ (网站自己画) │  登录 / 接口  │  鉴权、转发  │  X-Agentuse-User │  (内网)      │
└──────────────┘               └──────────────┘                  └──────┬───────┘
                                                                        │ WebSocket
 网站用户的电脑  ── curl setup.sh / 下载二进制 / daemon 长连接 ─────────┘ (公网)
```

## 和 Redis 的类比

| | Redis | agentuse |
|---|---|---|
| 部署 | 网站自己起一个实例 | 网站自己起一个 `agentuse-server`,一个 SQLite 文件 |
| 谁调用 | 只有后端 | 只有后端，用一个服务密钥 |
| 用户 | 不知道用户是谁，key 由后端拼 | 后端每次带上**网站自己的 user id** |
| 多个网站 | 各起各的实例 | 各起各的实例，互不相干，不做多租户 |

## 用户：直接用网站的 user id

- agentuse 不管登录。网站后端认好用户之后，调 agentuse 时带上这个用户在网站里的 id,agentuse 就把它当成那个用户。
- [users](../tables/users.md) 的 `id` 就是网站的 user id(如 `u_10086`)。daemon、会话、自定义 agent 都挂在它下面，网站不用另存映射表。
- 用户第一次出现时自动创建(`role = user`,`name` 取 id),网站不用提前同步用户。删用户、改名都不需要通知 agentuse。

## 认证：服务密钥 + 代为某个用户

网站后端拿 admin 的令牌当**服务密钥**,每个请求带两个头:

```
Authorization: Bearer ut_…(admin 令牌)
X-Agentuse-User: u_10086
```

- 带了 `X-Agentuse-User`:按这个用户的身份执行，权限和普通用户完全一样(只能看到他自己的 daemon、会话)。用户不存在就先建。
- 不带：就是 admin 本身，用来做管理(登记内置 agent、看全局)。
- 只有 admin 令牌能用这个头，普通用户令牌带了返回 403。
- 服务密钥只放在网站后端，不能下发到浏览器。

## 网站要做的事

网站用自己的页面和接口包一层，后端把请求转给 agentuse 的 [接口](../apis/README.md):

| 网站上的功能 | 后端调用(带 `X-Agentuse-User`) |
|---|---|
| 「连接我的电脑」 | [`POST /api/daemons`](../apis/daemons.md),把返回的安装命令展示给用户复制 |
| 看电脑在不在线、开关 agent | `GET /api/daemons`、`PUT /api/daemons/{id}/agents/{agent}` |
| 发起一次任务 | [`POST /api/sessions`](../apis/sessions.md) |
| 接着回复 | `POST /api/sessions/{id}/messages` |
| 看进度和结果 | `GET /api/sessions/{id}`,轮询 |

现成的客户端见 [sdk/](../../../sdk/README.md)(Python / Node.js / Go),封装了服务密钥、`X-Agentuse-User` 和上面这些接口。

用户复制的安装命令里是 agentuse-server 的公网地址，用户电脑直接连它，不经过网站后端。

TODO:进度现在只能轮询，以后加增量拉取(`after=<event id>`)和回调(webhook),让网站后端不用一直轮询。

## 网络

agentuse-server 的地址分两类，部署时用反向代理分开:

| 路径 | 谁访问 | 暴露到 |
|---|---|---|
| `/api/*`(除下面的) | 网站后端 | 只在内网 |
| `/setup.sh`、`/download/*`、`/api/daemon/*`(含 WebSocket) | 网站用户的电脑 | 公网 |

`agentuse-server start` 的 `-public-url` 填公网那个地址，安装命令里用的就是它。

## 自带网页

[总体设计](main.md#网页)里的网页照常可用，嵌入时当作**运维后台**给网站自己的管理员用(登 admin 令牌),网站的用户看不到它。
