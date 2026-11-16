//go:build linux || darwin

package cli

import "syscall"

// Exec keeps the PID and service-manager ownership, including inherited logs.
func execServer(path string, args, environment []string) error {
	return syscall.Exec(path, append([]string{path}, args...), environment)
}
