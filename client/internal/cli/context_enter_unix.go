//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// Replace the launcher so terminal signals and the shell's exit status retain
// their normal behavior (Ctrl+C interrupts a command, not the entire context).
func contextShell(shell, workspace string, environment []string) error {
	path, err := exec.LookPath(shell)
	if err != nil {
		return err
	}
	if err = os.Chdir(workspace); err != nil {
		return err
	}
	return syscall.Exec(path, []string{shell}, environment)
}
