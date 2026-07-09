package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
)

func TestManagerAPICreatesBatchWithHostedSessionsAndWorktrees(t *testing.T) {
	dir := t.TempDir()
	settings := config.Settings{StateDir: dir}
	m := New(Config{Config: config.Config{
		Settings: settings,
		Workers: map[string]config.WorkerConfig{
			"codex-app": {Port: 6767, Upstream: "openai", Launcher: "codex"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	}})

	var worktrees [][2]string
	restore := setBatchWorktreeCreatorForTest(func(sourceDir string, targetDir string) error {
		worktrees = append(worktrees, [2]string{sourceDir, targetDir})
		return os.MkdirAll(targetDir, 0700)
	})
	defer restore()

	body := strings.NewReader(`{"title":"fix scroll","prompt":"Fix scroll","worker_name":"codex-app","count":2,"source_directory":"/repo","model":"gpt-5.5"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var got BatchRun
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := BatchRun{
		ID:              "batch_1",
		Title:           "fix scroll",
		Prompt:          "Fix scroll",
		WorkerName:      "codex-app",
		WorkerPort:      6767,
		Model:           "gpt-5.5",
		SourceDirectory: "/repo",
		CreatedAt:       got.CreatedAt,
		Variants: []BatchVariant{
			{ID: "variant_1", Index: 1, HostedSessionID: "hs_1", SessionLabel: "fix scroll #1", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "1")},
			{ID: "variant_2", Index: 2, HostedSessionID: "hs_2", SessionLabel: "fix scroll #2", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "2")},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch mismatch:\n got %#v\nwant %#v", got, want)
	}
	if !reflect.DeepEqual(worktrees, [][2]string{{"/repo", want.Variants[0].WorktreeDir}, {"/repo", want.Variants[1].WorktreeDir}}) {
		t.Fatalf("worktrees mismatch: %#v", worktrees)
	}
}

func TestManagerAPIBatchCreateRejectsUnknownWorker(t *testing.T) {
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: t.TempDir()}}})

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"title":"fix scroll","worker_name":"missing","count":1,"source_directory":"/repo"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches", body))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
}

func TestManagerAPIBatchCreateRejectsInvalidCount(t *testing.T) {
	for _, count := range []int{0, 9} {
		dir := t.TempDir()
		m := New(Config{Config: config.Config{
			Settings: config.Settings{StateDir: dir},
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai", Launcher: "codex"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		}})

		res := httptest.NewRecorder()
		body := strings.NewReader(`{"title":"fix scroll","worker_name":"codex-app","count":` + strconv.Itoa(count) + `,"source_directory":"/repo"}`)
		m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches", body))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("count %d: unexpected status %d: %s", count, res.Code, res.Body.String())
		}
	}
}

func TestManagerAPISelectsBatchWinner(t *testing.T) {
	m, _ := newBatchAPITestManager(t)
	created := createBatchForAPITest(t, m)

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches/"+created.ID+"/variants/variant_2/select", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	var got BatchRun
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.WinnerVariantID = "variant_2"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerAPIDeletesBatchAndHostedSessions(t *testing.T) {
	m, dir := newBatchAPITestManager(t)
	created := createBatchForAPITest(t, m)

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/batches/"+created.ID, nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var deleted map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, map[string]string{"batch_id": created.ID}) {
		t.Fatalf("delete response mismatch: %#v", deleted)
	}

	batches, err := NewBatchRegistry(BatchRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, []BatchRun{}) {
		t.Fatalf("batches mismatch: %#v", batches)
	}
	sessions, err := NewHostedSessionRegistry(HostedSessionRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sessions, []HostedSessionRecord{}) {
		t.Fatalf("hosted sessions mismatch: %#v", sessions)
	}
}

func TestManagerAPIDeleteKeepsBatchWhenHostedSessionCleanupFails(t *testing.T) {
	m, dir := newBatchAPITestManager(t)
	created := createBatchForAPITest(t, m)
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(dir))
	err := registry.withLockedFile(func(file *hostedSessionFile) error {
		session := file.Sessions[created.Variants[0].HostedSessionID]
		session.TmuxWindowID = "@12"
		file.Sessions[created.Variants[0].HostedSessionID] = session
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	removeErr := errors.New("tmux socket permission denied")
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			return "", removeErr
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/batches/"+created.ID, nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	got, ok, err := NewBatchRegistry(BatchRegistryPath(dir)).Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected batch to remain gettable")
	}
	if !reflect.DeepEqual(got, created) {
		t.Fatalf("batch mismatch:\n got %#v\nwant %#v", got, created)
	}
	listed, err := NewBatchRegistry(BatchRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(listed, []BatchRun{created}) {
		t.Fatalf("batches mismatch: %#v", listed)
	}
}

func newBatchAPITestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: dir},
		Workers: map[string]config.WorkerConfig{
			"codex-app": {Port: 6767, Upstream: "openai", Launcher: "codex"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1"},
		},
	}})
	restore := setBatchWorktreeCreatorForTest(func(sourceDir string, targetDir string) error {
		return os.MkdirAll(targetDir, 0700)
	})
	t.Cleanup(restore)
	return m, dir
}

func createBatchForAPITest(t *testing.T, m *Manager) BatchRun {
	t.Helper()
	body := strings.NewReader(`{"title":"fix scroll","prompt":"Fix scroll","worker_name":"codex-app","count":2,"source_directory":"/repo","model":"gpt-5.5"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got BatchRun
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}
