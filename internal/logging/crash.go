package logging

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	rootRunIDBytes         = 8
	crashDirectoryName     = "crashes"
	activeRootMarkerName   = "active-root.json"
	crashFilePrefix        = "root-"
	defaultCrashRunsKeep   = 10
	defaultCrashRotateKeep = 2
)

type RootRunMetadata struct {
	RunID         string    `json:"run_id"`
	SupervisorPID int       `json:"supervisor_pid"`
	StartedAt     time.Time `json:"started_at"`
	Version       string    `json:"version"`
	GoVersion     string    `json:"go_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	ConfigDir     string    `json:"config_dir"`
	ManagerPort   int       `json:"manager_port"`
	ArtifactPath  string    `json:"artifact_path"`
}

type RootRunExit struct {
	ChildPID             int       `json:"child_pid"`
	ExitCode             int       `json:"exit_code"`
	Signal               string    `json:"signal"`
	ForwardedSignal      string    `json:"forwarded_signal"`
	DurationMilliseconds int64     `json:"duration_ms"`
	CompletedAt          time.Time `json:"completed_at"`
}

type CrashArtifact struct {
	metadata   RootRunMetadata
	markerPath string
	writer     *RotatingWriter
	stderr     *redactingLineWriter
	logger     *slog.Logger
}

func NewRunID() (string, error) {
	buf := make([]byte, rootRunIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate root run ID: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func OpenCrashArtifact(logDir string, metadata RootRunMetadata) (*CrashArtifact, *RootRunMetadata, error) {
	return openCrashArtifact(logDir, metadata, DefaultRotateMaxBytes, defaultCrashRotateKeep, defaultCrashRunsKeep)
}

func openCrashArtifact(logDir string, metadata RootRunMetadata, maxBytes int64, rotateKeep int, runsKeep int) (*CrashArtifact, *RootRunMetadata, error) {
	crashDir := filepath.Join(logDir, crashDirectoryName)
	if err := os.MkdirAll(crashDir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create crash log directory %s: %w", crashDir, err)
	}
	markerPath := filepath.Join(crashDir, activeRootMarkerName)
	var previous *RootRunMetadata
	markerData, err := os.ReadFile(markerPath)
	if err == nil {
		previous = &RootRunMetadata{}
		if err := json.Unmarshal(markerData, previous); err != nil {
			return nil, nil, fmt.Errorf("decode active root marker %s: %w", markerPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read active root marker %s: %w", markerPath, err)
	}

	startedAt := metadata.StartedAt.UTC()
	metadata.StartedAt = startedAt
	name := crashFilePrefix + startedAt.Format("20060102T150405.000000000Z") + "-" + metadata.RunID + ".log"
	metadata.ArtifactPath = filepath.Join(crashDir, name)
	writer, err := NewRotatingWriter(metadata.ArtifactPath, maxBytes, rotateKeep)
	if err != nil {
		return nil, nil, fmt.Errorf("open crash artifact %s: %w", metadata.ArtifactPath, err)
	}
	artifact := &CrashArtifact{
		metadata:   metadata,
		markerPath: markerPath,
		writer:     writer,
		stderr:     &redactingLineWriter{writer: writer},
	}
	artifact.logger = New(writer, "detail", ComponentRootSupervisor).With("run", metadata.RunID)
	if err := writeActiveRootMarker(markerPath, metadata); err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	if err := pruneCrashRunGroups(crashDir, runsKeep); err != nil {
		_ = writer.Close()
		return nil, nil, err
	}
	artifact.logger.Info(EventRootSupervisorStart,
		"pid", metadata.SupervisorPID,
		"started_at", metadata.StartedAt.Format(time.RFC3339Nano),
		"version", metadata.Version,
		"go", metadata.GoVersion,
		"os", metadata.OS,
		"arch", metadata.Arch,
		"config_dir", metadata.ConfigDir,
		"port", metadata.ManagerPort,
		"artifact", metadata.ArtifactPath,
	)
	if previous != nil {
		artifact.logger.Warn(EventRootPreviousUnclean,
			"previous_run", previous.RunID,
			"previous_pid", previous.SupervisorPID,
			"previous_started_at", previous.StartedAt.Format(time.RFC3339Nano),
			"previous_artifact", previous.ArtifactPath,
		)
	}
	return artifact, previous, nil
}

func (a *CrashArtifact) Writer() io.Writer {
	return a.stderr
}

func (a *CrashArtifact) Path() string {
	return a.metadata.ArtifactPath
}

func (a *CrashArtifact) Logger() *slog.Logger {
	return a.logger
}

func (a *CrashArtifact) Complete(exit RootRunExit) error {
	if err := a.stderr.Flush(); err != nil {
		return err
	}
	args := []any{
		"child_pid", exit.ChildPID,
		"exit_code", exit.ExitCode,
		"signal", exit.Signal,
		"forwarded_signal", exit.ForwardedSignal,
		"duration_ms", exit.DurationMilliseconds,
		"completed_at", exit.CompletedAt.UTC().Format(time.RFC3339Nano),
	}
	if exit.ExitCode == 0 && exit.Signal == "" {
		a.logger.Info(EventRootSupervisorExit, args...)
	} else {
		a.logger.Error(EventRootSupervisorExit, args...)
	}
	if err := os.Remove(a.markerPath); err != nil {
		return fmt.Errorf("remove active root marker %s: %w", a.markerPath, err)
	}
	return nil
}

func (a *CrashArtifact) Close() error {
	if err := a.stderr.Flush(); err != nil {
		_ = a.writer.Close()
		return err
	}
	return a.writer.Close()
}

func writeActiveRootMarker(path string, metadata RootRunMetadata) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".active-root-*.tmp")
	if err != nil {
		return fmt.Errorf("create active root marker in %s: %w", dir, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set active root marker permissions %s: %w", temporaryPath, err)
	}
	if err := json.NewEncoder(temporary).Encode(metadata); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode active root marker %s: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close active root marker %s: %w", temporaryPath, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace active root marker %s: %w", path, err)
	}
	return nil
}

func pruneCrashRunGroups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list crash artifacts %s: %w", dir, err)
	}
	groups := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, crashFilePrefix) {
			continue
		}
		index := strings.Index(name, ".log")
		if index == -1 {
			continue
		}
		base := name[:index+len(".log")]
		groups[base] = append(groups[base], name)
	}
	ordered := make([]string, 0, len(groups))
	for base := range groups {
		ordered = append(ordered, base)
	}
	sort.Strings(ordered)
	for _, base := range ordered[:max(0, len(ordered)-keep)] {
		for _, name := range groups[base] {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("remove old crash artifact %s: %w", filepath.Join(dir, name), err)
			}
		}
	}
	return nil
}

type redactingLineWriter struct {
	mu      sync.Mutex
	writer  io.Writer
	pending bytes.Buffer
}

func (w *redactingLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(p)
	for len(p) > 0 {
		index := bytes.IndexByte(p, '\n')
		if index == -1 {
			_, _ = w.pending.Write(p)
			return originalLength, nil
		}
		_, _ = w.pending.Write(p[:index])
		if _, err := io.WriteString(w.writer, Redact(w.pending.String())+"\n"); err != nil {
			return 0, err
		}
		w.pending.Reset()
		p = p[index+1:]
	}
	return originalLength, nil
}

func (w *redactingLineWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(w.writer, Redact(w.pending.String())+"\n")
	w.pending.Reset()
	return err
}
