package worker

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

const (
	headerRequestID         = "X-Request-Id"
	contentEncodingGzip     = "gzip"
	contentEncodingDeflate  = "deflate"
	contentEncodingZstd     = "zstd"
	proxyResponseBufferSize = 32 * 1024
)

type Worker struct {
	snapshots      *snapshotHolder
	client         *http.Client
	logger         *slog.Logger
	metrics        *MetricsTracker
	metricsEmitter *metricsEventEmitter
	metricFinishes sync.WaitGroup
}

type Options struct {
	Snapshot      RuntimeConfigSnapshot
	Runtime       appruntime.WorkerRuntime
	Client        *http.Client
	Logger        *slog.Logger
	MetricsWriter io.Writer
}

func New(opts Options) (*Worker, error) {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	logger := opts.Logger
	if logger == nil {
		logger = logging.New(io.Discard, "simple", logging.ComponentWorkerProxy)
	}
	snapshot := opts.Snapshot
	if opts.Runtime.Upstream.BaseURL != "" {
		var err error
		snapshot, err = snapshotFromRuntime(opts.Runtime)
		if err != nil {
			return nil, err
		}
	}
	snapshot = snapshot.withCompiledUpstream()
	snapshot, err := snapshot.withHTTPClient(client)
	if err != nil {
		return nil, err
	}
	return &Worker{
		snapshots:      newSnapshotHolder(snapshot),
		client:         client,
		logger:         logger,
		metrics:        NewMetricsTracker(time.Now),
		metricsEmitter: newMetricsEventEmitter(opts.MetricsWriter),
	}, nil
}

func (w *Worker) MetricsSnapshot() MetricsSnapshot {
	snapshot := w.metrics.Snapshot()
	if w.metricsEmitter != nil {
		snapshot.DroppedEvents = w.metricsEmitter.dropped.Load()
	}
	return snapshot
}

func (w *Worker) UpdateRuntime(runtime appruntime.WorkerRuntime) (appruntime.Generation, error) {
	snapshot, err := snapshotFromRuntime(runtime)
	if err != nil {
		return 0, err
	}
	snapshot = snapshot.withCompiledUpstream()
	snapshot, err = snapshot.withHTTPClient(w.client)
	if err != nil {
		return 0, err
	}
	w.snapshots.Store(snapshot)
	w.logger.Info(logging.EventSnapshotReload, "generation", snapshot.Generation)
	return appruntime.Generation(snapshot.Generation), nil
}

func (w *Worker) UpdateSnapshot(snapshot RuntimeConfigSnapshot) error {
	snapshot = snapshot.withCompiledUpstream()
	var err error
	snapshot, err = snapshot.withHTTPClient(w.client)
	if err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	w.snapshots.Store(snapshot)
	return nil
}

func (w *Worker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, constants.ProxyPathPrefix) || (r.URL.Path == proxyStatusAliasPath && r.Method == http.MethodGet) {
		w.serveManagement(rw, r)
		return
	}

	reqID := r.Header.Get(headerRequestID)
	if reqID == "" {
		reqID = logging.NewRequestID()
	}
	ctx := logging.ContextWithRequestID(r.Context(), reqID)

	rec := &responseRecorder{ResponseWriter: rw}
	start := time.Now()
	w.metrics.Start()
	w.logger.InfoContext(ctx, logging.EventRequestStart,
		"method", r.Method,
		"path", r.URL.Path,
	)

	snapshot := w.snapshots.Load()
	snapshot = snapshot.withCompiledUpstream()
	result, err := w.proxyRequest(rec, r.WithContext(ctx), snapshot)
	dur := time.Since(start)
	if err != nil {
		if rec.status == 0 {
			http.Error(rec, err.Error(), http.StatusBadGateway)
		}
	}
	event := RequestMetricEvent{
		Timestamp:     time.Now(),
		Upstream:      snapshot.Upstream.Name,
		Method:        r.Method,
		Path:          r.URL.Path,
		Status:        rec.status,
		DurationMS:    dur.Milliseconds(),
		ResponseBytes: rec.written,
		Model:         result.Model,
		Usage:         result.Usage,
	}
	if result.pending == nil {
		w.metrics.Finish(event)
		if w.metricsEmitter != nil {
			w.metricsEmitter.Emit(event)
		}
	} else {
		w.metricFinishes.Add(1)
		go func() {
			defer w.metricFinishes.Done()
			metadata := <-result.pending
			event.Model = metadata.Model
			event.Usage = metadata.Usage
			w.metrics.Finish(event)
			if w.metricsEmitter != nil {
				w.metricsEmitter.Emit(event)
			}
		}()
	}
	if err != nil {
		w.logger.ErrorContext(ctx, logging.EventRequestDone,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur", dur.Truncate(time.Millisecond).String(),
			"err", err.Error(),
		)
		return
	}
	level := logging.LevelForStatus(rec.status)
	w.logger.Log(ctx, level, logging.EventRequestDone,
		"method", r.Method,
		"path", r.URL.Path,
		"status", rec.status,
		"dur", dur.Truncate(time.Millisecond).String(),
		"bytes", rec.written,
	)
}

