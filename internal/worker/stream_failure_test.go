package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

const streamFailureTestTimeout = 20 * time.Millisecond

var errStreamFailureTestReset = errors.New("upstream reset")
var errStreamFailureTestModule = errors.New("response module failed")
var errStreamFailureTestDownstream = errors.New("downstream write failed")

func TestWorkerMetricCarriesSnapshotGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	metrics := &concurrencyDetectingWriter{}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 7,
			Upstream:   upstream.RuntimeUpstream{Name: "primary", BaseURL: server.URL},
		},
		MetricsWriter: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	workerInstance.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)))
	var got RequestMetricEvent
	if err := json.Unmarshal(waitForMetricEvents(t, metrics, 1), &got); err != nil {
		t.Fatal(err)
	}
	want := RequestMetricEvent{
		Timestamp:          got.Timestamp,
		SnapshotGeneration: 7,
		Upstream:           "primary",
		Method:             http.MethodPost,
		Path:               "/v1/responses",
		Status:             http.StatusOK,
		DurationMS:         got.DurationMS,
		ResponseBytes:      2,
		Usage:              UsageTokens{Known: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected metric event:\n got %#v\nwant %#v", got, want)
	}
}

func TestWorkerFirstByteTimeoutIncludesResponseHeaders(t *testing.T) {
	client := &http.Client{Transport: streamFailureRoundTripper(func(request *http.Request) (*http.Response, error) {
		select {
		case <-request.Context().Done():
			return nil, context.Cause(request.Context())
		case <-time.After(5 * streamFailureTestTimeout):
			return nil, errStreamFailureTestReset
		}
	})}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation:     1,
			StreamTimeouts: appruntime.StreamTimeouts{FirstByteMilliseconds: streamFailureTestTimeout.Milliseconds()},
			Upstream:       upstream.RuntimeUpstream{Name: "primary", BaseURL: "https://primary.example"},
		},
		Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, requestErr := workerInstance.proxyRequest(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)),
		workerInstance.snapshots.Load(),
	)
	got := struct {
		Failure *UpstreamFailure
		ErrKind string
	}{Failure: result.Failure}
	if errors.Is(requestErr, ErrStreamFirstByteTimeout) {
		got.ErrKind = string(UpstreamFailureFirstByteTimeout)
	}
	want := struct {
		Failure *UpstreamFailure
		ErrKind string
	}{
		Failure: &UpstreamFailure{Kind: UpstreamFailureFirstByteTimeout, BeforeFirstByte: true},
		ErrKind: string(UpstreamFailureFirstByteTimeout),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected header timeout outcome:\n got %#v\nwant %#v", got, want)
	}
}

func TestWorkerCallerCancellationWinsFirstByteTimeout(t *testing.T) {
	parentContext, cancelParent := context.WithCancel(t.Context())
	client := &http.Client{Transport: streamFailureRoundTripper(func(request *http.Request) (*http.Response, error) {
		select {
		case <-request.Context().Done():
			cancelParent()
			<-parentContext.Done()
			return nil, context.Cause(request.Context())
		case <-time.After(5 * streamFailureTestTimeout):
			cancelParent()
			return nil, errStreamFailureTestReset
		}
	})}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation:     1,
			StreamTimeouts: appruntime.StreamTimeouts{FirstByteMilliseconds: streamFailureTestTimeout.Milliseconds()},
			Upstream:       upstream.RuntimeUpstream{Name: "primary", BaseURL: "https://primary.example"},
		},
		Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)).WithContext(parentContext)
	started := time.Now()
	result, _ := workerInstance.proxyRequest(httptest.NewRecorder(), request, workerInstance.snapshots.Load())
	got := struct {
		Failure *UpstreamFailure
		ErrKind string
		Timely  bool
	}{Failure: result.Failure, Timely: time.Since(started) < 4*streamFailureTestTimeout}
	if parentContext.Err() != nil {
		got.ErrKind = "caller_canceled"
	}
	want := struct {
		Failure *UpstreamFailure
		ErrKind string
		Timely  bool
	}{Failure: nil, ErrKind: "caller_canceled", Timely: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected caller cancellation outcome:\n got %#v\nwant %#v", got, want)
	}
}

