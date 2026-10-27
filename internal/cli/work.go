package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/memory-co/agentuse/internal/store"
)

const workUsage = `agentuse work —— 工作的协调者用来管理线程(docs/v2/designs/work.md)

用法:
  agentuse work agents                            能用哪些 agent
  agentuse work threads                           这个工作的线程和状态
  agentuse work new --agent <agent_id> "要做的事"   开线程，打印线程 id
  agentuse work send <线程 id> "补充的话"           给线程发消息(它在跑就是插话)
  agentuse work read <线程 id>                     看线程的消息
  agentuse work resolve <线程 id>                  标记线程已完成

读环境变量 AGENTUSE_SERVER 和 AGENTUSE_WORK_TOKEN,daemon 起协调者时会设好。
`

func runWork(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, workUsage)
		return errUsage
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
		if len(rest) != 2 {
			return usageErr("用法: agentuse work send <线程 id> \"消息\"")
		}
		return workSend(ctx, c, rest[0], rest[1])
	case "read":
		if len(rest) != 1 {
			return usageErr("用法: agentuse work read <线程 id>")
		}
		return workRead(ctx, c, rest[0])
	case "resolve":
		if len(rest) != 1 {
			return usageErr("用法: agentuse work resolve <线程 id>")
		}
		var ss store.Session
		if err := c.do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(rest[0])+"/resolve", nil, &ss); err != nil {
			return err
		}
		fmt.Printf("线程 %s 已标记完成\n", ss.ID)
		return nil
	}
	return usageErr("未知命令 agentuse work %s\n\n%s", args[0], workUsage)
}

func workClient() (*apiClient, error) {
	server, token := os.Getenv("AGENTUSE_SERVER"), os.Getenv("AGENTUSE_WORK_TOKEN")
	if server == "" || token == "" {
		return nil, errors.New("没有 AGENTUSE_SERVER / AGENTUSE_WORK_TOKEN:这个命令只给工作的协调者在跑的时候用")
	}
	return &apiClient{server: strings.TrimRight(server, "/"), token: token,
		unauthorized: "工作令牌无效或已过期(只在协调者这条消息还在跑时有效)"}, nil
}

func workAgents(ctx context.Context, c *apiClient) error {
	var agents []store.Agent
	if err := c.do(ctx, http.MethodGet, "/api/agents", nil, &agents); err != nil {
		return err
	}
	rows := [][]string{{"agent_id", "名字", "harness", "说明"}}
	for _, a := range agents {
		rows = append(rows, []string{a.ID, a.Name, a.Harness, a.Description})
	}
	printTable(os.Stdout, rows)
	return nil
}

func workThreads(ctx context.Context, c *apiClient) error {
	var list []store.Session
	if err := c.do(ctx, http.MethodGet, "/api/sessions", nil, &list); err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("还没有线程")
		return nil
	}
	rows := [][]string{{"线程 id", "状态", "agent", "标题"}}
	for _, s := range list {
		rows = append(rows, []string{s.ID, s.Status, s.AgentID, s.Title})
	}
	printTable(os.Stdout, rows)
	return nil
}

func workNew(ctx context.Context, c *apiClient, args []string) error {
	fs := flag.NewFlagSet("agentuse work new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agent := fs.String("agent", "", "用哪个 agent(agentuse work agents 里的 agent_id)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *agent == "" || fs.NArg() != 1 {
		return usageErr("用法: agentuse work new --agent <agent_id> \"要做的事\"")
	}
	var res struct {
		Session store.Session `json:"session"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/sessions", map[string]string{"agent_id": *agent, "prompt": fs.Arg(0)}, &res); err != nil {
		return err
	}
	fmt.Printf("已开线程 %s。它回完会把结果发给你，不用等。\n", res.Session.ID)
	return nil
}

func workSend(ctx context.Context, c *apiClient, id, prompt string) error {
	var res struct {
		Status string `json:"status"` // 插话才有
	}
	if err := c.do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(id)+"/messages", map[string]string{"prompt": prompt}, &res); err != nil {
		return err
	}
	if res.Status != "" {
		fmt.Printf("线程 %s 还在跑，已作为插话送过去\n", id)
	} else {
		fmt.Printf("已发给线程 %s\n", id)
	}
	return nil
}

func workRead(ctx context.Context, c *apiClient, id string) error {
	var res struct {
		Session  store.Session   `json:"session"`
		Messages []store.Message `json:"messages"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id), nil, &res); err != nil {
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
		if m.Role == store.RoleUserMsg {
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
