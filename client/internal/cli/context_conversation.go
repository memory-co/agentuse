package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/internal/command"
	sdk "github.com/memory-co/agentuse/sdk/golang"
)

const conversationContextUsage = `context conversation —— 协调当前 conversation 的子会话

协作模式可用:
  context conversation agents
  context conversation create [--title TITLE] [--mode single] [--agents IDS] [--users IDS] [MESSAGE]
  context conversation list
  context conversation read CHILD_ID [--round N]
  context conversation send CHILD_ID --agents IDS MESSAGE
  context conversation wait CHILD_ID [--round N] [--timeout 5m]

子会话每轮全部执行结束后由 Server 自动通知协调者。wait 可被新输入或原生提问中断。
`
const childConversationUsage = `当前为单体子会话，直接执行任务，不能再创建会话。
需要澄清或遇到阻碍时，在最终答复中说明并正常结束本轮。
Server 会在整轮执行结束后通知协调者；保留 status 和 attachment 执行工具。
`
const singleConversationUsage = "当前为单体模式，直接完成任务；不提供 conversation 拆分命令。\n"

func contextConversationHelp(ctx context.Context, path string) string {
	c, err := runservice.OpenConversationContext(ctx, path)
	if err != nil {
		return conversationContextUsage
	}
	d, err := c.ConversationInfo(ctx)
	if err != nil {
		return singleConversationUsage
	}
	if d.Conversation.ParentConversationID != "" {
		return childConversationUsage
	}
	if d.Conversation.Mode != "collaborative" {
		return singleConversationUsage
	}
	return conversationContextUsage
}

func runContextConversation(ctx context.Context, path, run string, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, contextConversationHelp(ctx, path))
		return nil
	}
	op := args[0]
	fs := command.NewFlags("context conversation " + op)
	title, mode, agents, users, timeout := "", "single", "", "", 5*time.Minute
	var round int64
	switch op {
	case "agents", "list":
	case "read":
		fs.Int64Var(&round, "round", 0, "指定子会话轮次")
	case "create":
		fs.StringVar(&title, "title", "", "子会话标题")
		fs.StringVar(&mode, "mode", "single", "子会话模式：single")
		fs.StringVar(&agents, "agents", "", "Agent ID，逗号分隔")
		fs.StringVar(&users, "users", "", "用户 ID，逗号分隔")
	case "send":
		fs.StringVar(&agents, "agents", "", "Agent ID，逗号分隔")
		fs.StringVar(&users, "users", "", "用户 ID，逗号分隔")
	case "wait":
		fs.Int64Var(&round, "round", 0, "固定等待轮次，省略时选择开始等待时的最新轮次")
		fs.DurationVar(&timeout, "timeout", 5*time.Minute, "最长等待时间")
	default:
		return command.UsageErr("未知 conversation 命令 %s", op)
	}
	// Flags may follow a child ID; -- preserves text that begins with a dash.
	flags, values := []string{}, []string{}
	for i := 1; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			values = append(values, args[i+1:]...)
			break
		}
		if strings.HasPrefix(v, "-") {
			flags = append(flags, v)
			if !strings.Contains(v, "=") && v != "--help" && v != "-h" {
				i++
				if i == len(args) {
					return command.UsageErr("%s 缺少值", v)
				}
				flags = append(flags, args[i])
			}
		} else {
			values = append(values, v)
		}
	}
	fs.Usage = func() { fmt.Fprint(os.Stderr, contextConversationHelp(ctx, path)) }
	if err := command.Parse(fs, flags); err != nil {
		return err
	}
	if (op == "agents" || op == "list") && len(values) != 0 {
		return command.UsageErr("%s 不接受位置参数", op)
	}
	if (op == "read" || op == "wait") && len(values) != 1 {
		return command.UsageErr("%s 需要 CHILD_ID", op)
	}
	if op == "send" && len(values) < 2 {
		return command.UsageErr("send CHILD_ID --agents IDS MESSAGE")
	}
	roundSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "round" {
			roundSet = true
		}
	})
	if roundSet && round <= 0 {
		return command.UsageErr("--round 必须为正整数")
	}
	c, err := runservice.OpenExecutionContext(ctx, path, run)
	if err != nil {
		return err
	}
	client, err := c.ConversationClient(ctx)
	if err != nil {
		return err
	}
	d, err := client.GetConversation(ctx, "", c.ConversationID)
	if err != nil {
		return err
	}
	if d.Conversation.Mode != "collaborative" || d.Conversation.ParentConversationID != "" {
		return fmt.Errorf("只有协作模式可管理直接子会话；子会话正常结束本轮后会自动通知协调者")
	}
	ids := func(s string) []string {
		out := []string{}
		for _, id := range strings.Split(s, ",") {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, id)
			}
		}
		return out
	}
	var result any
	switch op {
	case "agents":
		result, err = client.ListAgents(ctx, "")
	case "list":
		result, err = client.ListChildren(ctx, "", c.ConversationID)
	case "create":
		result, err = client.CreateConversation(ctx, "", sdk.CreateConversationRequest{Mode: mode, ParentConversationID: c.ConversationID, Title: title, Prompt: strings.Join(values, " "), ToAgentIDs: ids(agents), ToUserIDs: ids(users)})
	case "read":
		if round > 0 {
			result, err = client.GetConversationRound(ctx, "", values[0], round)
		} else {
			result, err = client.GetConversation(ctx, "", values[0])
		}
	case "send":
		result, err = client.SendMessage(ctx, "", values[0], sdk.MessageRequest{Prompt: strings.Join(values[1:], " "), ToAgentIDs: ids(agents), ToUserIDs: ids(users)})
	case "wait":
		result, err = c.WaitChild(ctx, client, values[0], round, timeout)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
