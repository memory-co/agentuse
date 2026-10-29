//go:build unix

package daemon

import (
	"golang.org/x/sys/unix"
	"os"
)

// renameAttachment is relative to an already opened directory, including on Go 1.24.
func renameAttachment(root *os.Root, old, new string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return unix.Renameat(int(dir.Fd()), old, int(dir.Fd()), new)
}
