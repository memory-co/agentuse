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

// 建一个 agent,运行时是这台电脑上的 claude;以后 PublishAgentVersion 发布完整新配置，agent 身份不变
a, err := c.CreateAgent(ctx, uid, agentuse.CreateAgentRequest{ID: "code-helper", Name: "写代码", DaemonID: d.ID, Harness: "claude"})

// 用这个 agent 发起任务，等它回完(用 ctx 控制超时)
r, err := c.CreateSession(ctx, uid, agentuse.CreateSessionRequest{ToAgentIDs: []string{a.ID}, Prompt: "修一下 README 的错别字"})
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

先用 `CreateSessionRequest{DeferStart: true}` 建空会话，上传文件后 `SendMessage` 开始。附件是 session 的独立资源，大小限制和权限见 [attachments API](../../designs/v2/apis/attachments.md)。

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

## 多 agent

```go
ss, err := c.CreateSession(ctx, user, agentuse.CreateSessionRequest{Mode: "multi_agent"})
if err != nil { return err }
reply, err := c.SendMessage(ctx, user, ss.Session.ID, agentuse.MessageRequest{
    Prompt: "分别看看实现与风险", ToAgentIDs: []string{"coder", "reviewer"},
})
if err != nil { return err }
detail, err := c.WaitMessageRuns(ctx, user, ss.Session.ID, reply.Message.ID, time.Second)
```

Reply.Deliveries 是各目标输入分配，accepted 前 Reply.Runs 可以为空。实际 UUID 及插话归属由 client 决定。另有 GetRun、WaitRun、CancelRun、ReplyRunRequest。新模式 WaitSession 默认等全部执行终态，可传 WaitOptions{ReturnOnWaiting:true}。

消息使用 `to_agent_ids` 和 `to_user_ids`，可同时指定；返回值始终包含这两组数组。只接受新字段，不保留旧字段别名。

```go
// 只发给已有会话读取权限的用户，不启动 Agent。
r, err := c.SendMessage(ctx, uid, sessionID, agentuse.MessageRequest{
    Prompt: "请查看结果", ToUserIDs: []string{uid},
})
```

`ListRunAttachments(ctx, user, sessionID, runID, limit, cursor)` 和 `AllRunAttachments(ctx, user, sessionID, runID)` 只读取该 run 上传到会话的输出文件。设备密钥上传必须设置 `Client.RunID`，Server 校验 run 属于该设备、会话且仍在活动中；用户通过 API 上传输入无需 run ID。执行中的 AI 使用 [context 文件命令](../../docs/client/context.md)，无需自行配置 SDK。
