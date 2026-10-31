package cli

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func capture(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	done := make(chan [2]string)
	go func() {
		var o, e bytes.Buffer
		ch := make(chan struct{})
		go func() { io.Copy(&e, errR); close(ch) }()
		io.Copy(&o, outR)
		<-ch
		done <- [2]string{o.String(), e.String()}
	}()
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	fn()
	outW.Close()
	errW.Close()
	r := <-done
	return r[0], r[1]
}
