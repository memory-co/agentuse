package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/memory-co/agentuse/internal/models"
)

// Job 是交给 agent CLI 的一次活。
type Job struct {
	Prompt   string
	Dir      string   // 现场目录
	Env      []string // 追加的环境变量:AGENTUSE_URL / AGENTUSE_TOKEN
	ResumeID string   // 续接的 agent 会话 id,空 = 新会话
	Mode     models.WorkspaceMode
}

// Result 是 agent 跑完交回的东西。
type Result struct {
	AgentSessionID string
}

// Driver 把交互式的 agent CLI 变成可被程序调用的。
type Driver interface {
	Run(ctx context.Context, job Job) (Result, error)
}

// NewDriver 按名字选驱动。
func NewDriver(name string) (Driver, error) {
	switch name {
	case "", "claude":
		return &ClaudeDriver{Bin: "claude"}, nil
	default:
		return nil, fmt.Errorf("daemon: unknown driver %q", name)
	}
}

// ClaudeDriver 用 `claude -p --output-format stream-json` 驱动 Claude Code。
type ClaudeDriver struct {
	Bin string
}

func (d *ClaudeDriver) Args(job Job) []string {
	args := []string{"-p", job.Prompt, "--output-format", "stream-json", "--verbose",
		"--allowedTools", "Bash(agentuse *)"}
	if job.Mode == models.WorkspaceReadOnly {
		args = append(args, "--permission-mode", "plan")
	} else {
		args = append(args, "--permission-mode", "acceptEdits")
	}
	if job.ResumeID != "" {
		args = append(args, "--resume", job.ResumeID)
	}
	return args
}

func (d *ClaudeDriver) Run(ctx context.Context, job Job) (Result, error) {
	var res Result
	cmd := exec.CommandContext(ctx, d.Bin, d.Args(job)...)
	cmd.Dir = job.Dir
	cmd.Env = append(os.Environ(), job.Env...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	if err := cmd.Start(); err != nil {
		return res, err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var msg struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) == nil && msg.SessionID != "" {
			res.AgentSessionID = msg.SessionID
		}
	}
	return res, cmd.Wait()
}
