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

func TestManagerUpstreamPoolCRUD(t *testing.T) {
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary":  {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"backup":   {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"tertiary": {BaseURL: "https://tertiary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	m.cancelProbes()
	defer m.Close()

	response := requestManager(t, m, http.MethodGet, "/api/upstream-pools", "")
	var listed struct {
		Pools []upstreamPoolSummary `json:"pools"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	wantListed := []upstreamPoolSummary{{
		ID:             "coding-ha",
		Name:           "coding-ha",
		Upstreams:      []string{"primary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: 3, RecoverySuccessThreshold: 2, RecoveryWaitSeconds: 60},
		ActiveUpstream: "primary",
		Workers:        []string{"app"},
		Readiness: []PoolReadiness{
			{Upstream: "primary", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
			{Upstream: "backup", Pool: "coding-ha", Mode: "protocol", Authoritative: true, Readiness: ReadinessStateUnknown},
		},
	}}
	if !reflect.DeepEqual(listed.Pools, wantListed) {
		t.Fatalf("unexpected pools:\n got %#v\nwant %#v", listed.Pools, wantListed)
	}

	response = requestManager(t, m, http.MethodPost, "/api/upstream-pools", `{"name":"research-ha","upstreams":["backup","tertiary"]}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", response.Code, response.Body.String())
	}
	response = requestManager(t, m, http.MethodPatch, "/api/upstream-pools/research-ha", `{"upstreams":["tertiary","backup"],"circuit_breaker":{"failure_threshold":5,"recovery_success_threshold":4,"recovery_wait_seconds":30}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", response.Code, response.Body.String())
	}
	gotPool := m.store.Config().UpstreamPools["research-ha"]
	wantPool := config.UpstreamPool{
		Name:      "research-ha",
		Upstreams: []string{"tertiary", "backup"},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 5, RecoverySuccessThreshold: 4, RecoveryWaitSeconds: 30,
		},
	}
	if !reflect.DeepEqual(gotPool, wantPool) {
		t.Fatalf("unexpected updated pool:\n got %#v\nwant %#v", gotPool, wantPool)
	}

	response = requestManager(t, m, http.MethodDelete, "/api/upstream-pools/coding-ha", "")
	var conflict map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	wantConflict := map[string]any{"error": `worker "app" upstream pool "coding-ha" does not exist`}
	if response.Code != http.StatusConflict || !reflect.DeepEqual(conflict, wantConflict) {
		t.Fatalf("expected attached pool delete conflict, got %d: %s", response.Code, response.Body.String())
	}
	response = requestManager(t, m, http.MethodDelete, "/api/upstream-pools/research-ha", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", response.Code, response.Body.String())
	}
	if _, exists := m.store.Config().UpstreamPools["research-ha"]; exists {
		t.Fatal("expected unused pool to be deleted")
	}
}

func TestManagerUpstreamPoolUpdateRejectsInvalidMemberSet(t *testing.T) {
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: t.TempDir()},
		Plugins:  testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "primary", UpstreamPool: "coding-ha"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"primary": {BaseURL: "https://primary.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
			"backup":  {BaseURL: "https://backup.example/v1", ProtocolProbe: config.ProtocolProbeConfig{Model: "probe"}},
		},
		UpstreamPools: map[string]config.UpstreamPool{
			"coding-ha": {Upstreams: []string{"primary", "backup"}},
		},
	}})
	m.cancelProbes()
	defer m.Close()

	response := requestManager(t, m, http.MethodPatch, "/api/upstream-pools/coding-ha", `{"upstreams":["backup"]}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `workers must use one active upstream`) {
		t.Fatalf("expected active member conflict, got %d: %s", response.Code, response.Body.String())
	}
	want := []string{"primary", "backup"}
	if got := m.store.Config().UpstreamPools["coding-ha"].Upstreams; !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid mutation changed pool: got %#v want %#v", got, want)
	}

	response = requestManager(t, m, http.MethodDelete, "/api/upstreams/backup", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("expected pool member delete conflict, got %d: %s", response.Code, response.Body.String())
	}
	if _, exists := m.store.Config().Upstreams["backup"]; !exists {
		t.Fatal("pool member delete changed upstream config")
	}
}

func requestManager(t *testing.T, m *Manager, method string, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	m.ServeHTTP(response, httptest.NewRequest(method, "http://manager.local"+path, strings.NewReader(body)))
	return response
}
