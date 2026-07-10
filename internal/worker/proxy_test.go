package worker

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestWorkerPassesThroughWithNoModulesAndInjectsAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.URL.RawQuery != "x=1" {
			t.Fatalf("unexpected server URL %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("authorization was not injected: %q", r.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"body": string(body),
		})
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL, APIKey: "test-secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses?x=1", strings.NewReader(`{"input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"body":"{\"input\":\"hello\"}"`) {
		t.Fatalf("unexpected response body %s", res.Body.String())
	}
}

func TestWorkerProxiesNonGetStatusAlias(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/__ainn/status", strings.NewReader("body")))

	if res.Code != http.StatusCreated || res.Body.String() != "proxied" || receivedPath != "/__ainn/status" {
		t.Fatalf("status alias was not proxied: status=%d body=%q path=%q", res.Code, res.Body.String(), receivedPath)
	}
}

func TestWorkerRecordsMetricsAndWritesCompletionEvent(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-5",
			"usage": map[string]any{"input_tokens": 12, "output_tokens": 8},
		})
	}))
	defer upstreamServer.Close()

	metrics := &concurrencyDetectingWriter{}
	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	wantSnapshot := MetricsSnapshot{
		WindowSeconds: MetricsWindowSeconds,
		Requests:      1,
		RPM:           1,
		TPM:           20,
		AvgLatencyMS:  0,
		InputTokens:   12,
		OutputTokens:  8,
		TotalTokens:   20,
	}
	gotSnapshot := w.MetricsSnapshot()
	wantSnapshot.AvgLatencyMS = gotSnapshot.AvgLatencyMS
	if gotSnapshot != wantSnapshot {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", gotSnapshot, wantSnapshot)
	}
	var event RequestMetricEvent
	if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
		t.Fatal(err)
	}
	if event.Method != http.MethodPost || event.Path != "/v1/responses" || event.Status != http.StatusOK || event.Model != "gpt-5" || event.Usage.TotalTokens != 20 {
		t.Fatalf("bad metrics event: %#v", event)
	}
}

func TestWorkerMetricsWriterDoesNotBlockRequestCompletion(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-5","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	metrics := &blockingMetricsWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		workerInstance.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)),
		)
		close(done)
	}()

	select {
	case <-metrics.entered:
	case <-time.After(time.Second):
		t.Fatal("metrics writer was not called")
	}
	select {
	case <-done:
		close(metrics.release)
	case <-time.After(100 * time.Millisecond):
		close(metrics.release)
		<-done
		t.Fatal("request completion waited for the metrics writer")
	}
}

func TestWorkerMetricsEmitterReportsDroppedEvents(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstreamServer.Close()

	metrics := &blockingMetricsWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(metrics.release)
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	workerInstance.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://proxy.local/v1/models", nil))
	select {
	case <-metrics.entered:
	case <-time.After(time.Second):
		t.Fatal("metrics writer was not called")
	}
	for range metricsEventQueueSize + 1 {
		workerInstance.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://proxy.local/v1/models", nil))
	}

	if got := workerInstance.MetricsSnapshot().DroppedEvents; got != 1 {
		t.Fatalf("bad dropped event count: got %d want 1", got)
	}
}

