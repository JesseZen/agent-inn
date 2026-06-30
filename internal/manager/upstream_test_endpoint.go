package manager

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/jesse/agent-inn/internal/upstream"
)

type upstreamProbeResponse struct {
	Upstream   string `json:"upstream"`
	OK         bool   `json:"ok"`
	Degraded   bool   `json:"degraded,omitempty"`
	StatusCode int    `json:"status_code"`
	LatencyMS  int64  `json:"latency_ms"`
	Error      string `json:"error,omitempty"`
}

func (m *Manager) handleUpstreamTest(rw http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/upstreams/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "test" || r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	name := parts[0]
	if name == "" {
		http.NotFound(rw, r)
		return
	}
	result := m.probeUpstreamByName(r.Context(), name)
	if result.Error == "not_found" {
		writeJSON(rw, http.StatusNotFound, map[string]any{"error": "upstream not found", "upstream": name})
		return
	}
	writeJSON(rw, http.StatusOK, result)
}

func (m *Manager) handleUpstreamTestAll(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	profiles := m.upstreamProfileSnapshot()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]upstreamProbeResponse, len(names))
	var wg sync.WaitGroup
	ctx := r.Context()
	for i, name := range names {
		wg.Add(1)
		go func(idx int, n string) {
			defer wg.Done()
			results[idx] = m.probeUpstreamByName(ctx, n)
		}(i, name)
	}
	wg.Wait()
	writeJSON(rw, http.StatusOK, map[string]any{"results": results})
}

func (m *Manager) probeUpstreamByName(ctx context.Context, name string) upstreamProbeResponse {
	profile, ok := m.upstreamProfileSnapshot()[name]
	if !ok {
		return upstreamProbeResponse{Upstream: name, OK: false, Error: "not_found"}
	}
	runtime, err := upstream.ResolveRuntime(name, profile)
	if err != nil {
		m.publishEvent(EventUpstreamProbed, map[string]any{"upstream": name, "ok": false, "error": redactedErrorMessage(err)})
		return upstreamProbeResponse{Upstream: name, OK: false, Error: redactedErrorMessage(err)}
	}
	compiled, err := upstream.Compile(runtime)
	if err != nil {
		m.publishEvent(EventUpstreamProbed, map[string]any{"upstream": name, "ok": false, "error": redactedErrorMessage(err)})
		return upstreamProbeResponse{Upstream: name, OK: false, Error: redactedErrorMessage(err)}
	}
	probe := upstream.Probe(ctx, compiled)
	resp := upstreamProbeResponse{
		Upstream:   name,
		OK:         probe.OK,
		Degraded:   probe.Degraded,
		StatusCode: probe.StatusCode,
		LatencyMS:  probe.LatencyMS,
		Error:      probe.Error,
	}
	m.publishEvent(EventUpstreamProbed, map[string]any{
		"upstream":    name,
		"ok":          probe.OK,
		"degraded":    probe.Degraded,
		"status_code": probe.StatusCode,
		"latency_ms":  probe.LatencyMS,
		"error":       probe.Error,
	})
	return resp
}
