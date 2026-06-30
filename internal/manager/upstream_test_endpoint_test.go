package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestManagerAPIUpstreamTestProbesReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(Config{
		Config: config.Config{
			Upstreams: map[string]config.UpstreamProfile{
				"groq": {BaseURL: server.URL, APIKey: "sk-test"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/groq/test", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var got upstreamProbeResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	got.LatencyMS = 0
	want := upstreamProbeResponse{Upstream: "groq", OK: true, StatusCode: http.StatusOK}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	events := m.events.Replay(0)
	found := false
	for _, e := range events {
		if e.Type == EventUpstreamProbed {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected upstream.probed event, got events: %+v", events)
	}
}

func TestManagerAPIUpstreamTestUnknownReturns404(t *testing.T) {
	m := New(Config{Config: config.Config{}})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/unknown/test", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "upstream not found") {
		t.Fatalf("expected not found error, got: %s", res.Body.String())
	}
}

func TestManagerAPIUpstreamTestAllProbesAllUpstreams(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer serverB.Close()

	m := New(Config{
		Config: config.Config{
			Upstreams: map[string]config.UpstreamProfile{
				"alpha": {BaseURL: serverA.URL},
				"beta":  {BaseURL: serverB.URL},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/upstreams/test", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Results []upstreamProbeResponse `json:"results"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	results := body.Results
	for i := range results {
		results[i].LatencyMS = 0
	}
	want := []upstreamProbeResponse{
		{Upstream: "alpha", OK: true, StatusCode: http.StatusOK},
		{Upstream: "beta", OK: false, StatusCode: http.StatusUnauthorized, Error: "auth_error"},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("got %+v, want %+v", results, want)
	}
}