func TestWorkerRecordsRawChatCompletionUsageBeforeTranslation(t *testing.T) {
	const model = "gpt-5-mini"
	wantMetrics := struct {
		Model string
		Usage UsageTokens
	}{
		Model: model,
		Usage: UsageTokens{Known: true, InputTokens: 11, OutputTokens: 7, CacheReadTokens: 4, ReasoningTokens: 2, TotalTokens: 18},
	}
	tests := []struct {
		name             string
		contentType      string
		responseBody     string
		requestBody      string
		translatedMarker string
		rawObject        string
	}{
		{
			name:             "json",
			contentType:      "application/json",
			responseBody:     `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5-mini","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`,
			requestBody:      `{"model":"gpt-5-mini","input":"hello"}`,
			translatedMarker: "event: response.completed",
			rawObject:        `"object":"chat.completion"`,
		},
		{
			name:             "sse",
			contentType:      "text/event-stream",
			responseBody:     "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5-mini\",\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5-mini\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18,\"prompt_tokens_details\":{\"cached_tokens\":4},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\ndata: [DONE]\n\n",
			requestBody:      `{"model":"gpt-5-mini","input":"hello","stream":true}`,
			translatedMarker: "event: response.output_text.delta",
			rawObject:        `"object":"chat.completion.chunk"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Fatalf("unexpected upstream path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(w, test.responseBody)
			}))
			defer upstreamServer.Close()

			metrics := &concurrencyDetectingWriter{}
			workerInstance, err := New(Options{
				Runtime: appruntime.WorkerRuntime{
					ID:         "cli-openai",
					Generation: 1,
					Upstream: appruntime.UpstreamRuntime{
						ID:        "openai",
						BaseURL:   upstreamServer.URL,
						APIFormat: appruntime.APIFormatChatCompletions,
					},
					Modules: map[string]appruntime.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
				MetricsWriter: metrics,
			})
			if err != nil {
				t.Fatal(err)
			}

			req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(test.requestBody))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			workerInstance.ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Header().Get("Content-Type"), "text/event-stream") ||
				!strings.Contains(res.Body.String(), test.translatedMarker) ||
				strings.Contains(res.Body.String(), test.rawObject) {
				t.Fatalf("client response was not translated: headers=%v body=%s", res.Header(), res.Body.String())
			}

			var event RequestMetricEvent
			if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
				t.Fatal(err)
			}
			gotMetrics := struct {
				Model string
				Usage UsageTokens
			}{event.Model, event.Usage}
			if gotMetrics != wantMetrics {
				t.Fatalf("bad metrics event:\ngot  %#v\nwant %#v", gotMetrics, wantMetrics)
			}
		})
	}
}

func TestWorkerMetricsIgnoresTransformedResponseUsage(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstreamServer.Close()

	metrics := &concurrencyDetectingWriter{}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
			Modules:    []module.Middleware{transformedUsageMiddleware{}},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	workerInstance.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)))
	if res.Body.String() != `{"model":"transformed","usage":{"input_tokens":50,"output_tokens":25}}` {
		t.Fatalf("unexpected transformed response %q", res.Body.String())
	}

	var event RequestMetricEvent
	if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
		t.Fatal(err)
	}
	if event.Model != "" || event.Usage != (UsageTokens{Known: false}) {
		t.Fatalf("metrics used transformed response usage: %#v", event)
	}
}

func TestWorkerMetricsRecordsUsageFromCompressedUpstreamResponses(t *testing.T) {
	const model = "gpt-5-mini"
	wantMetrics := struct {
		Model string
		Usage UsageTokens
	}{
		Model: model,
		Usage: UsageTokens{Known: true, InputTokens: 11, OutputTokens: 7, CacheReadTokens: 4, ReasoningTokens: 2, TotalTokens: 18},
	}
	responses := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "json",
			contentType: "application/json",
			body:        `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-5-mini","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`,
		},
		{
			name:        "sse",
			contentType: "text/event-stream",
			body:        "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5-mini\",\"choices\":[]}\n\ndata: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5-mini\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18,\"prompt_tokens_details\":{\"cached_tokens\":4},\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\ndata: [DONE]\n\n",
		},
	}
	for _, response := range responses {
		for _, encoding := range []string{"gzip", "deflate", "zstd"} {
			t.Run(response.name+"/"+encoding, func(t *testing.T) {
				compressed := compressProxyResponse(t, encoding, []byte(response.body))
				upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got := r.Header.Get("Accept-Encoding"); got != encoding {
						t.Fatalf("unexpected accept encoding %q", got)
					}
					w.Header().Set("Content-Type", response.contentType)
					w.Header().Set("Content-Encoding", encoding)
					w.Header().Set("X-Upstream-Response", "preserved")
					w.WriteHeader(http.StatusCreated)
					midpoint := len(compressed) / 2
					_, _ = w.Write(compressed[:midpoint])
					w.(http.Flusher).Flush()
					_, _ = w.Write(compressed[midpoint:])
				}))
				defer upstreamServer.Close()

				metrics := &concurrencyDetectingWriter{}
				workerInstance, err := New(Options{
					Snapshot: RuntimeConfigSnapshot{
						Generation: 1,
						Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
					},
					MetricsWriter: metrics,
				})
				if err != nil {
					t.Fatal(err)
				}

				req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5-mini"}`))
				req.Header.Set("Accept-Encoding", encoding)
				res := httptest.NewRecorder()
				workerInstance.ServeHTTP(res, req)

				gotDelivery := struct {
					Status          int
					ContentType     string
					ContentEncoding string
					Marker          string
					Body            []byte
					Flushed         bool
				}{
					Status:          res.Code,
					ContentType:     res.Header().Get("Content-Type"),
					ContentEncoding: res.Header().Get("Content-Encoding"),
					Marker:          res.Header().Get("X-Upstream-Response"),
					Body:            res.Body.Bytes(),
					Flushed:         res.Flushed,
				}
				wantDelivery := struct {
					Status          int
					ContentType     string
					ContentEncoding string
					Marker          string
					Body            []byte
					Flushed         bool
				}{
					Status:          http.StatusCreated,
					ContentType:     response.contentType,
					ContentEncoding: encoding,
					Marker:          "preserved",
					Body:            compressed,
					Flushed:         true,
				}
				if !reflect.DeepEqual(gotDelivery, wantDelivery) {
					t.Fatalf("compressed response delivery changed:\ngot  %#v\nwant %#v", gotDelivery, wantDelivery)
				}

				var event RequestMetricEvent
				if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
					t.Fatal(err)
				}
				gotMetrics := struct {
					Model string
					Usage UsageTokens
				}{event.Model, event.Usage}
				if gotMetrics != wantMetrics {
					t.Fatalf("bad compressed metrics event:\ngot  %#v\nwant %#v", gotMetrics, wantMetrics)
				}
				gotSnapshot := workerInstance.MetricsSnapshot()
				wantSnapshot := MetricsSnapshot{
					WindowSeconds:   MetricsWindowSeconds,
					Requests:        1,
					RPM:             1,
					TPM:             18,
					AvgLatencyMS:    gotSnapshot.AvgLatencyMS,
					InputTokens:     11,
					OutputTokens:    7,
					CacheReadTokens: 4,
					ReasoningTokens: 2,
					TotalTokens:     18,
				}
				if gotSnapshot != wantSnapshot {
					t.Fatalf("compressed request was not counted exactly once:\ngot  %#v\nwant %#v", gotSnapshot, wantSnapshot)
				}
			})
		}
	}
}

