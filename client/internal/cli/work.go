package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/memory-co/agentuse/internal/command"
	"net/http"
	"net/url"
	"os"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

const workUsage = `agentuse work —— 工作的协调者用来管理线程(docs/v2/designs/work.md)

用法:
  agentuse work agents                            能用哪些 agent
  agentuse work threads                           这个工作的线程和状态
  agentuse work new --agents <agent_ids> "要做的事"   开线程，打印线程 id
  agentuse work new --agents <agent_ids> --defer-start  先建空线程，上传附件后再 send
  agentuse work send --agents <agent_ids> <线程 id> "补充的话"           给线程发消息(它在跑就是插话)
  agentuse work read <线程 id>                     看线程的消息
  agentuse work resolve <线程 id>                  标记线程已完成

读环境变量 AGENTUSE_SERVER 和 AGENTUSE_WORK(工作令牌),daemon 起协调者时会设好。
`

func runWork(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, workUsage)
		return command.ErrUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(workUsage)
		return nil
	}
	c, err := workClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	rest := args[1:]
	switch args[0] {
	case "agents":
		return workAgents(ctx, c)
	case "threads":
		return workThreads(ctx, c)
	case "new":
		return workNew(ctx, c, rest)
	case "send":
		return workSendTargets(ctx, c, rest)
	case "read":
		if len(rest) != 1 {
			return command.UsageErr("用法: agentuse work read <线程 id>")
		}
		return workRead(ctx, c, rest[0])
	case "resolve":
		if len(rest) != 1 {
			return command.UsageErr("用法: agentuse work resolve <线程 id>")
		}
		var ss agentuse.Session
		if err := c.Request(ctx, "", http.MethodPost, "/api/sessions/"+url.PathEscape(rest[0])+"/resolve", nil, &ss); err != nil {
			return err
		}
		fmt.Printf("线程 %s 已标记完成\n", ss.ID)
		return nil
	}
	return command.UsageErr("未知命令 agentuse work %s\n\n%s", args[0], workUsage)
}

func workClient() (*agentuse.Client, error) {
	server, token := os.Getenv("AGENTUSE_SERVER"), os.Getenv("AGENTUSE_WORK")
	if server == "" || token == "" {
		return nil, errors.New("没有 AGENTUSE_SERVER / AGENTUSE_WORK:这个命令只给工作的协调者在跑的时候用")
	}
	return &agentuse.Client{BaseURL: strings.TrimRight(server, "/"), Token: token}, nil
}

func workAgents(ctx context.Context, c *agentuse.Client) error {
	var agents []agentuse.Agent
	if err := c.Request(ctx, "", http.MethodGet, "/api/agents", nil, &agents); err != nil {
		return err
	}
	rows := [][]string{{"agent_id", "名字", "harness", "说明"}}
	for _, a := range agents {
		rows = append(rows, []string{a.ID, a.Name, a.Harness, a.Description})
	}
	command.PrintTable(os.Stdout, rows)
	return nil
}

func workThreads(ctx context.Context, c *agentuse.Client) error {
	var list []agentuse.Session
	if err := c.Request(ctx, "", http.MethodGet, "/api/sessions", nil, &list); err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("还没有线程")
		return nil
	}
	rows := [][]string{{"线程 id", "状态", "标题"}}
	for _, s := range list {
		rows = append(rows, []string{s.ID, s.Status, s.Title})
	}
	command.PrintTable(os.Stdout, rows)
	return nil
}

func workNew(ctx context.Context, c *agentuse.Client, args []string) error {
	fs := flag.NewFlagSet("agentuse work new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agents := fs.String("agents", "", "目标 agent，逗号分隔")
	deferStart := fs.Bool("defer-start", false, "先建空线程，上传附件后再发消息")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	if (*agents == "" && !*deferStart) || (!*deferStart && fs.NArg() != 1) || (*deferStart && fs.NArg() != 0) {
		return command.UsageErr("用法: agentuse work new --agents <agent_ids> \"要做的事\" | --defer-start")
	}
	var res struct {
		Session agentuse.Session `json:"session"`
	}
	request := map[string]any{}
	if !*deferStart {
		request["prompt"] = fs.Arg(0)
		request["target_agent_ids"] = splitAgents(*agents)
	}
	if err := c.Request(ctx, "", http.MethodPost, "/api/sessions", request, &res); err != nil {
		return err
	}
	if *deferStart {
		fmt.Printf("已创建空线程 %s。先用 agentuse attachment upload --session %s 上传，再用 agentuse work send --agents <目标> 开始。\n", res.Session.ID, res.Session.ID)
		return nil
	}
	fmt.Printf("已开线程 %s。它回完会把结果发给你，不用等。\n", res.Session.ID)
	return nil
}

func workRead(ctx context.Context, c *agentuse.Client, id string) error {
	var res struct {
		Session  agentuse.Session   `json:"session"`
		Messages []agentuse.Message `json:"messages"`
	}
	if err := c.Request(ctx, "", http.MethodGet, "/api/sessions/"+url.PathEscape(id), nil, &res); err != nil {
		return err
	}
	fmt.Printf("线程 %s「%s」 状态: %s\n", res.Session.ID, res.Session.Title, res.Session.Status)
	for _, m := range res.Messages {
		var v struct {
			Prompt string `json:"prompt"`
			OK     bool   `json:"ok"`
			Output string `json:"output"`
			Error  string `json:"error"`
		}
		_ = json.Unmarshal(m.Content, &v)
		if m.Role == "user" {
			text := v.Prompt
			if text == "" {
				text = string(m.Content)
			}
			fmt.Printf("\n## 发给它\n%s\n", text)
			continue
		}
		if v.OK {
			fmt.Printf("\n## 它回复\n%s\n", v.Output)
		} else {
			fmt.Printf("\n## 它失败了\n%s\n%s\n", v.Error, v.Output)
		}
	}
	return nil
}

func workSendTargets(ctx context.Context, c *agentuse.Client, args []string) error {
	fs := command.NewFlags("agentuse work send")
	agents := fs.String("agents", "", "目标 agent")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 2 || *agents == "" {
		return command.UsageErr("work send --agents coder,reviewer <session> \"消息\"")
	}
	var out any
	if err := c.Request(ctx, "", "POST", "/api/sessions/"+url.PathEscape(fs.Arg(0))+"/messages", map[string]any{"prompt": fs.Arg(1), "target_agent_ids": splitAgents(*agents)}, &out); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
