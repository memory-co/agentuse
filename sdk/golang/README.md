# agentuse(Go)

agentuse 的 Go 客户端，只用标准库。总体说明见 [sdk](../README.md)。

```bash
go get github.com/memory-co/agentuse/sdk/golang
```

```go
import agentuse "github.com/memory-co/agentuse/sdk/golang"

c := agentuse.New("http://agentuse.internal:8080", os.Getenv("AGENTUSE_TOKEN")) // admin 令牌
u := c.AsUser("u_10086")                                                         // 网站自己的 user id

// 连接我的电脑：把命令给用户复制
d, err := u.CreateDaemon(ctx, "my-laptop")
fmt.Println(d.SetupCommand)

// 电脑上线后打开 claude
_, err = u.SetDaemonAgent(ctx, d.ID, "claude", true)

// 发起任务，等它回完(用 ctx 控制超时)
r, err := u.CreateSession(ctx, agentuse.CreateSessionRequest{Agent: "claude", Prompt: "修一下 README 的错别字", Cwd: "~/code/x"})
wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
detail, err := u.WaitSession(wctx, r.Session.ID, 2*time.Second)
fmt.Println(string(detail.Messages[len(detail.Messages)-1].Content))

// 接着回复
_, err = u.SendMessage(ctx, r.Session.ID, agentuse.MessageRequest{Prompt: "再把 CHANGELOG 也改一下"})
if agentuse.IsStatus(err, 409) {
	// agent 还没回完
}
```

类型和接口里的对象一一对应(见 `types.go`),消息内容和回报的 `payload` 是 `json.RawMessage`。要自定义超时、代理就设 `c.HTTPClient`。