func TestUsageObservingReadCloserDoesNotWaitForSlowCompressedObservation(t *testing.T) {
	const compressedBodySize = 2 * 1024 * 1024
	body := make([]byte, compressedBodySize)
	state := uint32(1)
	for i := range body {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		body[i] = byte(state)
	}
	compressed := compressProxyResponse(t, contentEncodingGzip, body)
	observer := &blockingUsageObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	observed := newUsageObservingReadCloser(io.NopCloser(bytes.NewReader(compressed)), contentEncodingGzip, observer)

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, observed)
		if closeErr := observed.Close(); err == nil {
			err = closeErr
		}
		done <- err
	}()

	select {
	case <-observer.entered:
	case <-time.After(time.Second):
		t.Fatal("compressed observation did not start")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		close(observer.release)
		<-done
		t.Fatal("raw compressed copy waited for metrics processing")
	}

	result := observed.usageResult()
	if result.pending == nil {
		t.Fatal("compressed usage result was not asynchronous")
	}
	close(observer.release)
	metadata := <-result.pending
	if metadata != (responseUsageMetadata{Usage: UsageTokens{Known: false}}) {
		t.Fatalf("truncated compressed observation should be unknown: %#v", metadata)
	}
}

func TestWorkerMetricsPreservesResponseWhenDecompressionFails(t *testing.T) {
	body := []byte("not a gzip stream")
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(body)
	}))
	defer upstreamServer.Close()

	metrics := &concurrencyDetectingWriter{}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	workerInstance.ServeHTTP(res, req)

	gotDelivery := struct {
		Status          int
		ContentEncoding string
		Body            []byte
	}{res.Code, res.Header().Get("Content-Encoding"), res.Body.Bytes()}
	wantDelivery := struct {
		Status          int
		ContentEncoding string
		Body            []byte
	}{http.StatusAccepted, "gzip", body}
	if !reflect.DeepEqual(gotDelivery, wantDelivery) {
		t.Fatalf("decode failure changed proxy delivery:\ngot  %#v\nwant %#v", gotDelivery, wantDelivery)
	}

	var event RequestMetricEvent
	if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
		t.Fatal(err)
	}
	if event.Model != "" || event.Usage != (UsageTokens{}) {
		t.Fatalf("decode failure should leave usage unknown: %#v", event)
	}
}

