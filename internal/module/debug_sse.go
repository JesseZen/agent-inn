package module

import (
	"context"
	"fmt"
	"io"
)

type DebugSSE struct {
	baseMiddleware
	writer io.Writer
}

func NewDebugSSE(cfg ModuleConfig, writer io.Writer) *DebugSSE {
	return &DebugSSE{
		baseMiddleware: baseMiddleware{name: "debug_sse", config: cfg},
		writer:         writer,
	}
}

func (m *DebugSSE) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	return nil
}

func (m *DebugSSE) WrapResponse(ctx context.Context, req *ProxyRequest, upstream *ProxyResponse) (*ProxyResponse, error) {
	if !m.config.Enabled || m.writer == nil || !isEventStream(upstream.ContentType, upstream.Headers.Get("Content-Type")) {
		return upstream, nil
	}
	next := *upstream
	next.Body = &debugSSEReadCloser{source: upstream.Body, writer: m.writer}
	return &next, nil
}

type debugSSEReadCloser struct {
	source io.ReadCloser
	writer io.Writer
}

func (r *debugSSEReadCloser) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 {
		_, _ = fmt.Fprintf(r.writer, "DEBUG sse chunk bytes=%d\n", n)
	}
	return n, err
}

func (r *debugSSEReadCloser) Close() error {
	return r.source.Close()
}
