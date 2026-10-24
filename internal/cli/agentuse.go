package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/memory-co/agentuse/internal/daemon"
)

const agentuseUsage = `agentuse —— 让这台电脑上的 CLI agent 从 agentuse 服务领活

用法:
  agentuse register -server URL -token 安装令牌   注册这台电脑(setup.sh 会替你跑)
  agentuse daemon                                 前台运行 daemon(后台服务跑的就是它)
  agentuse status                                 看本机的注册和连接状态
  agentuse agents [-json]                         看本机检测到哪些 agent
  agentuse version

所有命令都可以用 -config 指定配置文件，默认 ~/.agentuse/daemon.json。
`

// AgentuseMain 是用户电脑上的 agentuse。
func AgentuseMain(args []string) int {
	return dispatch("agentuse", agentuseUsage, args, []command{
		{"register", runRegister},
		{"daemon", runDaemon},
		{"status", runStatus},
		{"agents", runAgents},
	})
}

func runRegister(args []string) error {
	fs := newFlags("agentuse register")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	server := fs.String("server", "", "agentuse 服务地址")
	token := fs.String("token", os.Getenv("TOKEN"), "页面上生成的安装令牌(默认读环境变量 TOKEN)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *server == "" || *token == "" {
		return usageErr("需要 -server 和 -token")
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
	fs := newFlags("agentuse daemon")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadDaemonConfig(*cfgPath)
	if err != nil {
		return err
	}
	daemon.Version = Version
	d, err := daemon.New(cfg, nil)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runStatus(args []string) error {
	fs := newFlags("agentuse status")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	if err := parse(fs, args); err != nil {
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
	printTable(os.Stdout, [][]string{
		{"服务", cfg.Server},
		{"daemon", cfg.DaemonID},
		{"配置", *cfgPath},
		{"服务连通", conn},
		{"后台服务", daemon.ServiceStatus()},
	})
	return nil
}

func runAgents(args []string) error {
	fs := newFlags("agentuse agents")
	cfgPath := fs.String("config", daemon.DefaultConfigPath(), "配置文件")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := daemon.LoadConfig(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	agents := daemon.ListAgents(cfg)
	if *asJSON {
		return printJSON(os.Stdout, agents)
	}
	rows := [][]string{{"名字", "命令", "版本"}}
	for _, a := range agents {
		path, ver := a.Path, a.Version
		switch {
		case a.Disabled:
			path, ver = "-", "已在配置里禁用"
		case !a.Found:
			path, ver = "-", "未安装"
		}
		rows = append(rows, []string{a.Name, path, ver})
	}
	printTable(os.Stdout, rows)
	return nil
}