func TestWorkerStreamDeadlineCoversResponseModules(t *testing.T) {
	body := newStreamFailureBlockingBody()
	client := &http.Client{Transport: streamFailureRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
		}, nil
	})}
	workerInstance, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation:     1,
			StreamTimeouts: appruntime.StreamTimeouts{FirstByteMilliseconds: streamFailureTestTimeout.Milliseconds()},
			Upstream:       upstream.RuntimeUpstream{Name: "primary", BaseURL: "https://primary.example"},
			Modules:        []module.Middleware{streamFailureMiddleware{mode: streamFailureModuleConsume}},
		},
		Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	result, requestErr := workerInstance.proxyRequest(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)),
		workerInstance.snapshots.Load(),
	)
	got := struct {
		Failure *UpstreamFailure
		ErrKind string
		Timely  bool
	}{Failure: result.Failure, Timely: time.Since(started) < 4*streamFailureTestTimeout}
	if errors.Is(requestErr, ErrStreamFirstByteTimeout) {
		got.ErrKind = string(UpstreamFailureFirstByteTimeout)
	}
	want := struct {
		Failure *UpstreamFailure
		ErrKind string
		Timely  bool
	}{
		Failure: &UpstreamFailure{Kind: UpstreamFailureFirstByteTimeout, BeforeFirstByte: true},
		ErrKind: string(UpstreamFailureFirstByteTimeout),
		Timely:  true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected module stream timeout outcome:\n got %#v\nwant %#v", got, want)
	}
}