func compressProxyResponse(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&compressed)
	case "deflate":
		writer = zlib.NewWriter(&compressed)
	case "zstd":
		var err error
		writer, err = zstd.NewWriter(&compressed)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported test encoding %q", encoding)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestWorkerSerializesConcurrentMetricsWrites(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-5","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	metrics := &concurrencyDetectingWriter{}
	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: upstreamServer.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	const requestCount = 64
	var wg sync.WaitGroup
	wg.Add(requestCount)
	for range requestCount {
		go func() {
			defer wg.Done()
			res := httptest.NewRecorder()
			w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)))
			if res.Code != http.StatusOK {
				t.Errorf("unexpected status %d: %s", res.Code, res.Body.String())
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt32(&metrics.concurrent) != 0 {
		t.Fatal("metrics writer was entered concurrently")
	}
	lines := bytes.Split(waitForMetricEvents(t, metrics, requestCount), []byte("\n"))
	if len(lines) != requestCount {
		t.Fatalf("bad metrics line count: got %d want %d", len(lines), requestCount)
	}
	for _, line := range lines {
		var event RequestMetricEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("corrupt metrics line %q: %v", line, err)
		}
	}
}

type concurrencyDetectingWriter struct {
	active     int32
	concurrent int32
	mu         sync.Mutex
	buf        bytes.Buffer
}

type blockingMetricsWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type blockingUsageObserver struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (o *blockingUsageObserver) Observe([]byte) {
	o.once.Do(func() { close(o.entered) })
	<-o.release
}

func (o *blockingUsageObserver) Finish() UsageTokens {
	return UsageTokens{Known: true, TotalTokens: 1}
}

func (o *blockingUsageObserver) Model() string {
	return "blocked"
}

type transformedUsageMiddleware struct{}

func (transformedUsageMiddleware) Name() string { return "transformed_usage" }

func (transformedUsageMiddleware) ProcessRequest(context.Context, *module.ProxyRequest) error {
	return nil
}

func (transformedUsageMiddleware) WrapResponse(_ context.Context, _ *module.ProxyRequest, upstream *module.ProxyResponse) (*module.ProxyResponse, error) {
	_, err := io.Copy(io.Discard, upstream.Body)
	if err != nil {
		return nil, err
	}
	if err := upstream.Body.Close(); err != nil {
		return nil, err
	}
	upstream.Body = io.NopCloser(strings.NewReader(`{"model":"transformed","usage":{"input_tokens":50,"output_tokens":25}}`))
	return upstream, nil
}

func (transformedUsageMiddleware) Config() module.ModuleConfig { return module.ModuleConfig{} }

func (transformedUsageMiddleware) UpdateConfig(module.ModuleConfig) error { return nil }

func (transformedUsageMiddleware) RequestBodyMode(module.ProxyRequestMeta) module.RequestBodyMode {
	return module.RequestBodyStream
}

