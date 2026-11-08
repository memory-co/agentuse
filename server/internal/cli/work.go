package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/memory-co/agentuse/internal/command"
)

const workUsage = `agentuse-server work —— 通过 Server API 管理跨设备的工作线程

用法:
  agentuse-server work agents
  agentuse-server work threads --work WORK_ID
  agentuse-server work new --work WORK_ID [--agents IDS] [--users IDS] "消息"
  agentuse-server work new --work WORK_ID --defer-start
  agentuse-server work send --work WORK_ID [--agents IDS] [--users IDS] THREAD_ID "消息"
  agentuse-server work read --work WORK_ID THREAD_ID
  agentuse-server work resolve --work WORK_ID THREAD_ID

使用 --server / --token / --config、AGENTUSE_SERVER / AGENTUSE_TOKEN 或 login 配置。
用户只能管理有权限的工作。所有命令通过 HTTP 执行，不直接读写数据库。
`

func runWork(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(workUsage)
		return nil
	}
	action := args[0]
	switch action {
	case "agents", "threads", "new", "send", "read", "resolve":
	default:
		return command.UsageErr("未知 work 命令 %s", action)
	}
	fs, h := clientFlags("agentuse-server work " + action)
	work := fs.String("work", "", "Server Work ID")
	agents := fs.String("agents", "", "逗号分隔的 Agent 收件人")
	users := fs.String("users", "", "逗号分隔的用户收件人")
	deferred := fs.Bool("defer-start", false, "只创建空线程")
	if err := command.Parse(fs.FlagSet, args[1:]); err != nil {
		return err
	}
	count := 0
	if action == "new" && !*deferred || action == "read" || action == "resolve" {
		count = 1
	}
	if action == "send" {
		count = 2
	}
	if fs.NArg() != count || (action != "agents" && *work == "") || (*deferred && (action != "new" || *agents != "" || *users != "")) || ((action != "new" && action != "send") && (*agents != "" || *users != "")) {
		return command.UsageErr("参数错误，见 agentuse-server work help")
	}
	client, err := h.client()
	if err != nil {
		return err
	}
	method, path := http.MethodGet, "/api/agents"
	var body any
	if action != "agents" {
		path = "/api/works/" + url.PathEscape(*work) + "/threads"
		switch action {
		case "read", "resolve", "send":
			path += "/" + url.PathEscape(fs.Arg(0))
			if action == "resolve" {
				method = http.MethodPost
				path += "/resolve"
			}
			if action == "send" {
				method = http.MethodPost
				path += "/messages"
			}
		case "new":
			method = http.MethodPost
		}
		if action == "new" || action == "send" {
			payload := map[string]any{}
			if !*deferred {
				prompt := fs.Arg(count - 1)
				payload = map[string]any{"prompt": prompt, "to_agent_ids": workTargets(*agents), "to_user_ids": workTargets(*users)}
			}
			body = payload
		}
	}
	ctx, stop := command.SignalContext()
	defer stop()
	var result any
	if err = client.Request(ctx, "", method, path, body, &result); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func workTargets(value string) []string {
	result := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
