package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	services "github.com/memory-co/agentuse/client/internal/service"
	endpointservice "github.com/memory-co/agentuse/client/internal/service/endpoint"
	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/internal/command"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func splitAgents(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func runConversation(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("agentuse conversation send <conversation> [--agents coder,reviewer] [--users user_ids] \"消息\" | read [conversation] | runs [conversation] | wait [conversation] | cancel <conversation> <run>")
		return nil
	}
	action := args[0]
	fs := command.NewFlags("agentuse conversation " + action)
	server := fs.String("server", os.Getenv("AGENTUSE_SERVER"), "服务地址")
	token := fs.String("token", os.Getenv("AGENTUSE_TOKEN"), "API 令牌")
	config := fs.String("config", command.EnvOr("AGENTUSE_CONFIG", endpointservice.DefaultPath()), "daemon 配置")
	session := fs.String("conversation", os.Getenv("AGENTUSE_CONVERSATION"), "Server conversation ID")
	agents := fs.String("agents", "", "逗号分隔的 Agent 收件人 ID")
	users := fs.String("users", "", "逗号分隔的用户收件人 ID")
	asJSON := fs.Bool("json", false, "JSON 输出")
	timeout := fs.Duration("timeout", 30*time.Minute, "等待上限")
	var flags, pos []string
	for i := 1; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(v, "-") {
			flags = append(flags, v)
			key := strings.TrimLeft(strings.SplitN(v, "=", 2)[0], "-")
			if !strings.Contains(v, "=") && key != "json" && key != "h" && key != "help" {
				i++
				if i >= len(args) {
					return command.UsageErr("参数缺少值")
				}
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, v)
		}
	}
	if err := command.Parse(fs, flags); err != nil {
		return err
	}
	if os.Getenv("AGENTUSE_ENDPOINT") == endpointservice.LocalID && *server == "" {
		return errors.New("本地模式没有 server；请在本地调试页面查看执行和 workspace")
	}
	if *server == "" || *token == "" {
		var cfg runservice.Config
		var e error
		if *server != "" {
			cfg, e = services.LoadConfigForServer(*config, *server)
		} else {
			cfg, e = services.LoadConfig(*config)
		}
		if e != nil {
			return e
		}
		if *server == "" {
			*server = cfg.Server
		}
		if *token == "" {
			*token = cfg.Key
		}
	}
	if *server == "" || *token == "" {
		return errors.New("缺少服务地址或凭据")
	}
	c := &agentuse.Client{BaseURL: strings.TrimRight(*server, "/"), Token: *token}
	if strings.HasPrefix(*token, "dk_") {
		c.RunID = os.Getenv("AGENTUSE_RUN")
	}
	need := 0
	if action == "send" || action == "cancel" {
		need = 1
	}
	if len(pos) > need {
		*session = pos[0]
		pos = pos[1:]
	}
	if *session == "" || len(pos) != need {
		return command.UsageErr("需要 conversation id 和对应命令参数")
	}
	ctx, stop := command.SignalContext()
	defer stop()
	base := "/api/sessions/" + url.PathEscape(*session)
	switch action {
	case "send":
		var reply any
		if err := c.Request(ctx, "", "POST", base+"/messages", map[string]any{"prompt": pos[0], "to_agent_ids": splitAgents(*agents), "to_user_ids": splitAgents(*users)}, &reply); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(reply)
	case "read", "runs", "wait":
		deadline := time.Now().Add(*timeout)
		for {
			var detail struct {
				Session  agentuse.Session   `json:"session"`
				Messages []agentuse.Message `json:"messages"`
				Runs     []agentuse.Run     `json:"runs"`
			}
			if err := c.Request(ctx, "", "GET", base, nil, &detail); err != nil {
				return err
			}
			if action == "wait" && detail.Session.Status != "idle" && detail.Session.Status != "resolved" {
				if time.Now().After(deadline) {
					return errors.New("等待超时")
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
				continue
			}
			if *asJSON || action == "wait" {
				return json.NewEncoder(os.Stdout).Encode(detail)
			}
			if action == "runs" {
				for _, r := range detail.Runs {
					fmt.Printf("%s\t@%s\t%s\n", r.ID, r.AgentID, r.Status)
				}
				return nil
			}
			for _, m := range detail.Messages {
				who := m.Role
				if m.AgentKey != "" {
					who = "@" + m.AgentKey
				}
				fmt.Printf("[%s %s] %s\n", m.ID, who, m.Content)
			}
			return nil
		}
	case "cancel":
		var r agentuse.Run
		if err := c.Request(ctx, "", "POST", base+"/runs/"+url.PathEscape(pos[0])+"/cancel", nil, &r); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(r)
	default:
		return command.UsageErr("未知 session 子命令 %s", action)
	}
}
