package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/internal/models"
)

// PrepareWorkspace 为一个会话准备现场:agent 工作目录(git 仓库)里一个 worktree,分支是会话的分支,
// 放在 <root>/<platform>/<session id>。同一会话的后续运行复用同一个 worktree,现场接着上次。
// agent 没有工作目录时给一个空目录。
func PrepareWorkspace(ctx context.Context, root string, a models.Agent, sess models.Session) (string, error) {
	dir := filepath.Join(root, sess.Platform, sess.ID)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if a.Workdir == "" {
		return dir, os.MkdirAll(dir, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	base := a.BaseRef
	if base == "" {
		base = "main"
	}
	repo := expandHome(a.Workdir)
	// 分支已存在(上次的 worktree 被清掉了)就直接挂上;否则从 base 切出来。
	args := []string{"-C", repo, "worktree", "add", "-b", sess.Branch, dir, base}
	if exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--verify", "--quiet", sess.Branch).Run() == nil {
		args = []string{"-C", repo, "worktree", "add", dir, sess.Branch}
	}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return dir, nil
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}
