//go:build !windows

package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock uses an OS lock, released even if the process crashes. The empty lock file
// carries no configuration or execution truth.
func Lock(path string) (func(), error) {
	if _, e := SafePath(filepath.Dir(path), filepath.Base(path)); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("client files are in use: %w", e)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
