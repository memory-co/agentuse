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

// HeartbeatInterval 是跑运行期间续租的间隔;hub 的租约默认 2 分钟,够丢几次心跳。
const HeartbeatInterval = 15 * time.Second

// Daemon 是领取—执行—交回的循环。
type Daemon struct {
	Config    config.Daemon
	Home      string
	Client    *Client
	Source    Source
	Driver    Driver
	Backoff   time.Duration // 连不上 hub 时多久重试
	Heartbeat time.Duration
}

func New(cfg config.Daemon, home string) (*Daemon, error) {
	drv, err := NewDriver(cfg.Driver)
	if err != nil {
		return nil, err
	}
	client := NewClient(cfg.HubURL, cfg.ID, cfg.Key)
	src, err := NewSource(cfg.Transport, client, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Daemon{
		Config:    cfg,
		Home:      home,
		Client:    client,
		Source:    src,
		Driver:    drv,
		Backoff:   5 * time.Second,
		Heartbeat: HeartbeatInterval,
	}, nil
}

// Run 一直跑到 ctx 结束。一次只跑一条运行。
func (d *Daemon) Run(ctx context.Context) error {
	defer d.Source.Close()
	for {
		claimed, err := d.Source.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("next run: %v", err)
			if !sleep(ctx, d.Backoff) {
				return ctx.Err()
			}
			continue
		}
		d.process(ctx, claimed)
	}
}

// process 跑一条运行:期间心跳续租;续租发现运行已被收回就停掉 agent;最后交回。
func (d *Daemon) process(ctx context.Context, c hub.Claimed) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.keepAlive(runCtx, cancel, c.Run.ID, c.Run.Attempts)

	done := d.handle(runCtx, c)
	done.Attempt = c.Run.Attempts
	// 交回用父 ctx:运行被 cancel 了也要告诉 hub。
	err := d.Client.Complete(ctx, c.Run.ID, done)
	switch {
	case errors.Is(err, ErrLost):
		log.Printf("run %s: no longer ours, result dropped", c.Run.ID)
	case err != nil:
		log.Printf("complete %s: %v (lease will expire and the run will be retried)", c.Run.ID, err)
	}
}

func (d *Daemon) keepAlive(ctx context.Context, cancel context.CancelFunc, runID string, attempt int) {
	t := time.NewTicker(d.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := d.Client.Heartbeat(ctx, runID, attempt)
			if errors.Is(err, ErrLost) {
				log.Printf("run %s: lease lost, stopping agent", runID)
				cancel()
				return
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("heartbeat %s: %v", runID, err)
			}
		}
	}
}

func (d *Daemon) handle(ctx context.Context, c hub.Claimed) hub.Completion {
	log.Printf("run %s: %s (%s, attempt %d)", c.Run.ID, c.Run.Thread, c.Session.ID, c.Run.Attempts)
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
