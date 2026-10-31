package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/memory-co/agentuse/client/internal/controller"
	daemon "github.com/memory-co/agentuse/client/internal/service"
	"github.com/memory-co/agentuse/client/internal/store"
	"github.com/memory-co/agentuse/internal/command"
)

func localPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", name)
}
func startLocalUI(cfg daemon.Config, reader store.Reader) (func(), error) {
	addr := cfg.UIAddress()
	if addr == "" {
		return func() {}, nil
	}
	ln, err := controller.Listen(addr)
	if err != nil {
		return nil, fmt.Errorf("本机页面监听失败（ui_addr 可设为空关闭）: %w", err)
	}
	token, err := controller.Token(localPath("ui.token"))
	if err != nil {
		ln.Close()
		return nil, err
	}
	handler, err := controller.New(reader, ln.Addr().String(), token)
	if err != nil {
		ln.Close()
		return nil, err
	}
	base := "http://" + ln.Addr().String()
	if err = os.WriteFile(localPath("ui.addr"), []byte(base+"\n"), 0600); err != nil {
		ln.Close()
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if e := server.Serve(ln); e != nil && !errors.Is(e, http.ErrServerClosed) {
			log.Printf("本机页面: %v", e)
		}
	}()
	log.Printf("本机只读页面: %s（agentuse ui 获取登录链接）", base)
	return func() { _ = server.Close(); _ = os.Remove(localPath("ui.addr")) }, nil
}
func runUI(args []string) error {
	fs := command.NewFlags("agentuse ui")
	config := fs.String("config", daemon.DefaultConfigPath(), "daemon 配置")
	open := fs.Bool("open", false, "同时在浏览器打开")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	cfg, err := loadDaemonConfig(*config)
	if err != nil {
		return err
	}
	if cfg.UIAddress() == "" {
		return errors.New("本机页面已在 ui_addr 中关闭")
	}
	b, err := os.ReadFile(localPath("ui.addr"))
	if err != nil {
		return errors.New("本机页面尚未启动，请先运行 agentuse daemon")
	}
	token, err := controller.Token(localPath("ui.token"))
	if err != nil {
		return err
	}
	link := strings.TrimSpace(string(b)) + "/?token=" + url.QueryEscape(token)
	fmt.Println(link)
	if *open {
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}
		return exec.Command(name, link).Start()
	}
	return nil
}
func startLedger(ctx context.Context, cfg daemon.Config) (*store.Ledger, error) {
	ledger, err := store.Open(localPath("ledger.db"))
	if err != nil {
		return nil, err
	}
	err = ledger.Writer().Purge(ctx, time.Now().AddDate(0, 0, -cfg.RetentionDays()))
	if err != nil {
		ledger.Close()
		return nil, err
	}
	return ledger, nil
}
