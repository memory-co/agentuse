package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/internal/command"
	"github.com/memory-co/agentuse/internal/version"
	"os"

	"github.com/memory-co/agentuse/internal/daemon"
)

const agentuseUsage = `agentuse —— 让这台电脑上的 CLI harness 从 agentuse 服务领活

用法:
  agentuse register -server URL -token 安装令牌   注册这台电脑(setup.sh 会替你跑)
  agentuse daemon                                 前台运行 daemon(后台服务跑的就是它)
  agentuse status                                 看本机的注册和连接状态
  agentuse harnesses [-json]                         看本机检测到哪些 harness
  agentuse work …                                 工作的协调者用来开线程、看线程(见 agentuse work help)
  agentuse session send <会话> --agents coder,reviewer "消息" | read | runs | wait | cancel
  agentuse attachment upload <文件> | list | download <id> --output <文件>
  agentuse version

所有命令都可以用 -config 指定配置文件，默认 ~/.agentuse/daemon.json。
`

// Main 是用户电脑上的 agentuse。
func Main(args []string) int {
	return command.Dispatch("agentuse", agentuseUsage, args, []command.Command{
		{"register", runRegister},
		{"daemon", runDaemon},
		{"status", runStatus},
		{"harnesses", runHarnesses},
		{"work", runWork},
		{"attachment", runAttachment},
		{"session", runSession},
	})
}

func runRegister(args []string) error {
	fs := command.NewFlags("agentuse register")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	server := fs.String("server", "", "agentuse 服务地址")
	token := fs.String("token", os.Getenv("TOKEN"), "页面上生成的安装令牌(默认读环境变量 TOKEN)")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	if *server == "" || *token == "" {
		return command.UsageErr("需要 -server 和 -token")
	}
	cfg, err := daemon.Register(context.Background(), *server, *token, *cfgPath)
	if err != nil {
		return fmt.Errorf("注册失败: %w", err)
	}
	fmt.Printf("注册成功: daemon %s,配置已写入 %s\n", cfg.DaemonID, *cfgPath)
	return nil
}

func loadDaemonConfig(path string) (daemon.Config, error) {
	cfg, err := daemon.LoadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("这台电脑还没注册(%s 不存在):请在 agentuse 页面上添加 daemon,复制安装命令运行", path)
	}
	return cfg, err
}

func runDaemon(args []string) error {
	fs := command.NewFlags("agentuse daemon")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadDaemonConfig(*cfgPath)
	if err != nil {
		return err
	}
	daemon.Version = version.Version
	d, err := daemon.New(cfg, nil)
	if err != nil {
		return err
	}
	ctx, stop := command.SignalContext()
	defer stop()
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runStatus(args []string) error {
	fs := command.NewFlags("agentuse status")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadDaemonConfig(*cfgPath)
	if err != nil {
		return err
	}
	conn := "正常(密钥有效)"
	if cfg.Key == "" {
		conn = "未注册"
	} else if _, err := daemon.CheckKey(context.Background(), cfg); err != nil {
		conn = "失败: " + err.Error()
	}
	command.PrintTable(os.Stdout, [][]string{
		{"服务", cfg.Server},
		{"daemon", cfg.DaemonID},
		{"配置", *cfgPath},
		{"服务连通", conn},
		{"后台服务", daemon.ServiceStatus()},
	})
	return nil
}

func runHarnesses(args []string) error {
	fs := command.NewFlags("agentuse harnesses")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	cfg, err := daemon.LoadConfig(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	harnesses := daemon.ListHarnesses(cfg)
	if *asJSON {
		return command.PrintJSON(os.Stdout, harnesses)
	}
	rows := [][]string{{"名字", "命令", "版本"}}
	for _, a := range harnesses {
		path, ver := a.Path, a.Version
		switch {
		case a.Disabled:
			path, ver = "-", "已在配置里禁用"
		case !a.Found:
			path, ver = "-", "未安装"
		}
		rows = append(rows, []string{a.Name, path, ver})
	}
	command.PrintTable(os.Stdout, rows)
	return nil
}
