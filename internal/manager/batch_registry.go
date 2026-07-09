package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const batchRunsFileName = "batch-runs.json"

type BatchRegistry struct {
	path string
	lock string
}

type BatchRun struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Prompt          string         `json:"prompt,omitempty"`
	WorkerName      string         `json:"worker_name"`
	WorkerPort      int            `json:"worker_port"`
	Model           string         `json:"model,omitempty"`
	SourceDirectory string         `json:"source_directory"`
	CreatedAt       time.Time      `json:"created_at"`
	Variants        []BatchVariant `json:"variants"`
}

type BatchVariant struct {
	ID              string `json:"id"`
	Index           int    `json:"index"`
	HostedSessionID string `json:"hosted_session_id"`
	SessionLabel    string `json:"session_label"`
	WorktreeDir     string `json:"worktree_dir"`
}

type BatchCreateInput struct {
	Title           string
	Prompt          string
	WorkerName      string
	WorkerPort      int
	Model           string
	SourceDirectory string
	Variants        []BatchVariant
}

type batchRunsFile struct {
	NextBatchID int                 `json:"next_batch_id"`
	Runs        map[string]BatchRun `json:"runs"`
}

func BatchRegistryPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return filepath.Join(expandHomePath(stateDir), batchRunsFileName)
}

func NewBatchRegistry(path string) *BatchRegistry {
	return &BatchRegistry{path: path, lock: path + ".lock"}
}

func (r *BatchRegistry) List() ([]BatchRun, error) {
	var runs []BatchRun
	err := r.withLockedFile(func(file *batchRunsFile) error {
		runs = make([]BatchRun, 0, len(file.Runs))
		for _, run := range file.Runs {
			runs = append(runs, run)
		}
		sort.Slice(runs, func(i, j int) bool {
			if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
				return runs[i].ID < runs[j].ID
			}
			return runs[i].CreatedAt.After(runs[j].CreatedAt)
		})
		return nil
	})
	return runs, err
}

func (r *BatchRegistry) Get(batchID string) (BatchRun, bool, error) {
	var run BatchRun
	found := false
	err := r.withLockedFile(func(file *batchRunsFile) error {
		batchID = strings.TrimSpace(batchID)
		value, ok := file.Runs[batchID]
		if !ok {
			return nil
		}
		run = value
		found = true
		return nil
	})
	return run, found, err
}

func (r *BatchRegistry) Create(input BatchCreateInput) (BatchRun, error) {
	var created BatchRun
	err := r.withLockedFile(func(file *batchRunsFile) error {
		if file.Runs == nil {
			file.Runs = map[string]BatchRun{}
		}
		title := strings.TrimSpace(input.Title)
		workerName := strings.TrimSpace(input.WorkerName)
		sourceDirectory := strings.TrimSpace(input.SourceDirectory)
		if title == "" {
			return errors.New("batch title is required")
		}
		if workerName == "" {
			return errors.New("worker name is required")
		}
		if input.WorkerPort <= 0 {
			return errors.New("worker port is required")
		}
		if sourceDirectory == "" {
			return errors.New("source directory is required")
		}

		file.NextBatchID++
		created = BatchRun{
			ID:              fmt.Sprintf("batch_%d", file.NextBatchID),
			Title:           title,
			Prompt:          strings.TrimSpace(input.Prompt),
			WorkerName:      workerName,
			WorkerPort:      input.WorkerPort,
			Model:           strings.TrimSpace(input.Model),
			SourceDirectory: sourceDirectory,
			CreatedAt:       time.Now().UTC(),
			Variants:        batchVariantsWithIDs(input.Variants),
		}
		file.Runs[created.ID] = created
		return nil
	})
	return created, err
}

func (r *BatchRegistry) SetVariants(batchID string, variants []BatchVariant) (BatchRun, error) {
	var updated BatchRun
	err := r.withLockedFile(func(file *batchRunsFile) error {
		batchID = strings.TrimSpace(batchID)
		run, ok := file.Runs[batchID]
		if !ok {
			return fmt.Errorf("batch %q not found", batchID)
		}
		run.Variants = batchVariantsWithIDs(variants)
		file.Runs[batchID] = run
		updated = run
		return nil
	})
	return updated, err
}

func (r *BatchRegistry) Delete(batchID string) (BatchRun, error) {
	var deleted BatchRun
	err := r.withLockedFile(func(file *batchRunsFile) error {
		batchID = strings.TrimSpace(batchID)
		run, ok := file.Runs[batchID]
		if !ok {
			return fmt.Errorf("batch %q not found", batchID)
		}
		delete(file.Runs, batchID)
		deleted = run
		return nil
	})
	return deleted, err
}

func (r *BatchRegistry) withLockedFile(fn func(*batchRunsFile) error) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	unlock, err := lockFile(r.lock)
	if err != nil {
		return err
	}
	defer unlock()

	file, err := r.loadFile()
	if err != nil {
		return err
	}
	if err := fn(file); err != nil {
		return err
	}
	return r.saveFile(file)
}

func (r *BatchRegistry) loadFile() (*batchRunsFile, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &batchRunsFile{Runs: map[string]BatchRun{}}, nil
		}
		return nil, err
	}
	var file batchRunsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Runs == nil {
		file.Runs = map[string]BatchRun{}
	}
	return &file, nil
}

func (r *BatchRegistry) saveFile(file *batchRunsFile) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return writeTextFile(r.path, string(data), 0600)
}

func batchVariantsWithIDs(variants []BatchVariant) []BatchVariant {
	out := make([]BatchVariant, len(variants))
	for i, variant := range variants {
		variant.ID = strings.TrimSpace(variant.ID)
		variant.HostedSessionID = strings.TrimSpace(variant.HostedSessionID)
		variant.SessionLabel = strings.TrimSpace(variant.SessionLabel)
		variant.WorktreeDir = strings.TrimSpace(variant.WorktreeDir)
		if variant.ID == "" {
			variant.ID = fmt.Sprintf("variant_%d", i+1)
		}
		out[i] = variant
	}
	return out
}
