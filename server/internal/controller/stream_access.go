package controller

import (
	"io"
	"net/http"
	"time"
)

type accessWriter struct {
	io.Writer
	check func() error
	last  time.Time
}

func (w *accessWriter) Write(p []byte) (int, error) {
	if time.Since(w.last) >= time.Second {
		if err := w.check(); err != nil {
			return 0, err
		}
		w.last = time.Now()
	}
	return w.Writer.Write(p)
}
func (s *Server) copyResource(w http.ResponseWriter, r *http.Request, source io.Reader) (int64, error) {
	if !resourceCredential(r) {
		return io.Copy(w, source)
	}
	// Interrupt blocked readers/writers when the database revokes access.
	done := make(chan struct{})
	finished := make(chan struct{})
	defer func() { close(done); <-finished }()
	go func() {
		defer close(finished)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := s.service.RunService.AccessForRequest(r.Context(), requestToken(r)); err != nil {
					if closer, ok := source.(io.Closer); ok {
						_ = closer.Close()
					}
					_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
					return
				}
			}
		}
	}()
	// WriterTo/ReaderFrom optimizations must not bypass periodic authorization.
	dst := &accessWriter{Writer: w, check: func() error {
		_, err := s.service.RunService.AccessForRequest(r.Context(), requestToken(r))
		return err
	}}
	return io.CopyBuffer(dst, struct{ io.Reader }{source}, make([]byte, 32<<10))
}
