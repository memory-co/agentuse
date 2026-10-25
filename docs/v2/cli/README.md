# CLI

命令行拆成两个二进制，给不同的人用:

| 二进制 | 给谁 | 跑在哪 | 干什么 |
|---|---|---|---|
| [`agentuse`](agentuse.md) | 普通用户 | 用户自己的电脑 | 注册本机、跑 daemon、看本机状态 |
| [`agentuse-server`](agentuse-server.md) | 部署 / 运维服务的人 | 服务器(`start`);管理命令哪都能跑 | 起服务;通过 HTTP API 管用户 |

为什么拆:

- 用户电脑上只需要 daemon 那部分，不该带着建用户、开服务这些命令。
- 两边的配置和权限完全不同(daemon 密钥 vs 管理员令牌),放一个二进制里命令容易混。

两个 CLI 都只通过 HTTP / WebSocket 和服务打交道;只有 `agentuse-server start` 这个服务进程本身读写数据库。

## 构建与分发

```
cmd/agentuse/         → agentuse
cmd/agentuse-server/  → agentuse-server
```

- 发布:`v*` tag 触发 release,产出 `agentuse-{linux,darwin}-{amd64,arm64}`、`agentuse-server-{linux,darwin}-{amd64,arm64}`、`SHA256SUMS`。仓库根的 `install.sh` 从最新 release 装 `agentuse-server`,并把各平台 `agentuse` 放到 `~/.agentuse/dist`(`-dist` 的默认值)。
- `make build`:两个都编到 `bin/`。
- `make dist`:只交叉编译 `agentuse`(linux / darwin × amd64 / arm64)到 `dist/`,`agentuse-server` 通过 `/download/agentuse-<os>-<arch>` 提供给 `setup.sh` 下载，见 [apis/setup](../apis/setup.md)。
- 拆开后，服务不能再「拿自己的可执行文件当 daemon 给」,所以必须有 `dist/`。`agentuse-server start` 启动时检查 `-dist` 目录，没有对应平台的二进制就打一条警告。

## 约定

- 子命令风格：`<二进制> <名词> <动词>` 或单个动词，如 `agentuse-server user create`、`agentuse register`。
- 参数用 `-flag value`(也接受 `--flag`),位置参数只放主对象(如用户名)。
- 输出给人看的中文;加 `-json` 时输出 JSON,方便脚本。
- 成功退出码 0,参数错误 2,其他错误 1;错误信息以 `错误:` 开头写到 stderr。
- 都有 `version` 和 `help`。
