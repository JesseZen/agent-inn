package manager

import (
	"encoding/json"
	"net/http"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/hostedhooks"
)

func (m *Manager) handleSettings(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, status := m.syncConfigFromStore()
		writeJSON(rw, http.StatusOK, map[string]any{
			"settings": cfg.Settings,
			"status": map[string]any{
				"generation":      status.Generation,
				"dirty":           status.Dirty,
				"last_save_error": status.LastSaveError,
			},
		})
		return
	}
	if r.Method != http.MethodPatch {
		http.NotFound(rw, r)
		return
	}
	type launchPatch struct {
		DefaultMode *string `json:"default_mode"`
	}
	type tmuxPatch struct {
		SocketName      *string `json:"socket_name"`
		HostSession     *string `json:"host_session"`
		HostStartMode   *string `json:"host_start_mode"`
		TurnStatusHooks *bool   `json:"turn_status_hooks"`
		StatusBarHeight *int    `json:"status_bar_height"`
	}
	type terminalPatch struct {
		Host   *string    `json:"host"`
		Opener *string    `json:"opener"`
		Tmux   *tmuxPatch `json:"tmux"`
	}
	type metricsPatch struct {
		RetentionDays *int `json:"retention_days"`
	}
	var patch struct {
		StateDir *string        `json:"state_dir"`
		LogDir   *string        `json:"log_dir"`
		Launch   *launchPatch   `json:"launch"`
		Terminal *terminalPatch `json:"terminal"`
		Metrics  *metricsPatch  `json:"metrics"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	if m.configuredConfigPath() == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "config path is required"})
		return
	}
	validationErr := m.updateConfigIf(func(candidate *config.Config) error {
		if patch.StateDir != nil {
			candidate.Settings.StateDir = *patch.StateDir
		}
		if patch.LogDir != nil {
			candidate.Settings.LogDir = *patch.LogDir
		}
		if patch.Launch != nil && patch.Launch.DefaultMode != nil {
			candidate.Settings.Launch.DefaultMode = *patch.Launch.DefaultMode
		}
		if patch.Terminal != nil {
			if patch.Terminal.Host != nil {
				candidate.Settings.Terminal.Host = *patch.Terminal.Host
			}
			if patch.Terminal.Opener != nil {
				candidate.Settings.Terminal.Opener = *patch.Terminal.Opener
			}
			if patch.Terminal.Tmux != nil {
				if patch.Terminal.Tmux.SocketName != nil {
					candidate.Settings.Terminal.Tmux.SocketName = *patch.Terminal.Tmux.SocketName
				}
				if patch.Terminal.Tmux.HostSession != nil {
					candidate.Settings.Terminal.Tmux.HostSession = *patch.Terminal.Tmux.HostSession
				}
				if patch.Terminal.Tmux.HostStartMode != nil {
					candidate.Settings.Terminal.Tmux.HostStartMode = *patch.Terminal.Tmux.HostStartMode
				}
				if patch.Terminal.Tmux.TurnStatusHooks != nil {
					candidate.Settings.Terminal.Tmux.TurnStatusHooks = *patch.Terminal.Tmux.TurnStatusHooks
				}
				if patch.Terminal.Tmux.StatusBarHeight != nil {
					candidate.Settings.Terminal.Tmux.StatusBarHeight = *patch.Terminal.Tmux.StatusBarHeight
				}
			}
		}
		if patch.Metrics != nil {
			if patch.Metrics.RetentionDays != nil {
				candidate.Settings.Metrics.RetentionDays = *patch.Metrics.RetentionDays
			}
		}
		candidate.ApplyDefaults()
		return candidate.Validate()
	})
	if validationErr != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(validationErr)})
		return
	}
	if err := m.store.Save(); err != nil {
		status := m.syncConfigStatusFromStore()
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err), "status": status})
		return
	}
	cfg, status := m.syncConfigFromStore()
	if patch.Metrics != nil && patch.Metrics.RetentionDays != nil {
		m.mu.RLock()
		store := m.metricsStore
		m.mu.RUnlock()
		if err := store.CleanupRetention(); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err), "status": status})
			return
		}
	}
	if m.reconcileTurnHooks {
		if err := hostedhooks.Reconcile(cfg.Settings); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err), "status": status})
			return
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"settings": cfg.Settings,
		"status": map[string]any{
			"generation":      status.Generation,
			"dirty":           status.Dirty,
			"last_save_error": status.LastSaveError,
		},
	})
}
