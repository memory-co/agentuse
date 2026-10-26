//go:build !unix

package daemon

import "os/exec"

func ownProcessGroup(cmd *exec.Cmd) {}