// responseRecorder wraps http.ResponseWriter to capture status code and byte count.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *Worker) proxyRequest(rw http.ResponseWriter, r *http.Request, snapshot RuntimeConfigSnapshot) (responseCopyResult, error) {
	ctx := r.Context()
	proxyReq := &module.ProxyRequest{
		Method:       r.Method,
		Path:         r.URL.Path,
		RawQuery:     r.URL.RawQuery,
		Headers:      r.Header.Clone(),
		ContentType:  r.Header.Get("Content-Type"),
		OriginalPath: r.URL.Path,
	}
	contentEncoding := strings.ToLower(strings.TrimSpace(proxyReq.Headers.Get("Content-Encoding")))
	bodyRequired := false
	for _, middleware := range snapshot.Modules {
		plan := middleware.RequestBodyMode(module.ProxyRequestMeta{
			Method:      proxyReq.Method,
			Path:        proxyReq.Path,
			Headers:     proxyReq.Headers,
			ContentType: proxyReq.ContentType,
		})
		if plan == module.RequestBodyBuffer {
			bodyRequired = true
			break
		}
	}
	if bodyRequired {
		body, contentType, err := readRequestBody(r)
		if err != nil {
			return responseCopyResult{}, err
		}
		proxyReq.Body = body
		proxyReq.ContentType = contentType
		proxyReq.NormalizeBufferedBodyHeaders()
	}
	for _, middleware := range snapshot.Modules {
		if err := middleware.ProcessRequest(ctx, proxyReq); err != nil {
			w.logger.ErrorContext(ctx, logging.EventModuleFail,
				"module", middleware.Name(),
				"method", proxyReq.Method,
				"path", proxyReq.Path,
				"err", err.Error(),
			)
			return responseCopyResult{}, err
		}
	}
	if !bodyRequired && contentEncoding != "" && contentEncoding != "identity" {
		body, _, err := readRequestBody(r)
		if err != nil {
			return responseCopyResult{}, err
		}
		proxyReq.Body = body
		bodyRequired = true
	}
	if bodyRequired {
		proxyReq.NormalizeBufferedBodyHeaders()
	}

	upstreamURL, err := snapshot.CompiledUpstream.Join(proxyReq.Path, proxyReq.RawQuery)
	if err != nil {
		return responseCopyResult{}, err
	}
	var body io.Reader = r.Body
	if bodyRequired {
		body = bytes.NewReader(proxyReq.Body)
	}
	upstreamReq, err := http.NewRequestWithContext(ctx, proxyReq.Method, upstreamURL, body)
	if err != nil {
		return responseCopyResult{}, err
	}
	upstreamReq.Header = proxyReq.Headers.Clone()
	if snapshot.CompiledUpstream.AuthorizationHeader != "" {
		upstreamReq.Header.Set("Authorization", snapshot.CompiledUpstream.AuthorizationHeader)
	}
	if bodyRequired && len(proxyReq.Body) > 0 {
		upstreamReq.ContentLength = int64(len(proxyReq.Body))
	}

	client := snapshot.HTTPClient
	if client == nil {
		client = w.client
	}
	upstreamHTTPResp, err := client.Do(upstreamReq)
	if err != nil {
		w.logger.ErrorContext(ctx, logging.EventUpstreamFail,
			"method", proxyReq.Method,
			"path", proxyReq.Path,
			"url", upstreamURL,
			"err", err.Error(),
		)
		return responseCopyResult{}, err
	}
	observedBody := newUsageObservingReadCloser(
		upstreamHTTPResp.Body,
		upstreamHTTPResp.Header.Get("Content-Encoding"),
		NewUsageObserver(upstreamHTTPResp.Header.Get("Content-Type")),
	)
	upstreamHTTPResp.Body = observedBody
	proxyResp := &module.ProxyResponse{
		StatusCode:  upstreamHTTPResp.StatusCode,
		Headers:     upstreamHTTPResp.Header.Clone(),
		Body:        upstreamHTTPResp.Body,
		ContentType: upstreamHTTPResp.Header.Get("Content-Type"),
	}

	for i := len(snapshot.Modules) - 1; i >= 0; i-- {
		proxyResp, err = snapshot.Modules[i].WrapResponse(ctx, proxyReq, proxyResp)
		if err != nil {
			_ = upstreamHTTPResp.Body.Close()
			w.logger.ErrorContext(ctx, logging.EventModuleFail,
				"module", snapshot.Modules[i].Name(),
				"method", proxyReq.Method,
				"path", proxyReq.Path,
				"phase", "wrap_response",
				"err", err.Error(),
			)
			return responseCopyResult{}, err
		}
	}

	err = copyProxyResponse(ctx, rw, proxyResp)
	return observedBody.usageResult(), err
}

func readRequestBody(r *http.Request) ([]byte, string, error) {
	if r.Body == nil {
		return nil, r.Header.Get("Content-Type"), nil
	}
	defer r.Body.Close()

	var reader io.Reader = r.Body
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case contentEncodingGzip:
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, "", err
		}
		defer gz.Close()
		reader = gz
	case contentEncodingDeflate:
		fl := flate.NewReader(r.Body)
		defer fl.Close()
		reader = fl
	case contentEncodingZstd:
		zr, err := zstd.NewReader(r.Body)
		if err != nil {
			return nil, "", err
		}
		defer zr.Close()
		reader = zr
	default:
		return nil, "", fmt.Errorf("unsupported content encoding %q", r.Header.Get("Content-Encoding"))
	}
	body, err := io.ReadAll(reader)
	return body, r.Header.Get("Content-Type"), err
}

type responseCopyResult struct {
	Usage   UsageTokens
	Model   string
	pending <-chan responseUsageMetadata
}

func copyProxyResponse(ctx context.Context, rw http.ResponseWriter, resp *module.ProxyResponse) error {
	defer resp.Body.Close()
	for key, values := range resp.Headers {
		for _, value := range values {
			rw.Header().Add(key, value)
		}
	}
	if resp.StatusCode != 0 {
		rw.WriteHeader(resp.StatusCode)
	}

	flusher, _ := rw.(http.Flusher)
	buf := make([]byte, proxyResponseBufferSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := rw.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
