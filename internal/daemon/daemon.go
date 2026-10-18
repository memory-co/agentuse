// Package daemon 跑在用户电脑上:主动连出 hub,领取运行,在 worktree 里回放现场,驱动 agent CLI。
// 同一个 daemon 可以同时服务多个平台。
package daemon

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/hub"
)

// Daemon 是领取—执行—交回的循环。
type Daemon struct {
	Config   config.Daemon
	Home     string
	Client   *Client
	Driver   Driver
	Interval time.Duration // 队列空时多久再问一次
}

func New(cfg config.Daemon, home string) (*Daemon, error) {
	drv, err := NewDriver(cfg.Driver)
	if err != nil {
		return nil, err
	}
	return &Daemon{
		Config:   cfg,
		Home:     home,
		Client:   NewClient(cfg.HubURL, cfg.ID, cfg.Key),
		Driver:   drv,
		Interval: 5 * time.Second,
	}, nil
}

// Run 一直跑到 ctx 结束。一次只跑一条运行。
func (d *Daemon) Run(ctx context.Context) error {
	for {
		claimed, err := d.Client.Claim(ctx)
		switch {
		case errors.Is(err, ErrNoRun):
			if !sleep(ctx, d.Interval) {
				return ctx.Err()
			}
			continue
		case err != nil:
			log.Printf("claim: %v", err)
			if !sleep(ctx, d.Interval) {
				return ctx.Err()
			}
			continue
		}
		done := d.handle(ctx, claimed)
		if err := d.Client.Complete(ctx, claimed.Run.ID, done); err != nil {
			log.Printf("complete %s: %v", claimed.Run.ID, err)
		}
	}
}

func (d *Daemon) handle(ctx context.Context, c hub.Claimed) hub.Completion {
	log.Printf("run %s: %s (%s)", c.Run.ID, c.Run.Thread, c.Session.ID)
	dir, err := PrepareWorkspace(ctx, d.Home, d.Config.Workspaces[c.Run.Platform], c.Session)
	if err != nil {
		return hub.Completion{Error: err.Error()}
	}
	res, err := d.Driver.Run(ctx, Job{
		Prompt:   BuildPrompt(c.Run),
		Dir:      dir,
		Env:      []string{"AGENTUSE_URL=" + d.Config.HubURL, "AGENTUSE_TOKEN=" + c.Token},
		ResumeID: c.Session.AgentSessionID,
		Mode:     c.Run.WorkspaceMode,
	})
	if err != nil {
		return hub.Completion{Error: err.Error(), AgentSessionID: res.AgentSessionID}
	}
	return hub.Completion{Succeeded: true, AgentSessionID: res.AgentSessionID}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
