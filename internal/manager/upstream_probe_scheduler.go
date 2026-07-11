package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

const (
	defaultUpstreamProbeInterval = time.Minute
	probeFingerprintSeparator    = "\x00"
)

type probeExecutionKey struct {
	Upstream string
	ProxyURL string
}

type probeSpec struct {
	Key                   probeExecutionKey
	Upstream              string
	ProxyURL              string
	Compiled              upstream.Compiled
	CredentialFingerprint string
	Model                 string
	Generation            int
	Fingerprint           string
	Pools                 []string
}

func (m *Manager) StartUpstreamProber(interval time.Duration) func() {
	if interval <= 0 {
		interval = defaultUpstreamProbeInterval
	}
	done := make(chan struct{})
	go func() {
		m.probeAllUpstreams(m.probeContext)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.probeAllUpstreams(m.probeContext)
			case <-done:
				return
			case <-m.probeContext.Done():
				return
			}
		}
	}()
	return func() { close(done) }
}

func (m *Manager) probeAllUpstreams(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	specs, pooled, err := m.buildProbeSpecifications()
	if err != nil {
		m.logger.Error(logging.EventUpstreamFail, "err", redactedErrorMessage(err))
		return
	}
	m.failoverMu.Lock()
	nextDesired := make(map[probeExecutionKey]probeSpec, len(specs))
	for _, spec := range specs {
		previous, exists := m.desiredProbes[spec.Key]
		if !exists || previous.Fingerprint != spec.Fingerprint || !slices.Equal(previous.Pools, spec.Pools) {
			m.probeGenerations[spec.Key]++
			spec.Generation = m.probeGenerations[spec.Key]
			for _, poolName := range previous.Pools {
				m.invalidatePoolReadinessLocked(poolName, previous.Upstream)
			}
			for _, poolName := range spec.Pools {
				m.invalidatePoolReadinessLocked(poolName, spec.Upstream)
			}
		} else {
			spec.Generation = previous.Generation
		}
		nextDesired[spec.Key] = spec
	}
	for key, previous := range m.desiredProbes {
		if _, exists := nextDesired[key]; exists {
			continue
		}
		m.probeGenerations[key]++
		for _, poolName := range previous.Pools {
			m.invalidatePoolReadinessLocked(poolName, previous.Upstream)
		}
		delete(m.pendingProbes, key)
	}
	m.desiredProbes = nextDesired
	for _, listed := range specs {
		spec := m.desiredProbes[listed.Key]
		if inFlight, exists := m.inFlightProbes[spec.Key]; exists {
			if inFlight.Generation != spec.Generation || inFlight.Fingerprint != spec.Fingerprint {
				m.pendingProbes[spec.Key] = spec
			}
			continue
		}
		m.startProbeLocked(spec)
	}
	m.failoverMu.Unlock()

	profiles := m.upstreamProfileSnapshot()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		if _, exists := pooled[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		go m.probeUpstreamByName(ctx, name)
	}
}

func (m *Manager) buildProbeSpecifications() ([]probeSpec, map[string]struct{}, error) {
	m.mu.RLock()
	profiles := make(map[string]config.UpstreamProfile, len(m.config.Upstreams))
	for name, profile := range m.config.Upstreams {
		profiles[name] = profile
	}
	pools := make(map[string]config.UpstreamPool, len(m.config.UpstreamPools))
	for name, pool := range m.config.UpstreamPools {
		pools[name] = pool
	}
	workers := make(map[string]config.WorkerConfig, len(m.config.Workers))
	for name, worker := range m.config.Workers {
		workers[name] = worker
	}
	m.mu.RUnlock()

	poolNames := make([]string, 0, len(pools))
	for name := range pools {
		poolNames = append(poolNames, name)
	}
	sort.Strings(poolNames)
	specsByKey := map[probeExecutionKey]probeSpec{}
	pooled := map[string]struct{}{}
	for _, poolName := range poolNames {
		pool := pools[poolName]
		workerNames := make([]string, 0)
		for workerName, worker := range workers {
			if worker.UpstreamPool == poolName {
				workerNames = append(workerNames, workerName)
			}
		}
		sort.Strings(workerNames)
		proxyURL := ""
		if len(workerNames) > 0 {
			proxyURL = strings.TrimSpace(workers[workerNames[0]].ProxyURL)
		}
		for _, upstreamName := range pool.Upstreams {
			pooled[upstreamName] = struct{}{}
			profile := profiles[upstreamName]
			runtime, err := upstream.ResolveRuntime(upstreamName, profile)
			if err != nil {
				return nil, nil, err
			}
			compiled, err := upstream.Compile(runtime)
			if err != nil {
				return nil, nil, err
			}
			credentialHash := sha256.Sum256([]byte(compiled.AuthorizationHeader))
			credentialFingerprint := hex.EncodeToString(credentialHash[:])
			model := strings.TrimSpace(profile.ProtocolProbe.Model)
			fingerprintHash := sha256.Sum256([]byte(strings.Join([]string{
				compiled.BaseURL.String(),
				string(compiled.APIFormat),
				credentialFingerprint,
				model,
				proxyURL,
			}, probeFingerprintSeparator)))
			key := probeExecutionKey{Upstream: upstreamName, ProxyURL: proxyURL}
			spec := specsByKey[key]
			if spec.Upstream == "" {
				spec = probeSpec{
					Key:                   key,
					Upstream:              upstreamName,
					ProxyURL:              proxyURL,
					Compiled:              compiled,
					CredentialFingerprint: credentialFingerprint,
					Model:                 model,
					Fingerprint:           hex.EncodeToString(fingerprintHash[:]),
				}
			}
			spec.Pools = append(spec.Pools, poolName)
			specsByKey[key] = spec
		}
	}
	keys := make([]probeExecutionKey, 0, len(specsByKey))
	for key := range specsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i int, j int) bool {
		if keys[i].Upstream == keys[j].Upstream {
			return keys[i].ProxyURL < keys[j].ProxyURL
		}
		return keys[i].Upstream < keys[j].Upstream
	})
	specs := make([]probeSpec, 0, len(keys))
	for _, key := range keys {
		specs = append(specs, specsByKey[key])
	}
	return specs, pooled, nil
}

func (m *Manager) startProbeLocked(spec probeSpec) {
	if m.probeContext.Err() != nil {
		return
	}
	m.inFlightProbes[spec.Key] = spec
	m.probeWait.Add(1)
	go func() {
		defer m.probeWait.Done()
		result := m.probeRunner(m.probeContext, spec)
		if m.probeContext.Err() == nil {
			m.recordScheduledProbeResult(spec, result)
		}
		m.failoverMu.Lock()
		delete(m.inFlightProbes, spec.Key)
		pending, exists := m.pendingProbes[spec.Key]
		if exists {
			delete(m.pendingProbes, spec.Key)
			m.startProbeLocked(pending)
		}
		m.failoverMu.Unlock()
	}()
}

func runProtocolProbe(ctx context.Context, spec probeSpec) upstream.ProbeResult {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if spec.ProxyURL != "" {
		proxyURL, err := appruntime.ParseProxyURL(spec.ProxyURL)
		if err != nil {
			return upstream.ProbeResult{Error: "connection_error", Mode: upstream.ProbeModeProtocol}
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return upstream.ProbeProtocolWithClient(ctx, spec.Compiled, spec.Model, &http.Client{Transport: transport})
}
