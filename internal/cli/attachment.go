package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/internal/attachment"
	"github.com/memory-co/agentuse/internal/daemon"
)

func runAttachment(args []string) error {
	if len(args) == 0 {
		return usageErr("用法: agentuse attachment upload <文件> | list | download <id> --output <文件>")
	}
	action := args[0]
	fs := newFlags("agentuse attachment " + action)
	session := fs.String("session", os.Getenv("AGENTUSE_SESSION"), "会话 id")
	server := fs.String("server", os.Getenv("AGENTUSE_SERVER"), "服务地址")
	token := fs.String("token", os.Getenv("AGENTUSE_TOKEN"), "用户或工作令牌")
	cfgPath := fs.String("config", envOr("AGENTUSE_CONFIG", daemon.DefaultConfigPath()), "daemon 配置")
	name := fs.String("name", "", "上传文件名")
	output := fs.String("output", "", "下载目标文件")
	force := fs.Bool("force", false, "允许覆盖下载目标")
	asJSON := fs.Bool("json", false, "输出 JSON")
	// flag stops at the first positional argument; accept flags on either side.
	var flags, positionals []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			key := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
			if !strings.Contains(arg, "=") && key != "json" && key != "force" && key != "h" && key != "help" {
				if i+1 >= len(args) {
					return usageErr("%s 缺少值", arg)
				}
				i++
				flags = append(flags, args[i])
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	if err := parse(fs, flags); err != nil {
		return err
	}
	if *session == "" {
		return errors.New("需要 --session 或 AGENTUSE_SESSION")
	}
	if *token == "" {
		*token = envOr("AGENTUSE_WORK", os.Getenv("AGENTUSE_WORK_TOKEN"))
	}
	if *server == "" || *token == "" {
		cfg, err := daemon.LoadConfig(*cfgPath)
		if err != nil {
			return err
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
	c := attachment.Client{Server: *server, Token: *token}
	ctx, cancel := signalContext()
	defer cancel()
	printJSON := func(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
	switch action {
	case "upload":
		if len(positionals) != 1 {
			return usageErr("upload 需要一个文件路径")
		}
		f, err := os.Open(positionals[0])
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return errors.New("只能上传普通文件")
		}
		filename := *name
		if filename == "" {
			filename = filepath.Base(positionals[0])
		}
		var messageID int64
		if strings.HasPrefix(*token, "dk_") {
			messageID, err = strconv.ParseInt(os.Getenv("AGENTUSE_MESSAGE_ID"), 10, 64)
			if err != nil || messageID <= 0 {
				return errors.New("daemon 上传需要 AGENTUSE_MESSAGE_ID")
			}
		}
		a, err := c.Upload(ctx, *session, filename, f, messageID)
		if err != nil {
			return fmt.Errorf("上传失败（连接中断时可能已经成功，请先 list 检查）: %w", err)
		}
		if *asJSON {
			return printJSON(a)
		}
		fmt.Printf("%s\t%s\t%d bytes\n%s\n", a.ID, a.Filename, a.SizeBytes, a.DownloadPath)
		return nil
	case "list":
		if len(positionals) != 0 {
			return usageErr("list 不接受位置参数")
		}
		list, err := c.List(ctx, *session)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(list)
		}
		rows := [][]string{{"id", "文件名", "字节", "下载路径"}}
		for _, a := range list {
			rows = append(rows, []string{a.ID, a.Filename, strconv.FormatInt(a.SizeBytes, 10), a.DownloadPath})
		}
		printTable(os.Stdout, rows)
		return nil
	case "download":
		if len(positionals) != 1 || *output == "" {
			return usageErr("download <id> --output <文件> [--force]")
		}
		a, err := c.Get(ctx, *session, positionals[0])
		if err != nil {
			return err
		}
		if err = c.Download(ctx, a, *output, *force); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(map[string]any{"attachment": a, "output": *output})
		}
		fmt.Println(*output)
		return nil
	default:
		return usageErr("未知 attachment 命令 %s", action)
	}
}
