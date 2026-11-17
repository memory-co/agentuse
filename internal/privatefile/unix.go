//go:build !windows

// Package privatefile restricts private cryptographic material to its OS user.
package privatefile

import "os"

func Protect(path string) error { return os.Chmod(path, 0600) }
