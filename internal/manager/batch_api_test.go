package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
		WorkerName:      "codex-app",
		WorkerPort:      6767,
		Model:           "gpt-5.5",
		SourceDirectory: "/repo",
		CreatedAt:       got.CreatedAt,
		Variants: []BatchVariant{
			{ID: "variant_1", Index: 1, HostedSessionID: "hs_1", SessionLabel: "fix scroll batch_1 #1", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "1")},
			{ID: "variant_2", Index: 2, HostedSessionID: "hs_2", SessionLabel: "fix scroll batch_1 #2", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "2")},
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

func TestManagerAPICreatesBatchesWithDuplicateTitles(t *testing.T) {
	m, dir := newBatchAPITestManager(t)
	first := createBatchForAPITest(t, m)
	second := createBatchForAPITest(t, m)

	want := []BatchRun{
		{
			ID:              "batch_1",
			Title:           "fix scroll",
			WorkerName:      "codex-app",
			WorkerPort:      6767,
			Model:           "gpt-5.5",
			SourceDirectory: "/repo",
			CreatedAt:       first.CreatedAt,
			Variants: []BatchVariant{
				{ID: "variant_1", Index: 1, HostedSessionID: "hs_1", SessionLabel: "fix scroll batch_1 #1", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "1")},
				{ID: "variant_2", Index: 2, HostedSessionID: "hs_2", SessionLabel: "fix scroll batch_1 #2", WorktreeDir: filepath.Join(dir, "worktrees", "batch_1", "2")},
			},
		},
		{
			ID:              "batch_2",
			Title:           "fix scroll",
			WorkerName:      "codex-app",
			WorkerPort:      6767,
			Model:           "gpt-5.5",
			SourceDirectory: "/repo",
			CreatedAt:       second.CreatedAt,
			Variants: []BatchVariant{
				{ID: "variant_1", Index: 1, HostedSessionID: "hs_3", SessionLabel: "fix scroll batch_2 #1", WorktreeDir: filepath.Join(dir, "worktrees", "batch_2", "1")},
				{ID: "variant_2", Index: 2, HostedSessionID: "hs_4", SessionLabel: "fix scroll batch_2 #2", WorktreeDir: filepath.Join(dir, "worktrees", "batch_2", "2")},
			},
		},
	}
	if !reflect.DeepEqual([]BatchRun{first, second}, want) {
		t.Fatalf("batches mismatch:\n got %#v\nwant %#v", []BatchRun{first, second}, want)
	}
}

func TestManagerAPIBatchVariantSelectRouteIsNotAvailable(t *testing.T) {
	m, _ := newBatchAPITestManager(t)
	created := createBatchForAPITest(t, m)

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches/"+created.ID+"/variants/variant_2/select", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
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

func TestManagerAPIDeleteRetriesAfterPartialHostedSessionCleanup(t *testing.T) {
	m, dir := newBatchAPITestManager(t)
	created := createBatchForAPITest(t, m)
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(dir))
	err := registry.withLockedFile(func(file *hostedSessionFile) error {
		session := file.Sessions[created.Variants[1].HostedSessionID]
		session.TmuxWindowID = "@12"
		file.Sessions[created.Variants[1].HostedSessionID] = session
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	removeErr := errors.New("tmux kill failed")
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "list-windows") {
				return "@12\t" + created.Variants[1].SessionLabel + "\n", nil
			}
			if strings.Contains(joined, "kill-window") {
				return "", removeErr
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/batches/"+created.ID, nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	partialSessions, err := NewHostedSessionRegistry(HostedSessionRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	wantPartialSessions := []HostedSessionRecord{
		{
			SessionID:    created.Variants[1].HostedSessionID,
			SessionLabel: created.Variants[1].SessionLabel,
			WorkerName:   created.WorkerName,
			WorkerPort:   created.WorkerPort,
			Workspace:    created.Variants[1].WorktreeDir,
			Model:        created.Model,
			TmuxWindowID: "@12",
			CreatedAt:    partialSessions[0].CreatedAt,
			LastOpenedAt: partialSessions[0].LastOpenedAt,
		},
	}
	if !reflect.DeepEqual(partialSessions, wantPartialSessions) {
		t.Fatalf("hosted sessions after failed delete mismatch:\n got %#v\nwant %#v", partialSessions, wantPartialSessions)
	}

	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return nil
	}

	retry := httptest.NewRecorder()
	m.ServeHTTP(retry, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/batches/"+created.ID, nil))
	if retry.Code != http.StatusOK {
		t.Fatalf("unexpected retry status %d: %s", retry.Code, retry.Body.String())
	}

	batches, err := NewBatchRegistry(BatchRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewHostedSessionRegistry(HostedSessionRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, []BatchRun{}) {
		t.Fatalf("batches mismatch: %#v", batches)
	}
	if !reflect.DeepEqual(sessions, []HostedSessionRecord{}) {
		t.Fatalf("hosted sessions mismatch: %#v", sessions)
	}
}

func TestManagerAPIBatchCreateCleansWorktreesWhenVariantCreationFails(t *testing.T) {
	repo := t.TempDir()
	runGitForBatchAPITest(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitForBatchAPITest(t, repo, "add", "README.md")
	runGitForBatchAPITest(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")

	m, dir := newBatchAPITestManager(t)
	var firstWorktree string
	createdWorktrees := 0
	restore := setBatchWorktreeCreatorForTest(func(sourceDir string, targetDir string) error {
		createdWorktrees++
		if createdWorktrees == 2 {
			return errors.New("create worktree failed")
		}
		firstWorktree = targetDir
		return createBatchWorktree(sourceDir, targetDir)
	})
	t.Cleanup(restore)

	body := strings.NewReader(`{"title":"rollback","prompt":"Fix","worker_name":"codex-app","count":2,"source_directory":` + strconv.Quote(repo) + `}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/batches", body))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if firstWorktree == "" {
		t.Fatal("expected first worktree to be created")
	}
	if _, err := os.Stat(firstWorktree); !os.IsNotExist(err) {
		t.Fatalf("expected first worktree directory to be removed, stat err: %v", err)
	}
	worktreeList := runGitForBatchAPITest(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(worktreeList, firstWorktree) {
		t.Fatalf("expected git worktree metadata to be removed, got:\n%s", worktreeList)
	}
	batches, err := NewBatchRegistry(BatchRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewHostedSessionRegistry(HostedSessionRegistryPath(dir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, []BatchRun{}) {
		t.Fatalf("batches mismatch: %#v", batches)
	}
	if !reflect.DeepEqual(sessions, []HostedSessionRecord{}) {
		t.Fatalf("hosted sessions mismatch: %#v", sessions)
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

func runGitForBatchAPITest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
