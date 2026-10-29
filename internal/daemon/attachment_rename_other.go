//go:build !unix

package daemon

import (
	"errors"
	"os"
)

func renameAttachment(root *os.Root, old, new string) error {
	return errors.New("附件同步目前只支持 Linux 和 macOS")
}
