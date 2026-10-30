package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/daemon"
	"github.com/memory-co/agentuse/internal/store"
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
func runSession(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("agentuse session send <session> --agents coder,reviewer \"消息\" | read [session] | runs [session] | wait [session] | cancel <session> <run>")
		return nil
	}
	action := args[0]
	fs := newFlags("agentuse session " + action)
	server := fs.String("server", os.Getenv("AGENTUSE_SERVER"), "服务地址")
	token := fs.String("token", os.Getenv("AGENTUSE_TOKEN"), "用户或工作令牌")
	config := fs.String("config", envOr("AGENTUSE_CONFIG", daemon.DefaultConfigPath()), "daemon 配置")
	session := fs.String("session", os.Getenv("AGENTUSE_SESSION"), "当前 session")
	agents := fs.String("agents", "", "逗号分隔的 @ 目标")
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
					return usageErr("参数缺少值")
				}
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, v)
		}
	}
	if err := parse(fs, flags); err != nil {
		return err
	}
	if *token == "" {
		*token = envOr("AGENTUSE_WORK", os.Getenv("AGENTUSE_WORK_TOKEN"))
	}
	if *server == "" || *token == "" {
		cfg, e := daemon.LoadConfig(*config)
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
	c := &apiClient{server: strings.TrimRight(*server, "/"), token: *token}
	if strings.HasPrefix(*token, "dk_") {
		c.runID = os.Getenv("AGENTUSE_RUN")
		c.attemptID = os.Getenv("AGENTUSE_ATTEMPT")
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
		return usageErr("需要 session id 和对应命令参数")
	}
	ctx, stop := signalContext()
	defer stop()
	base := "/api/sessions/" + url.PathEscape(*session)
	switch action {
	case "send":
		var reply any
		if err := c.do(ctx, "POST", base+"/messages", map[string]any{"prompt": pos[0], "target_agent_ids": splitAgents(*agents)}, &reply); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(reply)
	case "read", "runs", "wait":
		deadline := time.Now().Add(*timeout)
		for {
			var detail struct {
				Session  store.Session   `json:"session"`
				Messages []store.Message `json:"messages"`
				Runs     []store.Run     `json:"runs"`
			}
			if err := c.do(ctx, "GET", base, nil, &detail); err != nil {
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
					fmt.Printf("%s\t@%s\t%s\n", r.ID, r.AgentKey, r.Status)
				}
				return nil
			}
			for _, m := range detail.Messages {
				who := m.Role
				if m.AgentKey != "" {
					who = "@" + m.AgentKey
				}
				fmt.Printf("[%d %s] %s\n", m.ID, who, m.Content)
			}
			return nil
		}
	case "cancel":
		var r store.Run
		if err := c.do(ctx, "POST", base+"/runs/"+url.PathEscape(pos[0])+"/cancel", nil, &r); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(r)
	default:
		return usageErr("未知 session 子命令 %s", action)
	}
}
