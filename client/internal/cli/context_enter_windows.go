//go:build windows

package cli

import (
	"os"
	"os/exec"
)

func contextShell(shell, workspace string, environment []string) error {
	cmd := exec.Command(shell)
	cmd.Dir, cmd.Env = workspace, environment
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
