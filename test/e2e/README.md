# e2e 测试

单元测试跟着代码放在各自的包里(`foo.go` 旁边是 `foo_test.go`),`make test` / `go test -short ./...` 秒级跑完。
这里是 e2e:**先编出和发布一样的二进制，再从外面驱动它们**,一个场景一个包，可以单独跑。

```
test/e2e/
├── e2eutil/      共用工具：编二进制、起 agentuse-server、跑子进程、等条件
├── fakeharness/  自研 harness,一个可执行文件扮演 自定义 / claude / codex 三种 harness
├── install/      安装场景
└── session/      普通会话场景
```

## 跑

```bash
make e2e                 # 全部场景
make e2e E2E=install     # 只跑一个
go test -count=1 -v ./test/e2e/session/
```

`-short` 时全部跳过，所以 `go test -short ./...` 不会编二进制。需要 Go、bash、curl,只在 linux / macOS 上跑。

## 场景

### install:安装

照 README 的一行命令 `curl -fsSL …/install.sh | bash`,从「GitHub Release」装 agentuse-server,起服务，再在另一个 HOME(另一台用户电脑)上跑页面给的安装命令接入设备。

- GitHub 由 `install/testdata/curl`(假 curl)在本地扮演:Release 里放这次编出来的二进制和 SHA256SUMS,不碰外网;访问本机服务的请求交给真 curl。
- `install/testdata/systemctl` 假装没有 systemd,setup.sh 走 nohup 分支，不往测试机上装服务;setup.sh 这一步只在 linux 上跑。
- 还测了校验和对不上、版本不存在时报错退出。

### session:普通会话

编好的 agentuse-server + `agentuse daemon` + fakeharness,用 Go SDK(admin 令牌 + 用户 id)走一遍：添加设备 → `agentuse register` → 上报 harness → 建 agent → 开会话 → 续接 → 失败 → 别人用你的 agent → daemon 断线重连 → 删除设备。

fakeharness 在 daemon.json 里同时配成三个 harness:

| harness | 怎么跑 | 测的是 |
|---|---|---|
| `fake`(自定义) | stdin 读 `{session_id,resume,input}`,stdout 写 progress / result | 自定义 harness 的约定 |
| `claude` | `-p … --output-format stream-json [--resume id]` | `harness/claude/harness.yaml` 的参数和输出规则 |
| `codex` | `exec --json … [resume id] prompt` | `harness/codex/harness.yaml` 的参数和输出规则 |

回复里带上 prompt、续接的会话 id 和工作目录名，测试据此确认 daemon 传对了参数、在对的目录里跑;prompt 为 `fail` 时按各自格式报失败。

## 加一个场景

在 `test/e2e/` 下建一个包，`TestMain` 里调用 `e2eutil.Main(m)`,每个测试第一行 `e2eutil.Skip(t)`,用 `e2eutil.Binary(t, "agentuse-server")` 拿二进制。再把场景名加进 `.github/workflows/test.yml` 的 e2e matrix。
