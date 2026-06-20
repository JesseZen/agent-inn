package worker

import (
	"context"
	"io"
	"net/http"
	"os"
)

type Server struct {
	httpServer *http.Server
}

func NewServer(addr string, worker *Worker) *Server {
	return &Server{httpServer: &http.Server{Addr: addr, Handler: worker}}
}

func (s *Server) ListenAndServe() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Close() error {
	return s.httpServer.Close()
}

func (s *Server) InstallOrphanWatcher(stdin *os.File, shutdown func()) {
	if !isRunningUnderManager(stdin) {
		return
	}
	watchOrphan(stdin, shutdown)
}

func isRunningUnderManager(stdin *os.File) bool {
	info, err := stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func watchOrphan(stdin io.Reader, shutdown func()) {
	go func() {
		buf := make([]byte, 1)
		_, _ = stdin.Read(buf)
		shutdown()
	}()
}
