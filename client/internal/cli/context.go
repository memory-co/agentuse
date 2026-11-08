package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/internal/command"
)

const contextUsage = `context —— 当前 run 的输入与文件

用法:
  context input                 读取已受理的输入，包括补充消息
  context attachment list        列出当前输入和本次 run 上传的输出文件
  context attachment read FILE_ID 校验并取得文件的本地路径和 MIME 类型
  context attachment upload FILE  上传输出文件，自动关联当前 conversation/run
  context --help                查看可用能力
`
const humanContextUsage = `agentuse context —— 访问或进入本机执行上下文

用法:
  agentuse context [--path CONTEXT_JSON] [--run RUN_UUID] input
  agentuse context [--path CONTEXT_JSON] [--run RUN_UUID] attachment list
  agentuse context [--path CONTEXT_JSON] [--run RUN_UUID] attachment read FILE_ID
  agentuse context [--path CONTEXT_JSON] [--run RUN_UUID] attachment upload FILE
  agentuse context [--path CONTEXT_JSON] [--run RUN_UUID] enter

--path 默认读取 AGENTUSE_CONTEXT；未指定 run 时由原生适配器的 status 确认当前执行。
历史 run 需要显式 --run。enter 打开已绑定该 run 的 shell，里面可直接使用 context。
`

func ContextMain(args []string) int  { return command.ExitCode(contextCommand(args, true)) }
func runContext(args []string) error { return contextCommand(args, false) }
func contextCommand(args []string, bound bool) error {
	usage := humanContextUsage
	if bound {
		usage = contextUsage
	}
	fs := command.NewFlags("agentuse context")
	path := os.Getenv("AGENTUSE_CONTEXT")
	run := ""
	var selectedPath, selectedRun *string
	if !bound {
		selectedPath = fs.String("path", path, "Client 生成的 context.json")
		selectedRun = fs.String("run", "", "显式选择实际 run UUID")
	}
	showHelp := func(w io.Writer) {
		fmt.Fprint(w, usage)
	}
	fs.Usage = func() { showHelp(os.Stderr) }
	flags, rest := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && (arg == "--path" || arg == "-path" || arg == "--run" || arg == "-run") && !bound {
				i++
				if i == len(args) {
					return command.UsageErr("%s 缺少值", arg)
				}
				flags = append(flags, args[i])
			}
		} else {
			rest = append(rest, arg)
		}
	}
	if err := command.Parse(fs, flags); err != nil {
		return err
	}
	if !bound {
		path, run = *selectedPath, *selectedRun
	}
	if run == "" && path != "" && path == os.Getenv("AGENTUSE_CONTEXT") {
		run = os.Getenv("AGENTUSE_CONTEXT_RUN")
		if run == "" {
			run = os.Getenv("AGENTUSE_RUN")
		}
	}
	if len(rest) == 0 || rest[0] == "help" {
		showHelp(os.Stdout)
		return nil
	}
	switch rest[0] {
	case "input":
		if len(rest) != 1 {
			return command.UsageErr("context input 不接受额外参数")
		}
	case "attachment":
		if len(rest) < 2 {
			return command.UsageErr("context attachment list | read FILE_ID | upload FILE")
		}
		switch rest[1] {
		case "list":
			if len(rest) != 2 {
				return command.UsageErr("context attachment list 不接受额外参数")
			}
		case "read", "upload":
			if len(rest) != 3 {
				return command.UsageErr("context attachment %s 需要一个文件 ID 或路径", rest[1])
			}
		default:
			return command.UsageErr("未知 attachment 命令 %s", rest[1])
		}
	case "enter":
		if bound {
			return command.UsageErr("使用 agentuse context enter 进入上下文")
		}
	default:
		return command.UsageErr("未知 context 命令 %s", rest[0])
	}
	ctx, stop := command.SignalContext()
	defer stop()
	var selected *runservice.ExecutionContext
	var err error
	if rest[0] == "attachment" && (rest[1] == "list" || rest[1] == "read") {
		selected, err = runservice.OpenConversationContext(ctx, path)
	} else {
		selected, err = runservice.OpenExecutionContext(ctx, path, run)
	}
	if err != nil {
		return err
	}
	switch rest[0] {
	case "input":
		if len(rest) != 1 {
			return command.UsageErr("context input 不接受额外参数")
		}
		return json.NewEncoder(os.Stdout).Encode(selected)
	case "attachment":
		switch rest[1] {
		case "list":
			files, err := selected.Attachments(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"attachments": files})
		case "read":
			file, err := selected.Attachment(ctx, rest[2])
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(file)
		case "upload":
			file, err := selected.UploadAttachment(ctx, rest[2])
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(file)
		default:
			return command.UsageErr("未知 attachment 命令 %s", rest[1])
		}

	case "enter":
		if bound || len(rest) != 1 {
			return command.UsageErr("使用 agentuse context enter 进入上下文")
		}
		return enterContext(ctx, selected)
	default:
		return command.UsageErr("未知 context 命令 %s", rest[0])
	}
}

func enterContext(_ context.Context, selected *runservice.ExecutionContext) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
		if runtime.GOOS == "windows" {
			shell = "cmd.exe"
		}
	}
	// syscall.Exec preserves duplicate environment entries, unlike exec.Cmd.
	// Remove inherited bindings before adding the explicitly selected context.
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "AGENTUSE_CONTEXT", "AGENTUSE_CONTEXT_RUN", "AGENTUSE_RUN", "PATH":
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "AGENTUSE_CONTEXT="+selected.Path(), "AGENTUSE_CONTEXT_RUN="+selected.RunID,
		"AGENTUSE_RUN="+selected.RunID, "PATH="+selected.BinPath()+string(os.PathListSeparator)+os.Getenv("PATH"))
	return contextShell(shell, selected.Workspace, environment)
}
