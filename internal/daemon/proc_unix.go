//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup 让 harness 跑在自己的进程组里，取消时杀掉整组。
// 否则只杀得到直接子进程，它起的子进程(shell 里的命令、工具调用)还拿着 stdout,读输出会一直卡到它们自己退出。
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
