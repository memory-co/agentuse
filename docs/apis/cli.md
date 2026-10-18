# CLI

一个二进制 `agentuse`,三类使用者。

## 实例主人

```bash
agentuse hub start [-config ~/.agentuse/hub.json]
```

按 [hub.json](../structs/filesystem.md#hubjson) 起 hub。Ctrl-C 优雅退出。

## 用户电脑

```bash
agentuse daemon start [-config ~/.agentuse/daemon.json]
```

按 [daemon.json](../structs/filesystem.md#daemonjson) 连 hub,领取运行,驱动 agent。

## agent(运行里)

这些命令只在 daemon 启动的 agent 里能用,读 `AGENTUSE_URL` / `AGENTUSE_TOKEN`:

| 命令 | 等价于 | 说明 |
|---|---|---|
| `agentuse reply "<文本>"` | `POST /v1/run/reply` | 回复评论;文本为 `-` 时从 stdin 读 |
| `agentuse status <状态>` | `POST /v1/run/status` | 改线程对象的状态,如 `in_review` |
| `agentuse activity <type> "<文本>"` | `POST /v1/run/activity` | 报进度 |
| `agentuse run` | `GET /v1/run` | 打印这次运行的令牌范围 |

## 其它

```bash
agentuse version
agentuse help
```

退出码:0 成功,1 出错(错误打到 stderr),2 用法不对。
