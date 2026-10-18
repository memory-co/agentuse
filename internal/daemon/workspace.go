package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/memory-co/agentuse/internal/config"
	"github.com/memory-co/agentuse/internal/models"
)

// PrepareWorkspace 为一个会话准备现场:仓库里一个 worktree,分支是会话的分支。
// 同一会话的后续运行复用同一个 worktree,现场接着上次。没配仓库的平台给一个空目录。
func PrepareWorkspace(ctx context.Context, home string, ws config.Workspace, sess models.Session) (string, error) {
	root := ws.Worktree
	if root == "" {
		root = filepath.Join(home, "worktrees")
	}
	dir := filepath.Join(root, sess.Platform, sess.ID)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if ws.Repo == "" {
		return dir, os.MkdirAll(dir, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	base := ws.BaseRef
	if base == "" {
		base = "main"
	}
	repo := expandHome(ws.Repo)
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