func TestWorkerClassifiesUpstreamBodyReadFailures(t *testing.T) {
	tests := []struct {
		name            string
		body            io.ReadCloser
		moduleMode      streamFailureModuleMode
		wantBeforeFirst bool
	}{
		{
			name:            "copy before first byte",
			body:            &streamFailureReadErrorBody{},
			wantBeforeFirst: true,
		},
		{
			name:            "copy after first byte",
			body:            &streamFailureReadErrorBody{data: []byte("partial")},
			wantBeforeFirst: false,
		},
		{
			name:            "module before first byte",
			body:            &streamFailureReadErrorBody{},
			moduleMode:      streamFailureModuleConsume,
			wantBeforeFirst: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: streamFailureRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: test.body}, nil
			})}
			snapshot := RuntimeConfigSnapshot{
				Generation: 1,
				Upstream:   upstream.RuntimeUpstream{Name: "primary", BaseURL: "https://primary.example"},
			}
			if test.moduleMode != streamFailureModulePass {
				snapshot.Modules = []module.Middleware{streamFailureMiddleware{mode: test.moduleMode}}
			}
			workerInstance, err := New(Options{Snapshot: snapshot, Client: client})
			if err != nil {
				t.Fatal(err)
			}

			result, requestErr := workerInstance.proxyRequest(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)),
				workerInstance.snapshots.Load(),
			)
			got := struct {
				Failure *UpstreamFailure
				ErrKind string
			}{Failure: result.Failure}
			if errors.Is(requestErr, errStreamFailureTestReset) {
				got.ErrKind = "upstream_reset"
			}
			want := struct {
				Failure *UpstreamFailure
				ErrKind string
			}{
				Failure: &UpstreamFailure{Kind: UpstreamFailureTransport, BeforeFirstByte: test.wantBeforeFirst},
				ErrKind: "upstream_reset",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected upstream body failure:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

func TestWorkerIgnoresDownstreamAndModuleErrors(t *testing.T) {
	tests := []struct {
		name       string
		moduleMode streamFailureModuleMode
		writer     http.ResponseWriter
		wantErr    error
	}{
		{
			name:    "downstream writer",
			writer:  &streamFailureErrorWriter{header: make(http.Header)},
			wantErr: errStreamFailureTestDownstream,
		},
		{
			name:       "response module",
			moduleMode: streamFailureModuleError,
			writer:     httptest.NewRecorder(),
			wantErr:    errStreamFailureTestModule,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: streamFailureRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("response")),
				}, nil
			})}
			snapshot := RuntimeConfigSnapshot{
				Generation: 1,
				Upstream:   upstream.RuntimeUpstream{Name: "primary", BaseURL: "https://primary.example"},
			}
			if test.moduleMode != streamFailureModulePass {
				snapshot.Modules = []module.Middleware{streamFailureMiddleware{mode: test.moduleMode}}
			}
			workerInstance, err := New(Options{Snapshot: snapshot, Client: client})
			if err != nil {
				t.Fatal(err)
			}

			result, requestErr := workerInstance.proxyRequest(
				test.writer,
				httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{}`)),
				workerInstance.snapshots.Load(),
			)
			got := struct {
				Failure *UpstreamFailure
				Err     error
			}{Failure: result.Failure, Err: requestErr}
			want := struct {
				Failure *UpstreamFailure
				Err     error
			}{Failure: nil, Err: test.wantErr}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected local processing failure:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

type streamFailureRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip streamFailureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type streamFailureReadErrorBody struct {
	data []byte
	read bool
}

func (body *streamFailureReadErrorBody) Read(buffer []byte) (int, error) {
	if body.read {
		return 0, io.EOF
	}
	body.read = true
	return copy(buffer, body.data), errStreamFailureTestReset
}

func (*streamFailureReadErrorBody) Close() error { return nil }

type streamFailureBlockingBody struct {
	closed chan struct{}
	once   sync.Once
}

func newStreamFailureBlockingBody() *streamFailureBlockingBody {
	return &streamFailureBlockingBody{closed: make(chan struct{})}
}

func (body *streamFailureBlockingBody) Read([]byte) (int, error) {
	select {
	case <-body.closed:
		return 0, errStreamFailureTestReset
	case <-time.After(5 * streamFailureTestTimeout):
		return 0, errStreamFailureTestReset
	}
}

func (body *streamFailureBlockingBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

type streamFailureModuleMode int

const (
	streamFailureModulePass streamFailureModuleMode = iota
	streamFailureModuleConsume
	streamFailureModuleError
)

type streamFailureMiddleware struct {
	mode streamFailureModuleMode
}

func (streamFailureMiddleware) Name() string { return "stream_failure_test" }

func (streamFailureMiddleware) ProcessRequest(context.Context, *module.ProxyRequest) error {
	return nil
}

func (middleware streamFailureMiddleware) WrapResponse(_ context.Context, _ *module.ProxyRequest, response *module.ProxyResponse) (*module.ProxyResponse, error) {
	switch middleware.mode {
	case streamFailureModuleConsume:
		_, err := io.Copy(io.Discard, response.Body)
		return response, err
	case streamFailureModuleError:
		return nil, errStreamFailureTestModule
	default:
		return response, nil
	}
}

func (streamFailureMiddleware) Config() module.ModuleConfig { return module.ModuleConfig{} }

func (streamFailureMiddleware) UpdateConfig(module.ModuleConfig) error { return nil }

func (streamFailureMiddleware) RequestBodyMode(module.ProxyRequestMeta) module.RequestBodyMode {
	return module.RequestBodyStream
}

type streamFailureErrorWriter struct {
	header http.Header
}

func (writer *streamFailureErrorWriter) Header() http.Header { return writer.header }

func (*streamFailureErrorWriter) WriteHeader(int) {}

func (*streamFailureErrorWriter) Write([]byte) (int, error) {
	return 0, errStreamFailureTestDownstream
}
