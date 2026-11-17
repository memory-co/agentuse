//go:build !windows

// Package localipc provides an OS-private transport independent of the Web UI.
package localipc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func Address(identity string) (string, error) {
	sum := sha256.Sum256([]byte(identity))
	dir := filepath.Join(os.TempDir(), "agentuse-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", fmt.Errorf("unsafe local IPC directory")
	}
	// The containing directory must be owned by the current user.
	if err = checkOwner(info); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%x.sock", sum[:16])), nil
}
func Listen(address string) (net.Listener, error) {
	l, err := net.Listen("unix", address)
	if err == nil {
		return l, nil
	}
	c, e := net.DialTimeout("unix", address, time.Second)
	if e == nil {
		c.Close()
		return nil, fmt.Errorf("client context daemon already running")
	}
	if !errors.Is(e, syscall.ECONNREFUSED) {
		return nil, err
	}
	if info, e := os.Lstat(address); e == nil && info.Mode()&os.ModeSocket != 0 {
		if e = os.Remove(address); e != nil {
			return nil, e
		}
		return net.Listen("unix", address)
	}
	return nil, err
}
func Dial(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}
