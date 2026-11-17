//go:build !windows

package localipc

import (
	"fmt"
	"os"
	"syscall"
)

func checkOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("local IPC directory belongs to another user")
	}
	return nil
}
