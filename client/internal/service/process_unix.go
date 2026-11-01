//go:build !windows

package service

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Each native execution has its own process group, so cancellation also stops
// children that inherited stdout and would otherwise keep a debug run hanging.
func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			e := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(e, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return e
		}
	}
}
