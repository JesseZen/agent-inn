package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
)

const (
	defaultBatchVariantCount = 3
	maxBatchVariantCount     = 8
)

func (m *Manager) handleBatches(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		batches, err := m.batchRegistry.List()
		if err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"batches": batches})
		return
	}
	if r.Method == http.MethodPost {
		m.handleCreateBatch(rw, r)
		return
	}
	http.NotFound(rw, r)
}

func (m *Manager) handleCreateBatch(rw http.ResponseWriter, r *http.Request) {
	var payload struct {
		Title           string `json:"title"`
		Prompt          string `json:"prompt"`
		WorkerName      string `json:"worker_name"`
		Count           *int   `json:"count"`
		SourceDirectory string `json:"source_directory"`
		Model           string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	title := strings.TrimSpace(payload.Title)
	prompt := strings.TrimSpace(payload.Prompt)
	workerName := strings.TrimSpace(payload.WorkerName)
	sourceDirectory := strings.TrimSpace(payload.SourceDirectory)
	model := strings.TrimSpace(payload.Model)
	if workerName == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker name is required"})
		return
	}
	if sourceDirectory == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "source directory is required"})
		return
	}
	count := defaultBatchVariantCount
	if payload.Count != nil {
		count = *payload.Count
	}
	if count < 1 || count > maxBatchVariantCount {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "count must be between 1 and 8"})
		return
	}

	cfg, _ := m.syncConfigFromStore()
	worker, ok := cfg.Workers[workerName]
	if !ok {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("worker %q not found", workerName)})
		return
	}
	if worker.Role != "cli" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("worker %q is not a cli worker", workerName)})
		return
	}
	if title == "" {
		title = "batch"
	}
	batch, err := m.batchRegistry.Create(BatchCreateInput{
		Title:           title,
		Prompt:          prompt,
		WorkerName:      workerName,
		WorkerPort:      worker.Port,
		Model:           model,
		SourceDirectory: sourceDirectory,
	})
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}

	variants := make([]BatchVariant, 0, count)
	createdSessionIDs := make([]string, 0, count)
	labelBase := title
	if strings.TrimSpace(payload.Title) == "" {
		labelBase = batch.ID
	}
	for i := 1; i <= count; i++ {
		worktreeDir := filepath.Join(expandHomePath(cfg.Settings.StateDir), "worktrees", batch.ID, fmt.Sprintf("%d", i))
		if err := batchWorktreeCreator(sourceDirectory, worktreeDir); err != nil {
			m.cleanupReservedBatch(batch.ID, createdSessionIDs, cfg.Settings)
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		sessionLabel := fmt.Sprintf("%s #%d", labelBase, i)
		session, err := m.hostedSessions.Create(HostedSessionRecord{
			SessionLabel: sessionLabel,
			WorkerName:   workerName,
			WorkerPort:   worker.Port,
			Workspace:    worktreeDir,
			Model:        model,
		})
		if err != nil {
			m.cleanupReservedBatch(batch.ID, createdSessionIDs, cfg.Settings)
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		createdSessionIDs = append(createdSessionIDs, session.SessionID)
		variants = append(variants, BatchVariant{
			Index:           i,
			HostedSessionID: session.SessionID,
			SessionLabel:    session.SessionLabel,
			WorktreeDir:     worktreeDir,
		})
	}
	updated, err := m.batchRegistry.SetVariants(batch.ID, variants)
	if err != nil {
		m.cleanupReservedBatch(batch.ID, createdSessionIDs, cfg.Settings)
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	writeJSON(rw, http.StatusCreated, updated)
}

func (m *Manager) handleBatchByID(rw http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/batches/")
	if path == "" {
		http.NotFound(rw, r)
		return
	}
	parts := strings.Split(path, "/")
	batchID := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			batch, ok, err := m.batchRegistry.Get(batchID)
			if err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
			if !ok {
				http.NotFound(rw, r)
				return
			}
			writeJSON(rw, http.StatusOK, batch)
			return
		}
		if r.Method == http.MethodDelete {
			m.handleDeleteBatch(rw, batchID)
			return
		}
		http.NotFound(rw, r)
		return
	}
	if len(parts) == 4 && parts[1] == "variants" && parts[3] == "select" && r.Method == http.MethodPost {
		batch, err := m.batchRegistry.SelectWinner(batchID, strings.TrimSpace(parts[2]))
		if err != nil {
			writeJSON(rw, http.StatusNotFound, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, batch)
		return
	}
	http.NotFound(rw, r)
}

func (m *Manager) handleDeleteBatch(rw http.ResponseWriter, batchID string) {
	batch, err := m.batchRegistry.Delete(batchID)
	if err != nil {
		writeJSON(rw, http.StatusNotFound, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	cfg, _ := m.syncConfigFromStore()
	for _, variant := range batch.Variants {
		if variant.HostedSessionID != "" {
			if err := m.hostedSessions.RemoveForSettings(variant.HostedSessionID, cfg.Settings, hostedTMuxRunnerFactory()); err != nil {
				writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"batch_id": batch.ID})
}

func (m *Manager) cleanupReservedBatch(batchID string, sessionIDs []string, settings config.Settings) {
	for _, sessionID := range sessionIDs {
		_ = m.hostedSessions.RemoveForSettings(sessionID, settings, hostedTMuxRunnerFactory())
	}
	_, _ = m.batchRegistry.Delete(batchID)
}
