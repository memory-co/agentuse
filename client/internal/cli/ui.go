package cli

import (
	"context"
	"encoding/json"
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
	"github.com/memory-co/agentuse/client/internal/fileutil"
	endpointservice "github.com/memory-co/agentuse/client/internal/service/endpoint"
	harnessservice "github.com/memory-co/agentuse/client/internal/service/harness"
	runservice "github.com/memory-co/agentuse/client/internal/service/run"
	"github.com/memory-co/agentuse/client/internal/service/session"
	settingservice "github.com/memory-co/agentuse/client/internal/service/setting"
	"github.com/memory-co/agentuse/internal/command"
)

func localPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentuse", name)
}
func startLocalUI(cfg runservice.Config, sessions controller.SessionQueries, runs controller.RunQueries, managers ...*runservice.Service) (func(), error) {
	addr := cfg.UIAddress()
	if addr == "" {
		return func() {}, nil
	}
	ln, err := controller.Listen(addr)
	if err != nil {
		return nil, fmt.Errorf("本机页面监听失败（setting.yaml 的 local_http.enabled 可关闭页面）: %w", err)
	}
	token, err := controller.Token(localPath("ui.token"))
	if err != nil {
		ln.Close()
		return nil, err
	}
	providers := &endpointservice.Service{Path: cfg.ConfigPath}
	var manager *runservice.Service
	if len(managers) > 0 {
		manager = managers[0]
		providers = manager.Endpoints
	} else {
		providers, err = providers.Snapshot()
		if err != nil {
			ln.Close()
			return nil, err
		}
	}
	handler, err := controller.New(sessions, runs, ln.Addr().String(), token, controller.PublicData{
		SessionStatus: func(ctx context.Context, ep, c, s string) (any, error) { return manager.SessionStatus(ctx, ep, c, s) },
		LocalCreate: func(ctx context.Context, title string) (session.Conversation, error) {
			return manager.CreateConversation(ctx, endpointservice.LocalID, title)
		},
		LocalStart: func(ctx context.Context, conversation string, b json.RawMessage) (runservice.Run, error) {
			var req runservice.Request
			if err := json.Unmarshal(b, &req); err != nil {
				return runservice.Run{}, fileutil.ErrInvalid
			}
			return manager.Start(ctx, endpointservice.LocalID, conversation, req)
		},
		LocalControl: func(ctx context.Context, c, s, action string, b json.RawMessage) (any, error) {
			return manager.Control(ctx, endpointservice.LocalID, c, s, action, b)
		},
		LocalHarnesses: func() (any, error) { return manager.ProbeHarnesses() },
		Endpoints: func() any {
			items, e := providers.Views()
			if e != nil {
				return controller.ReadFailure{Err: e}
			}
			return map[string]any{"items": items}
		},
		Settings: func() any { return cfg.Settings },
		Harnesses: func() any {
			if len(managers) > 0 {
				return managers[0].Harnesses()
			}
			return []harnessservice.HarnessInfo{}
		},
		Status: func() any {
			if len(managers) > 0 {
				return managers[0].Snapshot()
			}
			return map[string]any{"state": "running"}
		},
	})
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
	log.Printf("本机页面: %s（agentuse ui 获取登录链接）", base)
	return func() { _ = server.Close(); _ = os.Remove(localPath("ui.addr")) }, nil
}
func runUI(args []string) error {
	fs := command.NewFlags("agentuse ui")
	fs.String("config", endpointservice.DefaultPath(), "client 配置")
	open := fs.Bool("open", false, "同时在浏览器打开")
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	b, err := os.ReadFile(localPath("ui.addr"))
	if err != nil {
		return errors.New("本机页面尚未启动，请先运行 agentuse start")
	}
	token, err := controller.Token(localPath("ui.token"))
	if err != nil {
		return err
	}
	link := strings.TrimSpace(string(b)) + "/#token=" + url.QueryEscape(token)
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
func startSessions(cfg runservice.Config) (*session.Service, error) {
	root := cfg.Settings.Session.WorkRoot
	if root == "" {
		root = settingservice.Defaults().Session.WorkRoot
	}
	sessions, err := session.New(root)
	if err != nil {
		return nil, err
	}

	return sessions, nil
}
