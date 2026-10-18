// Package daemon 跑在用户电脑上:主动连出 hub,领取绑在这台设备上的 agent 的运行,
// 在 agent 目录的 worktree 里回放现场,驱动 agent CLI。
// daemon.json 里每个 (hub, 平台) 一个 Daemon,各跑各的。
package daemon

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"time"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/hub"
)

// HeartbeatInterval 是跑运行期间续租的间隔;hub 的租约默认 2 分钟,够丢几次心跳。
const HeartbeatInterval = 15 * time.Second

// Daemon 是一台设备在一个 hub 上的领取—执行—交回循环。
type Daemon struct {
	Hub       config.HubConn
	Worktrees string // worktree 根目录
	Client    *Client
	Source    Source
	Driver    Driver        // 非空时所有运行都用它(测试用);空 = 按 agent 的 driver 选
	Backoff   time.Duration // 连不上 hub 时多久重试
	Heartbeat time.Duration
}

// New 为 daemon.json 里的一个接入建循环。
func New(conn config.HubConn, cfg config.Daemon, home string) (*Daemon, error) {
	client := NewClient(conn.URL, conn.DeviceID, conn.DeviceKey)
	src, err := NewSource(cfg.Transport, client, 5*time.Second)
	if err != nil {
		return nil, err
	}
	root := cfg.Worktrees
	if root == "" {
		root = filepath.Join(home, "worktrees")
	}
	return &Daemon{
		Hub:       conn,
		Worktrees: root,
		Client:    client,
		Source:    src,
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
	if !done.Succeeded {
		log.Printf("run %s: failed: %s", c.Run.ID, done.Error)
	}
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
	log.Printf("run %s: @%s in %s (%s, attempt %d)", c.Run.ID, c.Agent.Handle, c.Run.Thread, c.Session.ID, c.Run.Attempts)
	drv := d.Driver
	if drv == nil {
		var err error
		if drv, err = NewDriver(c.Agent.Driver); err != nil {
			return hub.Completion{Error: err.Error()}
		}
	}
	dir, err := PrepareWorkspace(ctx, d.Worktrees, c.Agent, c.Session)
	if err != nil {
		return hub.Completion{Error: err.Error()}
	}
	res, err := drv.Run(ctx, Job{
		Prompt:   BuildPrompt(c.Run),
		Dir:      dir,
		Env:      []string{"AGENTUSE_URL=" + d.Hub.URL, "AGENTUSE_TOKEN=" + c.Token},
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
