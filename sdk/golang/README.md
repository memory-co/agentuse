# agentuse(Go)

agentuse 的 Go 客户端，只用标准库。总体说明见 [sdk](../README.md)。

```bash
go get github.com/memory-co/agentuse/sdk/golang
```

```go
import agentuse "github.com/memory-co/agentuse/sdk/golang"

c := agentuse.New("http://agentuse.internal:8080", os.Getenv("AGENTUSE_TOKEN")) // admin 令牌，全局一个
uid := "u_10086"                                                                 // 网站自己的 user id,每次调用直接传

// 连接我的电脑：把命令给用户复制
d, err := c.CreateDaemon(ctx, uid, "my-laptop")
fmt.Println(d.SetupCommand)

// 电脑上线后打开 claude
_, err = c.SetDaemonHarness(ctx, uid, d.ID, "claude", true)

// 建一个 agent,运行时是这台电脑上的 claude;以后 UpdateAgent 切换运行时,agent 不变
a, err := c.CreateAgent(ctx, uid, agentuse.CreateAgentRequest{ID: "code-helper", Name: "写代码", DaemonID: d.ID, Harness: "claude"})

// 用这个 agent 发起任务，等它回完(用 ctx 控制超时)
r, err := c.CreateSession(ctx, uid, agentuse.CreateSessionRequest{AgentID: a.ID, Prompt: "修一下 README 的错别字"})
wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
detail, err := c.WaitSession(wctx, uid, r.Session.ID, 2*time.Second)
fmt.Println(string(detail.Messages[len(detail.Messages)-1].Content))

// harness 向用户提问时会话是 waiting,WaitSession 也会返回;回答后它在同一轮里接着做
if detail.Session.Status == "waiting" {
	q := detail.Questions[len(detail.Questions)-1]
	_, err = c.AnswerQuestion(ctx, uid, r.Session.ID, q.ID, map[string]string{q.Questions[0].Question: q.Questions[0].Options[0].Label})
}

// 接着回复;harness 还在跑时是插话(reply.Steer),会在它下一步之前送达
reply, err := c.SendMessage(ctx, uid, r.Session.ID, agentuse.MessageRequest{Prompt: "再把 CHANGELOG 也改一下"})
if err == nil && reply.Steer != nil {
	fmt.Println("插话:", reply.Steer.Status)
}
```

类型和接口里的对象一一对应(见 `types.go`),消息内容和回报的 `payload` 是 `json.RawMessage`。要自定义超时、代理就设 `c.HTTPClient`。

## 附件

先用 `CreateSessionRequest{AgentID: "coder", DeferStart: true}` 建空会话，上传文件后 `SendMessage` 开始。附件是 session 的独立资源，大小限制和权限见 [attachments API](../../docs/v2/apis/attachments.md)。

```go
f, err := os.Open("input.csv")
if err != nil { return err }
defer f.Close()
a, err := c.UploadAttachment(ctx, user, sessionID, "input.csv", f)
if err != nil { return err }
r, err := c.DownloadAttachment(ctx, user, sessionID, a.ID)
if err != nil { return err }
defer r.Close()
_, err = io.Copy(destination, r)
```

`ListAttachments(ctx, user, sessionID, limit, cursor)` 返回 `AttachmentPage`，用 `NextCursor` 继续取下一页；limit 为 0 时使用服务端默认值。另有 `GetAttachment`、`DeleteAttachment`。上传流由调用者关闭，下载流必须消费或关闭。下载响应头在 `Header` 中。