func (w *blockingMetricsWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func (w *concurrencyDetectingWriter) Write(p []byte) (int, error) {
	if atomic.AddInt32(&w.active, 1) > 1 {
		atomic.StoreInt32(&w.concurrent, 1)
	}
	time.Sleep(time.Millisecond)
	defer atomic.AddInt32(&w.active, -1)

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *concurrencyDetectingWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

func waitForMetricEvents(t *testing.T, metrics *concurrencyDetectingWriter, count int) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		data := bytes.TrimSpace(metrics.Bytes())
		if len(data) > 0 && len(bytes.Split(data, []byte("\n"))) >= count {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d metrics events", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkerRecordsFailedUpstreamMetricsAndUnknownUsage(t *testing.T) {
	metrics := &concurrencyDetectingWriter{}
	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: "http://127.0.0.1:1"},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"model":"gpt-5"}`)))
	if res.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	wantSnapshot := MetricsSnapshot{
		WindowSeconds:        MetricsWindowSeconds,
		Requests:             1,
		Errors:               1,
		RPM:                  1,
		UnknownUsageRequests: 1,
	}
	gotSnapshot := w.MetricsSnapshot()
	wantSnapshot.AvgLatencyMS = gotSnapshot.AvgLatencyMS
	if gotSnapshot != wantSnapshot {
		t.Fatalf("bad metrics snapshot:\ngot  %#v\nwant %#v", gotSnapshot, wantSnapshot)
	}
	var event RequestMetricEvent
	if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &event); err != nil {
		t.Fatal(err)
	}
	if event.Method != http.MethodPost || event.Path != "/v1/responses" || event.Status != http.StatusBadGateway || event.Usage.Known {
		t.Fatalf("bad metrics event: %#v", event)
	}
}

func TestWorkerRoutesUpstreamRequestThroughProxyURL(t *testing.T) {
	received := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"source":"proxy"}`))
	}))
	defer proxy.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			ProxyURL:   proxy.URL,
			Upstream:   upstream.RuntimeUpstream{BaseURL: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://worker.local/v1/responses?x=1", nil)
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted || res.Body.String() != `{"source":"proxy"}` {
		t.Fatalf("unexpected worker response: status=%d body=%q", res.Code, res.Body.String())
	}
	select {
	case got := <-received:
		want := "http://127.0.0.1:1/v1/responses?x=1"
		if got != want {
			t.Fatalf("proxy received URL %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive upstream request")
	}
}

func TestWorkerRejectsInvalidProxyURL(t *testing.T) {
	_, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			ProxyURL:   "localhost:7890",
			Upstream:   upstream.RuntimeUpstream{BaseURL: "http://127.0.0.1:1"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "proxy_url") {
		t.Fatalf("expected proxy_url error, got %v", err)
	}
}

func TestWorkerKeepsProxyClientFromStartingSnapshot(t *testing.T) {
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(firstReady)
		<-releaseFirst
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
	}))
	defer firstProxy.Close()
	secondProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("second"))
	}))
	defer secondProxy.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			ProxyURL:   firstProxy.URL,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: "http://127.0.0.1:1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	go func() {
		res := httptest.NewRecorder()
		w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/v1/responses", nil))
		result <- res.Body.String()
	}()

	select {
	case <-firstReady:
	case <-time.After(time.Second):
		t.Fatal("first proxy did not receive request")
	}

	_, err = w.UpdateRuntime(appruntime.WorkerRuntime{
		ID:         "cli-openai",
		Generation: 2,
		ProxyURL:   secondProxy.URL,
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "http://127.0.0.1:1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)

	select {
	case got := <-result:
		if got != "first" {
			t.Fatalf("in-flight request used changed proxy client: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/v1/responses", nil))
	if res.Body.String() != "second" {
		t.Fatalf("new request did not use updated proxy client: %q", res.Body.String())
	}
}

func TestWorkerDecompressesEncodedRequestWithoutModules(t *testing.T) {
	type upstreamRequest struct {
		Body            string
		ContentEncoding string
	}
	received := upstreamRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = upstreamRequest{
			Body:            string(body),
			ContentEncoding: r.Header.Get("Content-Encoding"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte(`{"input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", &compressed)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if received != (upstreamRequest{Body: `{"input":"hello"}`}) {
		t.Fatalf("unexpected upstream request %#v", received)
	}
}

func TestWorkerUsesOneSnapshotForWholeRequest(t *testing.T) {
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(firstReady)
		<-releaseFirst
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("second"))
	}))
	defer second.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: first.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	go func() {
		res := httptest.NewRecorder()
		w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/stream", nil))
		result <- res.Body.String()
	}()

	select {
	case <-firstReady:
	case <-time.After(time.Second):
		t.Fatal("first server did not receive request")
	}

	if err := w.UpdateSnapshot(RuntimeConfigSnapshot{
		Generation: 2,
		Upstream:   upstream.RuntimeUpstream{BaseURL: second.URL},
	}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)

	select {
	case got := <-result:
		if got != "first" {
			t.Fatalf("in-flight request used changed snapshot: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/stream", nil))
	if res.Body.String() != "second" {
		t.Fatalf("new request did not use new snapshot: %q", res.Body.String())
	}
}

func TestWorkerMetricsEventsKeepRequestSnapshotUpstream(t *testing.T) {
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(firstReady)
		<-releaseFirst
		w.WriteHeader(http.StatusOK)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer second.Close()

	metrics := &concurrencyDetectingWriter{}
	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{Name: "openai", BaseURL: first.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct{})
	go func() {
		res := httptest.NewRecorder()
		w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", nil))
		close(firstDone)
	}()

	select {
	case <-firstReady:
	case <-time.After(time.Second):
		t.Fatal("first server did not receive request")
	}

	if err := w.UpdateSnapshot(RuntimeConfigSnapshot{
		Generation: 2,
		Upstream:   upstream.RuntimeUpstream{Name: "anthropic", BaseURL: second.URL},
	}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/messages", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	lines := bytes.Split(waitForMetricEvents(t, metrics, 2), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("bad metrics line count: got %d want 2", len(lines))
	}
	var got [2]string
	for i, line := range lines {
		var event struct {
			Upstream string `json:"upstream"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		got[i] = event.Upstream
	}
	want := [2]string{"openai", "anthropic"}
	if got != want {
		t.Fatalf("bad metrics upstreams: got %#v want %#v", got, want)
	}
}

func TestWorkerRunsModuleChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
			Modules: []module.Middleware{
				module.NewToolFilter(module.ModuleConfig{Enabled: true, Params: map[string]any{"blocked_tools": []any{"image_generation"}}}),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"tools":[{"type":"image_generation"},{"type":"function","name":"keep"}]}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if strings.Contains(res.Body.String(), "image_generation") {
		t.Fatalf("module chain did not filter body: %s", res.Body.String())
	}
}

func TestWorkerRunsExternalRequestMiddleware(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "external-filter")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
python3 -c 'import json,sys
payload=json.load(sys.stdin)
payload["headers"]["X-External"]=["yes"]
payload["headers"]["X-Original-Path"]=[payload.get("original_path","")]
payload["headers"]["X-Params-Mode"]=[payload.get("params",{}).get("mode","")]
payload["path"]="/rewritten"
payload["body"]="external:"+payload.get("body","")
json.dump(payload, sys.stdout)'
`), 0700); err != nil {
		t.Fatal(err)
	}

	var receivedBody string
	var receivedHeader string
	var receivedOriginalPath string
	var receivedParamsMode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rewritten" || r.URL.RawQuery != "x=1" {
			t.Fatalf("unexpected rewritten URL %s", r.URL.String())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		receivedBody = string(body)
		receivedHeader = r.Header.Get("X-External")
		receivedOriginalPath = r.Header.Get("X-Original-Path")
		receivedParamsMode = r.Header.Get("X-Params-Mode")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"external_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         script,
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"external_filter": {Enabled: true, Params: map[string]any{"mode": "strict"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses?x=1", &compressed)
	req.Header.Set("Content-Encoding", "zstd")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedHeader != "yes" || receivedBody != "external:hello" || receivedOriginalPath != "/v1/responses" || receivedParamsMode != "strict" {
		t.Fatalf("external middleware did not mutate request: header=%q body=%q original_path=%q params_mode=%q", receivedHeader, receivedBody, receivedOriginalPath, receivedParamsMode)
	}
}

func TestWorkerRunsExternalRequestMiddlewareWithArgs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "external-filter.py")
	if err := os.WriteFile(script, []byte(`import json,sys
payload=json.load(sys.stdin)
payload["headers"]["X-External-Args"]=["yes"]
json.dump(payload, sys.stdout)
`), 0600); err != nil {
		t.Fatal(err)
	}

	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-External-Args")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"external_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{script},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"external_filter": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader("hello")))

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedHeader != "yes" {
		t.Fatalf("external middleware args were not used: header=%q", receivedHeader)
	}
}

func TestWorkerExternalToolFilterPassesThroughNonJSONRequest(t *testing.T) {
	scriptPath := repoRequestPluginScript(t, "tool_filter")
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"tool_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{scriptPath},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"tool_filter": {Enabled: true, Params: map[string]any{"blocked_tools": []any{"image_generation"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/files", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedBody != "hello" {
		t.Fatalf("unexpected upstream body %q", receivedBody)
	}
}

func TestWorkerExternalModelOverridePassesThroughNonJSONRequest(t *testing.T) {
	scriptPath := repoRequestPluginScript(t, "model_override")
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"model_override": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{scriptPath},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-test"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/files", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedBody != "hello" {
		t.Fatalf("unexpected upstream body %q", receivedBody)
	}
}

func TestWorkerExternalRequestLogPassesThroughBinaryUpload(t *testing.T) {
	scriptPath := repoRequestPluginScript(t, "request_log")
	body := []byte{0xff, 0xfe, 0x00, 'a', 0x80}
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"request_log": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{scriptPath},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"request_log": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/files", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Equal(receivedBody, body) {
		t.Fatalf("unexpected upstream body %v", receivedBody)
	}
}

func TestWorkerExternalRequestLogPassesThroughCompressedBinaryUpload(t *testing.T) {
	scriptPath := repoRequestPluginScript(t, "request_log")
	body := []byte{0xff, 0xfe, 0x00, 'a', 0x80}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	type upstreamRequest struct {
		Body            []byte
		ContentEncoding string
	}
	received := upstreamRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received.Body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		received.ContentEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"request_log": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{scriptPath},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"request_log": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/files", bytes.NewReader(compressed.Bytes()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	req.Header.Set("Content-Encoding", "gzip")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if received.ContentEncoding != "" || !bytes.Equal(received.Body, body) {
		t.Fatalf("unexpected upstream request %#v", received)
	}
}

func TestWorkerPreservesQueryWhenExternalPluginOmitsRawQuery(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "external-filter.py")
	if err := os.WriteFile(script, []byte(`import json,sys
payload=json.load(sys.stdin)
payload.pop("raw_query", None)
payload["path"]="/rewritten"
json.dump(payload, sys.stdout)
`), 0600); err != nil {
		t.Fatal(err)
	}

	type upstreamRequest struct {
		Path     string
		RawQuery string
	}
	received := upstreamRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = upstreamRequest{
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"external_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{script},
					ProtocolVersion: "2",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"external_filter": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/files?x=1", strings.NewReader("hello"))
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if received != (upstreamRequest{Path: "/rewritten", RawQuery: "x=1"}) {
		t.Fatalf("unexpected upstream request %#v", received)
	}
}

func TestWorkerClearsContentEncodingAfterBufferingCompressedRequest(t *testing.T) {
	type upstreamRequest struct {
		Body            string
		ContentEncoding string
	}
	received := upstreamRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = upstreamRequest{
			Body:            string(body),
			ContentEncoding: r.Header.Get("Content-Encoding"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
			Modules: []module.Middleware{
				module.NewToolFilter(module.ModuleConfig{Enabled: true, Params: map[string]any{"blocked_tools": []any{"image_generation"}}}),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte(`{"input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", &compressed)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if received != (upstreamRequest{Body: `{"input":"hello"}`}) {
		t.Fatalf("unexpected upstream request %#v", received)
	}
}

func repoRequestPluginScript(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("worker proxy_test: caller path unavailable")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "plugins", "request", name, "plugin")
}

func TestCopyResponseSkipsEmptyReads(t *testing.T) {
	writer := &recordingResponseWriter{header: http.Header{}}
	body := &emptyThenDataReadCloser{data: []byte("ok")}
	resp := &module.ProxyResponse{
		StatusCode: http.StatusAccepted,
		Headers:    http.Header{"X-Test": []string{"1"}},
		Body:       body,
	}

	err := copyProxyResponse(context.Background(), writer, resp)
	if err != nil {
		t.Fatal(err)
	}
	if writer.emptyWriteCount != 0 || writer.flushCount != 1 || string(writer.body) != "ok" {
		t.Fatalf("bad copy behavior: writes=%d flushes=%d body=%q", writer.emptyWriteCount, writer.flushCount, writer.body)
	}
}

func TestCopyResponseFlushesThroughResponseRecorder(t *testing.T) {
	writer := &recordingResponseWriter{header: http.Header{}}
	rec := &responseRecorder{ResponseWriter: writer}
	resp := &module.ProxyResponse{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}

	err := copyProxyResponse(context.Background(), rec, resp)
	if err != nil {
		t.Fatal(err)
	}
	if writer.flushCount != 1 {
		t.Fatalf("expected recorder to forward flush, got %d", writer.flushCount)
	}
}

func TestWorkerLogsRequestStartAndDoneWithCorrelationID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, "detail", logging.ComponentWorkerProxy)

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL, APIKey: "test-key"},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	out := logBuf.String()
	if !strings.Contains(out, logging.EventRequestStart) {
		t.Fatalf("missing %s in log output: %s", logging.EventRequestStart, out)
	}
	if !strings.Contains(out, logging.EventRequestDone) {
		t.Fatalf("missing %s in log output: %s", logging.EventRequestDone, out)
	}

	// Both lines must share the same req= correlation id.
	var reqID string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, logging.EventRequestStart) {
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "req=") {
					reqID = field
				}
			}
		}
	}
	if reqID == "" {
		t.Fatalf("no req= field found in request.start line: %s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, logging.EventRequestDone) && !strings.Contains(line, reqID) {
			t.Fatalf("request.done line missing correlation id %q: %s", reqID, line)
		}
	}
}

func TestWorkerReusesInboundRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, "detail", logging.ComponentWorkerProxy)

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Request-Id", "inbound-abc123")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if !strings.Contains(logBuf.String(), "req=inbound-abc123") {
		t.Fatalf("inbound request id not propagated in logs: %s", logBuf.String())
	}
}

func TestWorkerLogsUpstreamFail(t *testing.T) {
	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, "detail", logging.ComponentWorkerProxy)

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: "http://127.0.0.1:1"},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if !strings.Contains(logBuf.String(), logging.EventUpstreamFail) {
		t.Fatalf("missing %s in log output: %s", logging.EventUpstreamFail, logBuf.String())
	}
}

func TestWorkerLogsBadGatewayStatusOnUpstreamFail(t *testing.T) {
	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, "detail", logging.ComponentWorkerProxy)

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: "http://127.0.0.1:1"},
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("got response status %d, want %d", res.Code, http.StatusBadGateway)
	}
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, logging.EventRequestDone) && strings.Contains(line, "status=502") {
			return
		}
	}
	t.Fatalf("missing request.done status=502 in log output: %s", logBuf.String())
}

func TestNewWithNilLoggerDoesNotWriteStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = stdoutWriter
	worker, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
		},
	})
	os.Stdout = originalStdout
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	res := httptest.NewRecorder()
	worker.ServeHTTP(res, req)
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != "" {
		t.Fatalf("nil logger wrote to stdout: %q", string(out))
	}
}

func TestWorkerLogsSnapshotReloadOnUpdateRuntime(t *testing.T) {
	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, "detail", logging.ComponentWorkerProxy)

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{Generation: 1,
			Upstream: upstream.RuntimeUpstream{BaseURL: "http://localhost:9999"}},
		Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.UpdateRuntime(appruntime.WorkerRuntime{
		Upstream: appruntime.UpstreamRuntime{
			ID:        "openai",
			BaseURL:   "http://localhost:9999",
			APIFormat: "openai",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(logBuf.String(), logging.EventSnapshotReload) {
		t.Fatalf("missing %s in log output: %s", logging.EventSnapshotReload, logBuf.String())
	}
}

// Ensure slog.Logger is passed through and not a nil-logger dependency.
var _ *slog.Logger = (*slog.Logger)(nil)

func TestNewRejectsInvalidRuntimeInsteadOfPanicking(t *testing.T) {
	worker, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: "https://api.openai.com/v1",
			},
			Hooks: map[string]appruntime.ModuleConfig{
				"unknown": {Enabled: true},
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if worker != nil {
		t.Fatalf("expected nil worker on error, got %#v", worker)
	}
}

type emptyThenDataReadCloser struct {
	data []byte
	read int
}

func (r *emptyThenDataReadCloser) Read(p []byte) (int, error) {
	r.read++
	switch r.read {
	case 1:
		return 0, nil
	case 2:
		return copy(p, r.data), io.EOF
	default:
		return 0, io.EOF
	}
}

func (r *emptyThenDataReadCloser) Close() error {
	return nil
}

type recordingResponseWriter struct {
	header          http.Header
	status          int
	body            []byte
	emptyWriteCount int
	flushCount      int
}

func (w *recordingResponseWriter) Header() http.Header {
	return w.header
}

func (w *recordingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *recordingResponseWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		w.emptyWriteCount++
	}
	w.body = append(w.body, data...)
	return len(data), nil
}

func (w *recordingResponseWriter) Flush() {
	w.flushCount++
}
