package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/hostedhooks"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

func TestManagerDetectsManagedPortConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"one": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	if err := m.CheckPortAvailable("two", 6767); err == nil || !strings.Contains(err.Error(), "worker 'one'") {
		t.Fatalf("expected managed port conflict, got %v", err)
	}
}

func TestManagerDetectsExternalPortConflict(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	m := New(Config{Config: config.Config{}})
	if err := m.CheckPortAvailable("new", port); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("expected external port conflict, got %v", err)
	}
}

func TestManagerAPIListsWorkersAndProvidersWithoutSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") {
		t.Fatalf("workers API leaked secret: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"has_api_key":true`) {
		t.Fatalf("workers API did not expose key state: %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "api_key_ref") {
		t.Fatalf("workers API leaked legacy key ref field: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"snapshot_generation":1`) {
		t.Fatalf("workers API did not expose snapshot generation: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected config status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") {
		t.Fatalf("config API leaked expanded secret: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"dirty"`) {
		t.Fatalf("config API missing status: %s", res.Body.String())
	}
}

func TestManagerAPISetsHostedSessionStatusFromTmuxState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := New(Config{Config: config.Config{}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(""))
	active, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "ainn:worker-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 2",
		WorkerName:   "worker",
		WorkerPort:   11200,
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			if strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(defaultTmuxSettings()), " ") {
				return "ainn:worker-1\tworker 1\n", nil
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/hosted-sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"session_id":"`+active.SessionID+`"`) {
		t.Fatalf("missing active session: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"session_id":"`+stale.SessionID+`"`) {
		t.Fatalf("missing stale session: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"active"`) {
		t.Fatalf("missing active status: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"stale"`) {
		t.Fatalf("missing stale status: %s", res.Body.String())
	}
}

func TestManagerAPIHostedSessionsUseTmuxSettingsForStatus(t *testing.T) {
	dir := t.TempDir()
	settings := config.Settings{
		StateDir: dir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	m := New(Config{Config: config.Config{Settings: settings}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(dir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			got = append(got, append([]string{}, args...))
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "@12\tworker 1\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/hosted-sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"session_id":"`+created.SessionID+`"`) {
		t.Fatalf("missing session: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"active"`) {
		t.Fatalf("missing active status: %s", res.Body.String())
	}
	want := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tmux calls: got %#v want %#v", got, want)
	}
}

func TestManagerAPIDeleteHostedSessionUsesTmuxSettings(t *testing.T) {
	dir := t.TempDir()
	settings := config.Settings{
		StateDir: dir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	m := New(Config{Config: config.Config{Settings: settings}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(dir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "worker 1",
		WorkerName:   "worker",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			got = append(got, append([]string{}, args...))
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "@12\tworker 1\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "http://manager.local/api/hosted-sessions/"+created.SessionID, nil)
	m.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	want := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		TmuxKillWindowCommandForSettings(settings, "@12"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tmux calls: got %#v want %#v", got, want)
	}
}

func TestManagerHostedSessionsUseSettingsStateDir(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "config-dir", "config.yaml")
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Settings: config.Settings{StateDir: stateDir},
			Workers: map[string]config.WorkerConfig{
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"session_label":"solve problem A","worker_name":"cli","worker_port":11199}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}

	wantPath := filepath.Join(stateDir, hostedSessionsFileName)
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected hosted registry at settings.state_dir %s: %v", wantPath, err)
	}
	oldPath := filepath.Join(filepath.Dir(configPath), hostedSessionsFileName)
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("hosted registry should not use config file dir %s: %v", oldPath, err)
	}
}

func TestManagerAPIDuplicatesHostedSession(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{
		Config: config.Config{
			Settings: config.Settings{StateDir: stateDir},
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "cli-openai",
		WorkerPort:                 11199,
		Workspace:                  "/tmp/work",
		Model:                      "gpt-5.5",
		AddDirs:                    []string{"/tmp/shared"},
		TmuxWindowID:               "@12",
		LauncherSessionID:          "019e7c18-0ee7-7ff2-bc82-9c410511ede3",
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions/"+created.SessionID+"/duplicate", nil))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := HostedSessionRecord{
		SessionID:    got.SessionID,
		SessionLabel: "solve problem A 2",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		Workspace:    "/tmp/work",
		Model:        "gpt-5.5",
		AddDirs:      []string{"/tmp/shared"},
		CreatedAt:    got.CreatedAt,
		LastOpenedAt: got.LastOpenedAt,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
	persisted, ok, err := registry.Get(got.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected duplicated hosted session %q", got.SessionID)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, want)
	}
}

func TestManagerAPIMarksHostedSessionUnread(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	m := New(Config{Config: config.Config{Settings: settings}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "cli-openai",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			gotCalls = append(gotCalls, append([]string{}, args...))
			if reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)) {
				return "", nil
			}
			if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)) {
				return "@12\tsolve problem A\n", nil
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions/"+created.SessionID+"/mark-unread", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnAcknowledgedGeneration = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, want)
	}
	wantCalls := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForRecord(settings, want),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestManagerAPIMarkHostedSessionUnreadRejectsRunningTurn(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: stateDir}}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:   "solve problem A",
		WorkerName:     "cli-openai",
		WorkerPort:     11199,
		TmuxWindowID:   "@12",
		TurnState:      HostedTurnStateRunning,
		TurnGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			t.Fatalf("unexpected tmux call: %#v", args)
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions/"+created.SessionID+"/mark-unread", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantError := map[string]string{"error": `hosted session "` + created.SessionID + `" turn state "running" cannot be marked unread`}
	if !reflect.DeepEqual(got, wantError) {
		t.Fatalf("got %#v, want %#v", got, wantError)
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, created) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, created)
	}
}

func TestManagerAPIMarksWindowlessHostedSessionUnreadWithoutTmux(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: stateDir}}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "cli-openai",
		WorkerPort:                 11199,
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			t.Fatalf("unexpected tmux call: %#v", args)
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions/"+created.SessionID+"/mark-unread", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnAcknowledgedGeneration = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
}

func TestManagerAPIMarksStaleHostedSessionUnreadWithoutTmuxUpdate(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	m := New(Config{Config: config.Config{Settings: settings}})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel:               "solve problem A",
		WorkerName:                 "cli-openai",
		WorkerPort:                 11199,
		TmuxWindowID:               "@12",
		TurnState:                  HostedTurnStateDone,
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			gotCalls = append(gotCalls, append([]string{}, args...))
			if reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)) {
				return "", nil
			}
			if reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)) {
				return "@99\tother\n", nil
			}
			return "", errors.New("can't find window")
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions/"+created.SessionID+"/mark-unread", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.TurnAcknowledgedGeneration = 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted session:\n got %#v ok=%v\nwant %#v", persisted, ok, want)
	}
	wantCalls := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestManagerAPIRenamesStaleHostedSession(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"session_label":"solve problem B"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.SessionLabel = "solve problem B"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, want)
	}
}

func TestManagerAPIRenamesActiveHostedSessionWindow(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{
		StateDir: stateDir,
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{
				SocketName:  "ainn-test",
				HostSession: "ainn-test-host",
			},
		},
	}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	var gotCalls [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			gotCalls = append(gotCalls, append([]string{}, args...))
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "@12\tsolve problem A\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"session_label":"solve problem B"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantSession := created
	wantSession.SessionLabel = "solve problem B"
	if !reflect.DeepEqual(got, wantSession) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, wantSession)
	}
	wantCalls := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		{"tmux", "-L", "ainn-test", "rename-window", "-t", "ainn-test-host:@12", "solve problem B"},
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got tmux calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestManagerAPIHostedSessionRenameRejectsDuplicateLabel(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem B",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"session_label":"solve problem B"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusConflict {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"error": `hosted session label "solve problem B" already exists`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestManagerAPIPatchesStaleHostedSessionWorker(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {Port: 11199, Upstream: "openai", Launcher: "codex"},
				"cli-local":  {Port: 11200, Upstream: "openai", Launcher: "codex"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "@99\tother\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"worker_name":"cli-local"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionRecord
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := created
	want.WorkerName = "cli-local"
	want.WorkerPort = 11200
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected response:\n got %#v\nwant %#v", got, want)
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, want) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, want)
	}
}

func TestManagerAPIHostedSessionWorkerPatchRejectsActiveSession(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {Port: 11199, Upstream: "openai", Launcher: "codex"},
				"cli-local":  {Port: 11200, Upstream: "openai", Launcher: "codex"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "@12\tsolve problem A\n", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"worker_name":"cli-local"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected active hosted session conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "active hosted session") {
		t.Fatalf("conflict response did not explain active hosted session: %s", res.Body.String())
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, created) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, created)
	}
}

func TestManagerAPIHostedSessionWorkerPatchRejectsLauncherChange(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{
		Config: config.Config{
			Settings: settings,
			Workers: map[string]config.WorkerConfig{
				"cli-openai":  {Port: 11199, Upstream: "openai", Launcher: "codex"},
				"claude-main": {Port: 11200, Upstream: "openai", Launcher: "claudecode"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			switch {
			case strings.Join(args, " ") == strings.Join(TmuxHasSessionCommandForSettings(settings), " "):
				return "", nil
			case strings.Join(args, " ") == strings.Join(TmuxListWindowDetailsCommandForSettings(settings), " "):
				return "", nil
			default:
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"worker_name":"claude-main"}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected launcher conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "launcher") {
		t.Fatalf("conflict response did not explain launcher mismatch: %s", res.Body.String())
	}
	persisted, ok, err := registry.Get(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected hosted session %q", created.SessionID)
	}
	if !reflect.DeepEqual(persisted, created) {
		t.Fatalf("unexpected persisted session:\n got %#v\nwant %#v", persisted, created)
	}
}

func TestManagerSyncsCodexProfilesOnStartup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIFormat: "responses"},
			},
		},
	})

	data, err := os.ReadFile(filepath.Join(home, ".codex", "cli-openai.config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model_provider = 'OpenAI'`) {
		t.Fatalf("unexpected profile file: %s", data)
	}
	if !strings.Contains(string(data), `[model_providers.OpenAI]`) {
		t.Fatalf("unexpected profile file: %s", data)
	}
	if !strings.Contains(string(data), `base_url = 'http://127.0.0.1:11199'`) {
		t.Fatalf("unexpected profile file: %s", data)
	}
}

func TestManagerLogSinkWritesToDefaultHomeLogDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	sink := m.LogSink("app")
	if sink == nil {
		t.Fatal("expected log sink")
	}
	if _, err := sink.Write([]byte("WARN upstream closed early\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(home, ".ainn", "logs", "worker-6767.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected default log file at %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "WARN upstream closed early") {
		t.Fatalf("unexpected log file content: %s", data)
	}
}

func TestManagerLogSinkWritesToSettingsLogDir(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	m := New(Config{
		Config: config.Config{
			Settings: config.Settings{LogDir: logDir},
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	sink := m.LogSink("app")
	if sink == nil {
		t.Fatal("expected log sink")
	}
	if _, err := sink.Write([]byte("WARN upstream closed early\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(logDir, "worker-6767.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected settings log file at %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "WARN upstream closed early") {
		t.Fatalf("unexpected log file content: %s", data)
	}
}

func TestManagerBuildsWorkerRuntimeConfigForFDWithoutSecretInArgs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})

	spawn, err := m.BuildWorkerSpawn("codex-app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(spawn.Args, " "), "sk-secret") {
		t.Fatalf("secret leaked into argv: %#v", spawn.Args)
	}
	if !strings.Contains(string(spawn.RuntimeJSON), "sk-secret") {
		t.Fatalf("runtime fd payload missing resolved secret: %s", spawn.RuntimeJSON)
	}
	var decoded appruntime.WorkerRuntime
	if err := json.Unmarshal(spawn.RuntimeJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "codex-app" || decoded.ListenPort != 6767 || decoded.Generation != 1 {
		t.Fatalf("bad runtime payload: %#v", decoded)
	}
	if decoded.Upstream.ID != "openai" || decoded.Upstream.APIKey != "sk-secret" {
		t.Fatalf("bad runtime payload: %#v", decoded)
	}
}

func TestManagerStartWorkerUsesProviderSecretWhenConfigured(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		Starter: starter,
	})

	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected one worker spawn, got %d", len(starter.spawns))
	}
	if !strings.Contains(string(starter.spawns[0].RuntimeJSON), "sk-env") {
		t.Fatalf("expected env secret in runtime payload, got %s", starter.spawns[0].RuntimeJSON)
	}
}

func TestManagerWorkerSummariesExposeRole(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Role: "app", Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	summaries := m.workerSummaries()
	if len(summaries) != 1 || summaries[0].Role != "app" {
		t.Fatalf("expected worker role in summaries: %#v", summaries)
	}
}

func TestManagerWorkerSummaryUsesStableIDsAndDisplayNames(t *testing.T) {
	cfg := testManagerConfig()
	worker := cfg.Workers["app"]
	worker.Name = "Codex Main"
	worker.UpstreamID = "openai"
	worker.Upstream = ""
	cfg.Workers["app"] = worker
	profile := cfg.Upstreams["openai"]
	profile.Name = "OpenAI Display"
	cfg.Upstreams["openai"] = profile

	m := New(Config{Config: cfg})
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workers) != 1 {
		t.Fatalf("expected one worker, got %#v", body.Workers)
	}
	got := struct {
		ID         string
		Name       string
		UpstreamID string
		Upstream   upstream.RedactedUpstream
	}{body.Workers[0].ID, body.Workers[0].Name, body.Workers[0].UpstreamID, body.Workers[0].Upstream}
	want := struct {
		ID         string
		Name       string
		UpstreamID string
		Upstream   upstream.RedactedUpstream
	}{"app", "Codex Main", "openai", upstream.RedactedUpstream{ID: "openai", Name: "OpenAI Display", BaseURL: cfg.Upstreams["openai"].BaseURL, HasAPIKey: true}}
	if got != want {
		t.Fatalf("bad worker summary:\nwant %#v\ngot  %#v", want, got)
	}
}

func TestManagerWorkerSummaryReportsMissingUpstream(t *testing.T) {
	cfg := testManagerConfig()
	worker := cfg.Workers["app"]
	worker.UpstreamID = "missing-upstream"
	worker.Upstream = ""
	cfg.Workers["app"] = worker
	delete(cfg.Upstreams, "openai")

	m := New(Config{Config: cfg})
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workers) != 1 {
		t.Fatalf("expected one worker, got %#v", body.Workers)
	}
	want := upstream.RedactedUpstream{ID: "missing-upstream", Name: "missing-upstream", Missing: true}
	if body.Workers[0].Upstream != want {
		t.Fatalf("bad missing upstream:\nwant %#v\ngot  %#v", want, body.Workers[0].Upstream)
	}
}

func testManagerConfig() config.Config {
	return config.Config{
		Plugins: testPluginDefinitions(),
		Workers: map[string]config.WorkerConfig{
			"app": {Port: 6767, Upstream: "openai"},
		},
		Upstreams: map[string]config.UpstreamProfile{
			"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
		},
	}
}

func TestManagerAPITogglesConfiguredWorkerModule(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"tool_filter": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/modules/tool_filter/toggle", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected toggle status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"enabled":true`) {
		t.Fatalf("toggle response did not enable module: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if !strings.Contains(res.Body.String(), `"enabled":true`) {
		t.Fatalf("worker list did not reflect toggle: %s", res.Body.String())
	}
	if client.toggledPort != 6767 || client.toggledModule != "tool_filter" {
		t.Fatalf("live worker toggle was not called: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIExposesConfiguredWorkerPluginBindingsAndRegistrySupport(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"plain": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	var got WorkerDetail
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.LogLevel != "simple" || len(got.Modules) != 0 || len(got.Hooks) != 0 {
		t.Fatalf("bad worker bindings:\ngot modules=%#v hooks=%#v log=%q\nwant no modules, no hooks, log=%q", got.Modules, got.Hooks, got.LogLevel, "simple")
	}
	wantSupport := map[string]appruntime.ModuleProtocolSupport{
		"debug_sse": {
			Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolResponses},
			Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityStreamEvents},
		},
		"api_translate": {
			Protocols: []appruntime.ProtocolKind{
				appruntime.ProtocolResponses,
				appruntime.ProtocolChatCompletions,
			},
			Capabilities: []appruntime.ProtocolCapability{
				appruntime.ProtocolCapabilityInputText,
				appruntime.ProtocolCapabilityToolCalls,
				appruntime.ProtocolCapabilityStreamEvents,
			},
		},
	}
	for name, want := range wantSupport {
		if !reflect.DeepEqual(got.ModuleSupport[name], want) {
			t.Fatalf("bad support for %s:\ngot  %#v\nwant %#v", name, got.ModuleSupport[name], want)
		}
	}
}

func TestManagerAPIExposesExternalPluginDefinitionSupportWhenStopped(t *testing.T) {
	dir := t.TempDir()
	requestManifestPath := filepath.Join(dir, "external_filter.yaml")
	if err := os.WriteFile(requestManifestPath, []byte(`name: external_filter
kind: request_middleware
version: 0.1.0
protocol_version: "2"
command: /bin/cat
protocols:
  - responses
capabilities:
  - input_text
`), 0600); err != nil {
		t.Fatal(err)
	}
	hookManifestPath := filepath.Join(dir, "external_hook.yaml")
	if err := os.WriteFile(hookManifestPath, []byte(`name: external_hook
kind: lifecycle_hook
version: 0.1.0
protocol_version: "1"
command: /bin/cat
protocols:
  - anthropic
capabilities:
  - stream_events
`), 0600); err != nil {
		t.Fatal(err)
	}
	plugins := testPluginDefinitions()
	plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   requestManifestPath,
	}
	plugins["external_hook"] = config.PluginDefinition{
		Kind:   config.PluginKindLifecycleHook,
		Source: config.PluginSourceExternal,
		Path:   hookManifestPath,
	}
	m := New(Config{
		Config: config.Config{
			Plugins: plugins,
			Workers: map[string]config.WorkerConfig{
				"plain": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	var detail WorkerDetail
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	wantDetailSupport := map[string]appruntime.ModuleProtocolSupport{
		"external_filter": {
			Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolResponses},
			Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityInputText},
		},
		"external_hook": {
			Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolAnthropic},
			Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityStreamEvents},
		},
	}
	for name, want := range wantDetailSupport {
		if !reflect.DeepEqual(detail.ModuleSupport[name], want) {
			t.Fatalf("bad detail support for %s:\ngot  %#v\nwant %#v", name, detail.ModuleSupport[name], want)
		}
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected workers status %d: %s", res.Code, res.Body.String())
	}
	var list struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Workers) != 1 {
		t.Fatalf("expected one worker, got %#v", list.Workers)
	}
	for name, want := range wantDetailSupport {
		if !reflect.DeepEqual(list.Workers[0].ModuleSupport[name], want) {
			t.Fatalf("bad summary support for %s:\ngot  %#v\nwant %#v", name, list.Workers[0].ModuleSupport[name], want)
		}
	}
}

func TestManagerAPIRunningWorkerOverlaysLiveSupportOnPluginDefinitions(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "external_filter.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: external_filter
kind: request_middleware
version: 0.1.0
protocol_version: "2"
command: /bin/cat
protocols:
  - anthropic
capabilities:
  - input_text
`), 0600); err != nil {
		t.Fatal(err)
	}
	plugins := testPluginDefinitions()
	plugins["external_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	client := &recordingWorkerClient{
		statusBody: `{"snapshot_generation":7,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1"},"module_support":{"debug_sse":{"protocols":["chat_completions"],"capabilities":["stream_events"]}}}`,
	}
	m := New(Config{
		Config: config.Config{
			Plugins: plugins,
			Workers: map[string]config.WorkerConfig{
				"plain": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["plain"] = "running"

	wantExternal := appruntime.ModuleProtocolSupport{
		Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolAnthropic},
		Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityInputText},
	}
	wantLive := appruntime.ModuleProtocolSupport{
		Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolChatCompletions},
		Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityStreamEvents},
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	var detail WorkerDetail
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(detail.ModuleSupport["external_filter"], wantExternal) {
		t.Fatalf("bad detail support for external_filter:\ngot  %#v\nwant %#v", detail.ModuleSupport["external_filter"], wantExternal)
	}
	if !reflect.DeepEqual(detail.ModuleSupport["debug_sse"], wantLive) {
		t.Fatalf("bad detail support for debug_sse:\ngot  %#v\nwant %#v", detail.ModuleSupport["debug_sse"], wantLive)
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected workers status %d: %s", res.Code, res.Body.String())
	}
	var list struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Workers) != 1 {
		t.Fatalf("expected one worker, got %#v", list.Workers)
	}
	if !reflect.DeepEqual(list.Workers[0].ModuleSupport["external_filter"], wantExternal) {
		t.Fatalf("bad summary support for external_filter:\ngot  %#v\nwant %#v", list.Workers[0].ModuleSupport["external_filter"], wantExternal)
	}
	if !reflect.DeepEqual(list.Workers[0].ModuleSupport["debug_sse"], wantLive) {
		t.Fatalf("bad summary support for debug_sse:\ngot  %#v\nwant %#v", list.Workers[0].ModuleSupport["debug_sse"], wantLive)
	}
}

func TestManagerAPIStoppedWorkerUsesExternalSupportForBuiltinRequestName(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "tool_filter.yaml")
	if err := os.WriteFile(manifestPath, []byte(`name: tool_filter
kind: request_middleware
version: 0.1.0
protocol_version: "2"
command: `+pluginPath+`
protocols:
  - chat_completions
capabilities:
  - input_text
`), 0600); err != nil {
		t.Fatal(err)
	}
	plugins := testPluginDefinitions()
	plugins["tool_filter"] = config.PluginDefinition{
		Kind:   config.PluginKindRequestMiddleware,
		Source: config.PluginSourceExternal,
		Path:   manifestPath,
	}
	m := New(Config{
		Config: config.Config{
			Plugins: plugins,
			Workers: map[string]config.WorkerConfig{
				"plain": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	var detail WorkerDetail
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	want := appruntime.ModuleProtocolSupport{
		Protocols:    []appruntime.ProtocolKind{appruntime.ProtocolChatCompletions},
		Capabilities: []appruntime.ProtocolCapability{appruntime.ProtocolCapabilityInputText},
	}
	if !reflect.DeepEqual(detail.ModuleSupport["tool_filter"], want) {
		t.Fatalf("bad detail support for tool_filter:\ngot  %#v\nwant %#v", detail.ModuleSupport["tool_filter"], want)
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected workers status %d: %s", res.Code, res.Body.String())
	}
	var list struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Workers) != 1 {
		t.Fatalf("expected one worker, got %#v", list.Workers)
	}
	if !reflect.DeepEqual(list.Workers[0].ModuleSupport["tool_filter"], want) {
		t.Fatalf("bad summary support for tool_filter:\ngot  %#v\nwant %#v", list.Workers[0].ModuleSupport["tool_filter"], want)
	}
}

func TestManagerAPIWorkerDetailIncludesProviderFieldsAndConfigPatchState(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-live")
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-live"}},
					},
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true},
					},
					LogLevel: "detail",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file", APIFormat: "chat_completions"},
			},
		},
		WorkerClient: &recordingWorkerClient{
			statusBody: `{"snapshot_generation":7,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1","has_api_key":true,"api_format":"chat_completions"},"modules":{"model_override":{"enabled":true,"params":{"model":"gpt-live"}}},"hooks":{"config_patch":{"enabled":true}},"hook_statuses":{"config_patch":{"state":"unresolved","detail":{"provider_name":"test","field_name":"base_url","previous_value":"https://example.com/v1","patched_value":"http://127.0.0.1:6767","current_value":"https://manual.example/v1"}}}}`,
		},
	})
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	for _, want := range []string{
		`"base_url":"https://api.openai.com/v1"`,
		`"has_api_key":true`,
		`"api_format":"chat_completions"`,
		`"snapshot_generation":7`,
		`"log_level":"detail"`,
		`"hook_statuses":{"config_patch":{"state":"unresolved"`,
		`"detail"`,
		`"current_value":"https://manual.example/v1"`,
		`"hooks":{"config_patch":{"enabled":true`,
		`"model":"gpt-live"`,
	} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("worker detail missing %s: %s", want, res.Body.String())
		}
	}
	if strings.Contains(res.Body.String(), "config_patch_state") || strings.Contains(res.Body.String(), "config_patch_detail") {
		t.Fatalf("worker detail included old config_patch fields: %s", res.Body.String())
	}
}

func TestManagerAPIWorkerSummaryAndDetailIncludeProxyURL(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					ProxyURL: "http://user:pass@127.0.0.1:7890",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected workers status %d: %s", res.Code, res.Body.String())
	}
	var list struct {
		Workers []WorkerSummary `json:"workers"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Body.String(), "pass") || strings.Contains(res.Body.String(), "user") {
		t.Fatalf("workers list leaked proxy credentials: %s", res.Body.String())
	}
	wantList := struct {
		Workers []WorkerSummary `json:"workers"`
	}{
		Workers: []WorkerSummary{
			{
				ID:                 "app",
				Name:               "app",
				Port:               6767,
				Role:               "cli",
				Launcher:           "codex",
				UpstreamID:         "openai",
				Upstream:           upstream.RedactedUpstream{ID: "openai", Name: "openai", BaseURL: "https://api.openai.com/v1"},
				ProxyURL:           "http://127.0.0.1:7890",
				ProxyURLRedacted:   true,
				Protocol:           appruntime.ProtocolResponses,
				ModuleSupport:      supportForPluginDefinitions(testPluginDefinitions()),
				Status:             "configured",
				SnapshotGeneration: 1,
				LogLevel:           "simple",
			},
		},
	}
	if !reflect.DeepEqual(list, wantList) {
		t.Fatalf("bad workers list:\ngot  %#v\nwant %#v", list, wantList)
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	var detail WorkerDetail
	if err := json.Unmarshal(res.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Body.String(), "pass") || strings.Contains(res.Body.String(), "user") {
		t.Fatalf("worker detail leaked proxy credentials: %s", res.Body.String())
	}
	wantDetail := WorkerDetail{
		ID:                 "app",
		Name:               "app",
		Port:               6767,
		Role:               "cli",
		Launcher:           "codex",
		UpstreamID:         "openai",
		Upstream:           upstream.RedactedUpstream{ID: "openai", Name: "openai", BaseURL: "https://api.openai.com/v1"},
		ProxyURL:           "http://127.0.0.1:7890",
		ProxyURLRedacted:   true,
		Protocol:           appruntime.ProtocolResponses,
		ModuleSupport:      supportForPluginDefinitions(testPluginDefinitions()),
		Status:             "configured",
		SnapshotGeneration: 1,
		LogLevel:           "simple",
	}
	if !reflect.DeepEqual(detail, wantDetail) {
		t.Fatalf("bad worker detail:\ngot  %#v\nwant %#v", detail, wantDetail)
	}
}

func TestManagerAPIWorkerDetailIncludesGenericHookStatuses(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: map[string]config.PluginDefinition{
				"external_hook": {Kind: config.PluginKindLifecycleHook, Source: config.PluginSourceExternal, Path: "/tmp/plugin.yaml"},
			},
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"external_hook": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: &recordingWorkerClient{
			statusBody: `{"snapshot_generation":4,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1"},"hooks":{"external_hook":{"enabled":true}},"hook_statuses":{"external_hook":{"state":"active","detail":{"message":"started"}}}}`,
		},
	})
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected worker detail status %d: %s", res.Code, res.Body.String())
	}
	for _, want := range []string{
		`"hook_statuses":{"external_hook":{"state":"active"`,
		`"message":"started"`,
		`"hooks":{"external_hook":{"enabled":true`,
	} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("worker detail missing %s: %s", want, res.Body.String())
		}
	}
	if strings.Contains(res.Body.String(), "config_patch_state") || strings.Contains(res.Body.String(), "config_patch_detail") {
		t.Fatalf("worker detail included old config_patch fields: %s", res.Body.String())
	}
}

func TestManagerAPIPatchesRunningWorkerProxyURL(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["cli"] = "running"

	body := strings.NewReader(`{"port":11199,"upstream":"openai","proxy_url":"http://127.0.0.1:7890"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	gotConfig, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	wantConfig := config.WorkerConfig{
		Name:           "cli",
		Role:           "cli",
		Launcher:       "codex",
		Port:           11199,
		Upstream:       "openai",
		UpstreamID:     "openai",
		ProxyURL:       "http://127.0.0.1:7890",
		LogLevel:       "simple",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("bad worker config:\ngot  %#v\nwant %#v", gotConfig, wantConfig)
	}
	wantRuntime := appruntime.WorkerRuntime{
		ID:         "cli",
		Generation: 2,
		ListenPort: 11199,
		Role:       "cli",
		LogLevel:   "simple",
		ProxyURL:   "http://127.0.0.1:7890",
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "https://api.openai.com/v1",
		},
		Modules: map[string]appruntime.ModuleConfig{},
		Hooks:   map[string]appruntime.ModuleConfig{},
	}
	if !reflect.DeepEqual(client.appliedRuntime, wantRuntime) {
		t.Fatalf("bad applied runtime:\ngot  %#v\nwant %#v", client.appliedRuntime, wantRuntime)
	}
}

func TestManagerAPIRejectsInvalidWorkerProxyURL(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					ProxyURL: "http://127.0.0.1:7890",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["cli"] = "running"

	body := strings.NewReader(`{"port":11199,"upstream":"openai","proxy_url":"localhost:7890"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	gotConfig, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	wantConfig := config.WorkerConfig{
		Name:           "cli",
		Role:           "cli",
		Launcher:       "codex",
		Port:           11199,
		Upstream:       "openai",
		UpstreamID:     "openai",
		ProxyURL:       "http://127.0.0.1:7890",
		LogLevel:       "simple",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("bad worker config:\ngot  %#v\nwant %#v", gotConfig, wantConfig)
	}
	if !reflect.DeepEqual(client.appliedRuntime, appruntime.WorkerRuntime{}) {
		t.Fatalf("invalid proxy URL was applied: %#v", client.appliedRuntime)
	}
}

func TestManagerAPIPreservesWorkerProxyURLWhenPatchOmitsField(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					ProxyURL: "http://127.0.0.1:7890",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["cli"] = "running"

	body := strings.NewReader(`{"port":11199,"upstream":"openai","log_level":"detail"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	gotConfig, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	wantConfig := config.WorkerConfig{
		Name:           "cli",
		Role:           "cli",
		Launcher:       "codex",
		Port:           11199,
		Upstream:       "openai",
		UpstreamID:     "openai",
		ProxyURL:       "http://127.0.0.1:7890",
		LogLevel:       "detail",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("bad worker config:\ngot  %#v\nwant %#v", gotConfig, wantConfig)
	}
	wantRuntime := appruntime.WorkerRuntime{
		ID:         "cli",
		Generation: 2,
		ListenPort: 11199,
		Role:       "cli",
		LogLevel:   "detail",
		ProxyURL:   "http://127.0.0.1:7890",
		Upstream: appruntime.UpstreamRuntime{
			ID:      "openai",
			BaseURL: "https://api.openai.com/v1",
		},
		Modules: map[string]appruntime.ModuleConfig{},
		Hooks:   map[string]appruntime.ModuleConfig{},
	}
	if !reflect.DeepEqual(client.appliedRuntime, wantRuntime) {
		t.Fatalf("bad applied runtime:\ngot  %#v\nwant %#v", client.appliedRuntime, wantRuntime)
	}
}

func TestManagerUpdateWorkerRestartsRunningWorkerWhenHooksChange(t *testing.T) {
	starter := &recordingStarter{}
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:      starter,
		WorkerClient: client,
	})
	if err := m.StartWorker("cli"); err != nil {
		t.Fatal(err)
	}
	current, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	next := current
	next.Hooks = map[string]config.ModuleConfig{
		"config_patch": {Enabled: false, Params: map[string]any{"config_path": "~/.codex/config.toml"}},
	}

	if err := m.UpdateWorker("cli", current, next); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected hook config change to restart worker, got %d spawns", len(starter.spawns))
	}
	if client.appliedPort != 0 || !reflect.DeepEqual(client.appliedRuntime, appruntime.WorkerRuntime{}) {
		t.Fatalf("hook config change was hot-applied: port=%d runtime=%#v", client.appliedPort, client.appliedRuntime)
	}
}

func TestManagerAPIUpdatesWorkerLogLevel(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	body := strings.NewReader(`{"port":11199,"upstream":"openai","log_level":"detail","request_modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"log_level":"detail"`) {
		t.Fatalf("update response did not expose detail log level: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if !strings.Contains(res.Body.String(), `"log_level":"detail"`) {
		t.Fatalf("worker detail did not persist log level: %s", res.Body.String())
	}
}

func TestManagerAPIPatchesRunningWorkerLogLevelWithoutRecheckingCurrentPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     port,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:      fakeStarter{},
		WorkerClient: &recordingWorkerClient{},
	})
	if err := m.StartWorker("cli"); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(fmt.Sprintf(`{"port":%d,"upstream":"openai","log_level":"detail","request_modules":{"api_translate":{"enabled":true}}}`, port))
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, fmt.Sprintf("http://manager.local/api/workers/%d", port), body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}

	got, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Name:       "cli",
		Role:       "cli",
		Launcher:   "codex",
		Port:       port,
		Upstream:   "openai",
		UpstreamID: "openai",
		LogLevel:   "detail",
		RequestModules: map[string]config.ModuleConfig{
			"api_translate": {Enabled: true},
		},
		Hooks: map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker config %#v", got)
	}
}

func TestManagerAPIPatchWorkerRejectsUndefinedPluginBeforePersisting(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	body := strings.NewReader(`{"port":11199,"upstream":"openai","log_level":"simple","request_modules":{"missing":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `references undefined plugin`) {
		t.Fatalf("response did not explain runtime validation failure: %s", res.Body.String())
	}
	got, ok := m.workerConfig("cli")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Name:       "cli",
		Role:       "cli",
		Launcher:   "codex",
		Port:       11199,
		Upstream:   "openai",
		UpstreamID: "openai",
		LogLevel:   "simple",
		RequestModules: map[string]config.ModuleConfig{
			"api_translate": {Enabled: true},
		},
		Hooks: map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invalid patch was persisted:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestManagerAPIPatchesConfiguredWorkerModule(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"model_override": {Enabled: false, Params: map[string]any{"model": "old-model"}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true,"params":{"model":"new-model"}}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/6767/modules/model_override", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"enabled":true`) || !strings.Contains(res.Body.String(), `"model":"new-model"`) {
		t.Fatalf("patch response did not include updated module: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if !strings.Contains(res.Body.String(), `"enabled":true`) || !strings.Contains(res.Body.String(), `"model":"new-model"`) {
		t.Fatalf("worker detail did not reflect module patch: %s", res.Body.String())
	}
	if client.patchedPort != 6767 || client.patchedModule != "model_override" || !client.patchedConfig.Enabled || client.patchedConfig.Params["model"] != "new-model" {
		t.Fatalf("live worker patch was not called: port=%d module=%s config=%#v", client.patchedPort, client.patchedModule, client.patchedConfig)
	}
}

func TestManagerAPIPatchesUnconfiguredWorkerModuleWithRuntimeApply(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:           6767,
					Upstream:       "openai",
					RequestModules: map[string]config.ModuleConfig{},
					Hooks:          map[string]config.ModuleConfig{},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true,"params":{"model":"gpt-live"}}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/6767/modules/model_override", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected patch status %d: %s", res.Code, res.Body.String())
	}

	wantWorker := config.WorkerConfig{
		Name:       "app",
		Role:       "cli",
		Launcher:   "codex",
		Port:       6767,
		Upstream:   "openai",
		UpstreamID: "openai",
		LogLevel:   "simple",
		RequestModules: map[string]config.ModuleConfig{
			"model_override": {Enabled: true, Params: map[string]any{"model": "gpt-live"}},
		},
		Hooks: map[string]config.ModuleConfig{},
	}
	if got := m.store.Config().Workers["app"]; !reflect.DeepEqual(got, wantWorker) {
		t.Fatalf("worker config mismatch:\ngot  %#v\nwant %#v", got, wantWorker)
	}

	wantRuntime, err := (RuntimeBuilder{}).Build(m.store.Config(), "app", appruntime.Generation(m.workerGeneration("app")))
	if err != nil {
		t.Fatal(err)
	}
	if got := client.appliedRuntimes; !reflect.DeepEqual(got, map[int]appruntime.WorkerRuntime{6767: wantRuntime}) {
		t.Fatalf("runtime apply mismatch:\ngot  %#v\nwant %#v", got, map[int]appruntime.WorkerRuntime{6767: wantRuntime})
	}
	gotPatch := struct {
		Port   int
		Module string
		Config config.ModuleConfig
	}{Port: client.patchedPort, Module: client.patchedModule, Config: client.patchedConfig}
	if !reflect.DeepEqual(gotPatch, struct {
		Port   int
		Module string
		Config config.ModuleConfig
	}{}) {
		t.Fatalf("unexpected live patch call: %#v", gotPatch)
	}
}

func TestManagerAPIToggleRejectsSecondRunningConfigPatch(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true},
					},
				},
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/11199/modules/config_patch/toggle", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch already active on another worker") {
		t.Fatalf("conflict response did not explain config_patch ownership: %s", res.Body.String())
	}
	if client.toggledPort != 0 || client.toggledModule != "" {
		t.Fatalf("live worker toggle should not be called on rejected config_patch: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIToggleRejectsConfigPatchWhenWorkerRecoveryStateIsUnresolved(t *testing.T) {
	client := &recordingWorkerClient{
		statusBody: `{"snapshot_generation":3,"upstream":{"name":"openai","base_url":"https://api.openai.com/v1"},"hooks":{"config_patch":{"enabled":false}},"hook_statuses":{"config_patch":{"state":"unresolved"}}}`,
	}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/11199/modules/config_patch/toggle", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected unresolved config_patch toggle conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch recovery state unresolved") {
		t.Fatalf("missing unresolved recovery error: %s", res.Body.String())
	}
	if client.toggledPort != 0 || client.toggledModule != "" {
		t.Fatalf("live worker toggle should not be called on unresolved config_patch: port=%d module=%s", client.toggledPort, client.toggledModule)
	}
}

func TestManagerAPIPatchRejectsSecondRunningConfigPatchEnable(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true},
					},
				},
				"cli": {
					Port:     11199,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	m.statuses["codex-app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199/modules/config_patch", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "config_patch already active on another worker") {
		t.Fatalf("conflict response did not explain config_patch ownership: %s", res.Body.String())
	}
	if client.patchedPort != 0 || client.patchedModule != "" {
		t.Fatalf("live worker patch should not be called on rejected config_patch: port=%d module=%s", client.patchedPort, client.patchedModule)
	}
}

func TestManagerAPIPatchConfigPatchRestartsWorkerWithoutLiveModulePatch(t *testing.T) {
	starter := &recordingStarter{}
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: false},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:      starter,
		WorkerClient: client,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected initial worker spawn, got %d", len(starter.spawns))
	}

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"enabled":true,"params":{"config_path":"/tmp/codex-config.toml","state_dir":"/tmp/ainn"}}`)
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/6767/modules/config_patch", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected config_patch patch status %d: %s", res.Code, res.Body.String())
	}
	if client.patchedPort != 0 || client.patchedModule != "" {
		t.Fatalf("config_patch patch should not hit live module patch: port=%d module=%s", client.patchedPort, client.patchedModule)
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected config_patch patch to restart worker, got %d spawns", len(starter.spawns))
	}
	if len(starter.processes) < 2 || starter.processes[0].stops != 1 {
		t.Fatalf("expected old worker process to stop during restart, got %#v", starter.processes)
	}
}

func TestManagerAPICreatesAndStartsWorker(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	body := strings.NewReader(`{"name":"cli-openai","port":11199,"upstream":"openai","request_modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", res.Code, res.Body.String())
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected worker to be started, got %d spawns", len(starter.spawns))
	}
	if !strings.Contains(res.Body.String(), `"name":"cli-openai"`) || !strings.Contains(res.Body.String(), `"status":"running"`) {
		t.Fatalf("create response missing worker summary: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"api_translate":{"enabled":true`) {
		t.Fatalf("created worker not visible in manager API: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestManagerAPICreatesClaudeCodeWorker(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{},
			Upstreams: map[string]config.UpstreamProfile{
				"anthropic": {BaseURL: "https://api.anthropic.com/v1", APIFormat: "anthropic"},
			},
		},
		Starter: starter,
	})

	body := strings.NewReader(`{"name":"claude-main","port":11201,"upstream":"anthropic","launcher":"claudecode"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", res.Code, res.Body.String())
	}
	got, ok := m.workerConfig("claude-main")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Name:           "claude-main",
		Role:           "cli",
		Launcher:       "claudecode",
		Port:           11201,
		Upstream:       "anthropic",
		UpstreamID:     "anthropic",
		LogLevel:       "simple",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker config:\ngot  %#v\nwant %#v", got, want)
	}
	if !strings.Contains(res.Body.String(), `"launcher":"claudecode"`) || !strings.Contains(res.Body.String(), `"protocol":"anthropic"`) {
		t.Fatalf("create response missing claudecode fields: %s", res.Body.String())
	}
}

func TestManagerAPICreatesWorkerWithAllocatedPort(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	body := strings.NewReader(`{"name":"cli-openai","upstream":"openai"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusCreated {
		t.Fatalf("unexpected create status %d: %s", res.Code, res.Body.String())
	}
	var summary WorkerSummary
	if err := json.NewDecoder(res.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.Port <= 0 {
		t.Fatalf("expected allocated port, got %#v", summary)
	}
	got, ok := m.workerConfig("cli-openai")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Name:           "cli-openai",
		Role:           "cli",
		Launcher:       "codex",
		Port:           summary.Port,
		Upstream:       "openai",
		UpstreamID:     "openai",
		LogLevel:       "simple",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected worker config:\ngot  %#v\nwant %#v", got, want)
	}
	if len(starter.spawns) != 1 {
		t.Fatalf("expected worker to be started, got %d spawns", len(starter.spawns))
	}
}

func TestManagerAPICreateWorkerRejectsManagedPortConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	body := strings.NewReader(`{"name":"duplicate","port":6767,"upstream":"openai"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected port conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "worker 'app'") {
		t.Fatalf("conflict response did not name owning worker: %s", res.Body.String())
	}
}

func TestManagerAPIUpdatesWorkerPortByRespawning(t *testing.T) {
	starter := &recordingStarter{}
	checker := &recordingHealthChecker{results: map[int]bool{11200: true}}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {
					Port:     11199,
					Upstream: "openai",
					RequestModules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:       starter,
		HealthChecker: checker,
	})
	if err := m.StartWorker("cli-openai"); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"port":11200,"upstream":"openai","request_modules":{"api_translate":{"enabled":true}}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected update status %d: %s", res.Code, res.Body.String())
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected worker respawn on new port, got %d spawns", len(starter.spawns))
	}
	if starter.processes[0].stops != 1 {
		t.Fatalf("expected old process stopped after new port spawn, got %d stops", starter.processes[0].stops)
	}
	if checker.calls[11200] == 0 {
		t.Fatalf("expected manager to health-check the new port before stopping old worker")
	}
	if !strings.Contains(res.Body.String(), `"port":11200`) || !strings.Contains(res.Body.String(), `"status":"running"`) {
		t.Fatalf("update response did not expose new worker summary: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11199", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("old port still resolves after port update: %d %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/11200", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"cli-openai"`) {
		t.Fatalf("new port did not resolve after update: %d %s", res.Code, res.Body.String())
	}
}

func TestManagerAPIWorkerPortUpdateRejectsConflict(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	body := strings.NewReader(`{"port":6767,"upstream":"openai"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected port conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "worker 'app'") {
		t.Fatalf("conflict response did not name owning worker: %s", res.Body.String())
	}
}

func TestManagerAPIWorkerPortUpdateRejectsActiveHostedSession(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nextPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	m := New(Config{
		Config: config.Config{
			Settings: config.Settings{StateDir: stateDir},
			Plugins:  testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"cli-openai": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	_, err = registry.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			if strings.Contains(strings.Join(args, " "), "list-windows") {
				return "@12\tsolve problem A\n", nil
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	body := strings.NewReader(fmt.Sprintf(`{"port":%d,"upstream":"openai"}`, nextPort))
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/11199", body))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected active hosted session conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "active hosted session") {
		t.Fatalf("conflict response did not explain active hosted session: %s", res.Body.String())
	}
	got, ok := m.workerConfig("cli-openai")
	if !ok {
		t.Fatal("worker config missing")
	}
	want := config.WorkerConfig{
		Name:           "cli-openai",
		Role:           "cli",
		Launcher:       "codex",
		Port:           11199,
		Upstream:       "openai",
		UpstreamID:     "openai",
		LogLevel:       "simple",
		RequestModules: map[string]config.ModuleConfig{},
		Hooks:          map[string]config.ModuleConfig{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worker config changed:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestManagerWorkerLifecycleStateTransitions(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})

	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}
	assertWorkerStatus(t, m, "running")

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/restart", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected restart status %d: %s", res.Code, res.Body.String())
	}
	assertWorkerStatus(t, m, "running")

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", res.Code, res.Body.String())
	}
	assertWorkerStatus(t, m, "stopped")
}

func TestManagerAPIDeleteWorkerConfigStopsAndRemovesWorker(t *testing.T) {
	process := &recordingManagedProcess{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fixedStarter{process: process},
	})
	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/workers/6767/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", res.Code, res.Body.String())
	}
	if process.stopCount != 1 {
		t.Fatalf("expected worker process to stop once, got %d", process.stopCount)
	}
	if _, ok := m.workerConfig("codex-app"); ok {
		t.Fatal("expected worker config to be removed")
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected removed worker lookup to return 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestManagerAPIDeleteUpstreamRejectsReferencedProvider(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/upstreams/openai", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("expected upstream conflict, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "used by worker") {
		t.Fatalf("expected conflict response to name worker usage, got %s", res.Body.String())
	}
}

func TestManagerAPIDeleteUpstreamRemovesUnreferencedProvider(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai":    {BaseURL: "https://api.openai.com/v1"},
				"anthropic": {BaseURL: "https://api.anthropic.com/v1"},
			},
		},
	})

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "http://manager.local/api/upstreams/anthropic", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected delete status %d: %s", res.Code, res.Body.String())
	}
	if _, ok := m.upstreamProfileSnapshot()["anthropic"]; ok {
		t.Fatal("expected upstream profile to be removed")
	}
	if _, ok := m.upstreamProfileSnapshot()["openai"]; !ok {
		t.Fatal("expected referenced upstream to remain")
	}
}

func TestManagerReportsForcedStopState(t *testing.T) {
	starter := &recordingStarter{forcedStop: true}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"codex-app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("codex-app"); err != nil {
		t.Fatal(err)
	}

	if err := m.StopWorker("codex-app"); err != nil {
		t.Fatal(err)
	}

	assertWorkerStatus(t, m, "stopped (forced)")
}

func TestManagerStartConfiguredWorkers(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err != nil {
		t.Fatal(err)
	}
	if len(starter.spawns) != 2 {
		t.Fatalf("expected two worker spawns, got %d", len(starter.spawns))
	}
	assertWorkerStatus(t, m, "running")
}

func TestManagerStartConfiguredWorkersRecoversStaleConfigPatchBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://example.com/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	patch := modulehook.NewConfigPatch(module.ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"config_path": configPath,
			"state_dir":   filepath.Join(dir, "state"),
		},
	}, modulehook.BuildDependencies{
		WorkerID:   "worker-6767",
		WorkerPort: 6767,
	})
	if err := patch.Start(); err != nil {
		t.Fatal(err)
	}
	if err := patch.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true, Params: map[string]any{"config_path": configPath, "state_dir": filepath.Join(dir, "state")}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `base_url = "https://example.com/v1"`) {
		t.Fatalf("expected manager startup recovery to restore config, got:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "config-patch-journal.json")); !os.IsNotExist(err) {
		t.Fatalf("expected stale journal removed before spawn, got %v", err)
	}
}

func TestManagerStartConfiguredWorkersLeavesManualEditConflictUnresolvedBeforeSpawn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://example.com/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	patch := modulehook.NewConfigPatch(module.ModuleConfig{
		Enabled: true,
		Params: map[string]any{
			"config_path": configPath,
			"state_dir":   filepath.Join(dir, "state"),
		},
	}, modulehook.BuildDependencies{
		WorkerID:   "worker-6767",
		WorkerPort: 6767,
	})
	if err := patch.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join([]string{
		`model_provider = "test"`,
		``,
		`[model_providers.test]`,
		`base_url = "https://manual.example/v1"`,
		``,
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := patch.CloseLockForTest(); err != nil {
		t.Fatal(err)
	}

	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Hooks: map[string]config.ModuleConfig{
						"config_patch": {Enabled: true, Params: map[string]any{"config_path": configPath, "state_dir": filepath.Join(dir, "state")}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})

	if err := m.StartConfiguredWorkers(); err == nil {
		t.Fatal("expected unresolved startup recovery to fail before spawn")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `base_url = "https://manual.example/v1"`) {
		t.Fatalf("expected manual edit preserved, got:\n%s", data)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "state", "config-patch-journal.json.unresolved.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected unresolved journal after startup recovery, got %#v", matches)
	}
	if len(starter.spawns) != 0 {
		t.Fatalf("expected worker spawn blocked on unresolved recovery, got %d spawns", len(starter.spawns))
	}
}

func TestManagerStartWorkerSpawnLogUsesSpawnPortAfterConfigDeletion(t *testing.T) {
	var logBuf bytes.Buffer
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
		Logger:  logging.New(&logBuf, "detail", logging.ComponentManagerSuper),
	})
	starter.onStart = func(spawn WorkerSpawn) {
		m.updateConfig(func(cfgRoot *config.Config) {
			delete(cfgRoot.Workers, "app")
		})
	}

	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}

	line := logBuf.String()
	if !strings.Contains(line, logging.EventWorkerSpawn) || !strings.Contains(line, "port=6767") {
		t.Fatalf("spawn log should use spawned port, got %q", line)
	}
}

type recordingStarter struct {
	spawns     []WorkerSpawn
	processes  []*recordingProcess
	forcedStop bool
	onStart    func(WorkerSpawn)
}

func (s *recordingStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	s.spawns = append(s.spawns, spawn)
	if s.onStart != nil {
		s.onStart(spawn)
	}
	process := &recordingProcess{forcedStop: s.forcedStop}
	s.processes = append(s.processes, process)
	return process, nil
}

type recordingProcess struct {
	stops      int
	forcedStop bool
}

func (p *recordingProcess) Stop() error {
	p.stops++
	return nil
}

func (p *recordingProcess) ForcedStop() bool {
	return p.forcedStop
}

func TestManagerStartConfiguredWorkersReportsFailure(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"bad": {Port: 6767, Upstream: "missing"},
			},
		},
		Starter: fakeStarter{},
	})
	if err := m.StartConfiguredWorkers(); err == nil {
		t.Fatal("expected missing provider failure")
	}
}

func TestManagerConfigAndProviderPersistenceAPI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	client := &recordingWorkerClient{}
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://relay.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected provider update status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "relay.example") || strings.Contains(res.Body.String(), "sk-") {
		t.Fatalf("bad provider update response: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "http://manager.local/api/config", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected config save status %d: %s", res.Code, res.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "https://relay.example/v1") || !strings.Contains(string(data), "api_key: sk-file") || strings.Contains(string(data), "api_key_ref") {
		t.Fatalf("bad persisted config:\n%s", data)
	}
	if client.appliedRuntimes[6767].Upstream.ID != "openai" || client.appliedRuntimes[6767].Upstream.BaseURL != "https://relay.example/v1" || client.appliedRuntimes[6767].Upstream.APIFormat != "chat_completions" {
		t.Fatalf("live runtime apply was not called: %#v", client.appliedRuntimes)
	}
}

func TestManagerSettingsAPIUpdatesAndPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Settings: config.Settings{
				StateDir: filepath.Join(dir, "state"),
				LogDir:   filepath.Join(dir, "logs"),
				Terminal: config.TerminalSettings{
					Tmux: config.TmuxSettings{
						SocketName:    "custom-socket",
						HostSession:   "custom-host",
						HostStartMode: "new-window",
					},
				},
			},
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	defer m.Close()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/settings", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected settings get status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"state_dir"`) || !strings.Contains(res.Body.String(), `"log_dir"`) {
		t.Fatalf("unexpected settings response: %s", res.Body.String())
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(
		res,
		httptest.NewRequest(
			http.MethodPatch,
			"http://manager.local/api/settings",
			strings.NewReader(`{"state_dir":"`+filepath.Join(dir, "next-state")+`","log_dir":"`+filepath.Join(dir, "next-logs")+`","terminal":{"tmux":{"host_start_mode":"reuse-first-window"}}}`),
		),
	)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected settings patch status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"state_dir"`) || !strings.Contains(res.Body.String(), `"log_dir"`) {
		t.Fatalf("unexpected settings patch response: %s", res.Body.String())
	}

	loaded, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.StateDir != filepath.Join(dir, "next-state") || loaded.Settings.LogDir != filepath.Join(dir, "next-logs") {
		t.Fatalf("settings were not persisted: %#v", loaded.Settings)
	}
	if loaded.Settings.Terminal.Tmux.SocketName != "custom-socket" || loaded.Settings.Terminal.Tmux.HostSession != "custom-host" {
		t.Fatalf("settings patch should preserve omitted terminal settings: %#v", loaded.Settings.Terminal.Tmux)
	}
	if loaded.Settings.Terminal.Tmux.HostStartMode != "reuse-first-window" {
		t.Fatalf("settings patch should persist host start mode: %#v", loaded.Settings.Terminal.Tmux)
	}
	if loaded.Settings.Terminal.Opener != "default" {
		t.Fatalf("settings patch should preserve omitted terminal opener: %#v", loaded.Settings.Terminal)
	}
}

func TestManagerSettingsAPIReconcilesTurnStatusHooks(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	m := New(Config{
		ConfigPath:         configPath,
		ReconcileTurnHooks: true,
		Config: config.Config{
			Settings: config.Settings{
				StateDir: filepath.Join(dir, "state"),
			},
		},
	})
	defer m.Close()

	res := httptest.NewRecorder()
	m.ServeHTTP(
		res,
		httptest.NewRequest(
			http.MethodPatch,
			"http://manager.local/api/settings",
			strings.NewReader(`{"terminal":{"tmux":{"turn_status_hooks":true}}}`),
		),
	)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected settings patch status %d: %s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(hostedhooks.TurnStatusScriptPath()); err != nil {
		t.Fatal(err)
	}
	codexHooks, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexHooks), "hosted-turn-status") {
		t.Fatalf("codex hooks were not installed:\n%s", codexHooks)
	}
	loaded, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Settings.Terminal.Tmux.TurnStatusHooks {
		t.Fatalf("turn status hook setting was not persisted: %#v", loaded.Settings.Terminal.Tmux)
	}
	wantStatus := hostedhooks.StatusReport{
		ScriptInstalled: true,
		CodexInstalled:  true,
		ClaudeInstalled: true,
	}
	status, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("bad turn status hook status:\n got %#v\nwant %#v", status, wantStatus)
	}
	wantScript, err := os.ReadFile(hostedhooks.TurnStatusScriptPath())
	if err != nil {
		t.Fatal(err)
	}
	wantCodexHooks, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantClaudeHooks, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	res = httptest.NewRecorder()
	m.ServeHTTP(
		res,
		httptest.NewRequest(
			http.MethodPatch,
			"http://manager.local/api/settings",
			strings.NewReader(`{"terminal":{"tmux":{"turn_status_hooks":false}}}`),
		),
	)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected settings patch status %d: %s", res.Code, res.Body.String())
	}
	status, err = hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("bad turn status hook status:\n got %#v\nwant %#v", status, wantStatus)
	}
	gotScript, err := os.ReadFile(hostedhooks.TurnStatusScriptPath())
	if err != nil {
		t.Fatal(err)
	}
	gotCodexHooks, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	gotClaudeHooks, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotScript, wantScript) {
		t.Fatalf("bad preserved turn status script:\n got %q\nwant %q", gotScript, wantScript)
	}
	if !bytes.Equal(gotCodexHooks, wantCodexHooks) {
		t.Fatalf("bad preserved codex hooks:\n got %q\nwant %q", gotCodexHooks, wantCodexHooks)
	}
	if !bytes.Equal(gotClaudeHooks, wantClaudeHooks) {
		t.Fatalf("bad preserved claude hooks:\n got %q\nwant %q", gotClaudeHooks, wantClaudeHooks)
	}
	loaded, err = config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.Terminal.Tmux.TurnStatusHooks {
		t.Fatalf("turn status hook setting should be false: %#v", loaded.Settings.Terminal.Tmux)
	}
}

func TestManagerStartupDoesNotUninstallTurnStatusHooksWhenDisabled(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := hostedhooks.Install(); err != nil {
		t.Fatal(err)
	}
	wantScript, err := os.ReadFile(hostedhooks.TurnStatusScriptPath())
	if err != nil {
		t.Fatal(err)
	}
	wantCodexHooks, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantClaudeHooks, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	m := New(Config{
		ConfigPath:         filepath.Join(dir, "config.yaml"),
		ReconcileTurnHooks: true,
		Config: config.Config{
			Settings: config.Settings{
				StateDir: filepath.Join(dir, "state"),
				Terminal: config.TerminalSettings{
					Tmux: config.TmuxSettings{TurnStatusHooks: false},
				},
			},
		},
	})
	defer m.Close()

	wantStatus := hostedhooks.StatusReport{
		ScriptInstalled: true,
		CodexInstalled:  true,
		ClaudeInstalled: true,
	}
	status, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, wantStatus) {
		t.Fatalf("bad turn status hook status:\n got %#v\nwant %#v", status, wantStatus)
	}
	gotScript, err := os.ReadFile(hostedhooks.TurnStatusScriptPath())
	if err != nil {
		t.Fatal(err)
	}
	gotCodexHooks, err := os.ReadFile(filepath.Join(homeDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	gotClaudeHooks, err := os.ReadFile(filepath.Join(homeDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotScript, wantScript) {
		t.Fatalf("bad preserved turn status script:\n got %q\nwant %q", gotScript, wantScript)
	}
	if !bytes.Equal(gotCodexHooks, wantCodexHooks) {
		t.Fatalf("bad preserved codex hooks:\n got %q\nwant %q", gotCodexHooks, wantCodexHooks)
	}
	if !bytes.Equal(gotClaudeHooks, wantClaudeHooks) {
		t.Fatalf("bad preserved claude hooks:\n got %q\nwant %q", gotClaudeHooks, wantClaudeHooks)
	}
}

func TestManagerUpstreamUpdatePersistsDesiredStateAndMarksFailedApplyOutOfSync(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	client := &recordingWorkerClient{applyErrByPort: map[int]error{11199: errors.New("worker rejected runtime")}}
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
				"cli": {Port: 11199, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	m.statuses["app"] = "running"
	m.statuses["cli"] = "running"

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://relay.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("expected desired update success, got %d: %s", res.Code, res.Body.String())
	}

	cfg := m.store.Config()
	if cfg.Upstreams["openai"].BaseURL != "https://relay.example/v1" {
		t.Fatalf("upstream update was not persisted: %#v", cfg.Upstreams["openai"])
	}
	if client.appliedRuntimes[6767].Upstream.BaseURL != "https://relay.example/v1" {
		t.Fatalf("healthy worker did not receive runtime: %#v", client.appliedRuntimes)
	}
	if client.appliedRuntimes[11199].Upstream.BaseURL != "https://relay.example/v1" {
		t.Fatalf("failing worker was not attempted: %#v", client.appliedRuntimes)
	}
	if got := m.workerStatus("app"); got != WorkerStateRunning {
		t.Fatalf("healthy worker status changed: %s", got)
	}
	if got := m.workerStatus("cli"); got != WorkerStateOutOfSync {
		t.Fatalf("failing worker status = %s, want %s", got, WorkerStateOutOfSync)
	}
}

func TestManagerConfigUpdatesPersistAsynchronously(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	m := New(Config{
		ConfigPath: configPath,
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1", APIKey: "sk-file"},
			},
		},
	})
	defer m.Close()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", strings.NewReader(`{"base_url":"https://async.example/v1","api_key":"sk-file","api_format":"chat_completions"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected provider update status %d: %s", res.Code, res.Body.String())
	}

	eventually(t, time.Second, func() bool {
		loaded, err := config.LoadFile(configPath)
		return err == nil && loaded.Upstreams["openai"].BaseURL == "https://async.example/v1"
	})

	eventually(t, time.Second, func() bool {
		res = httptest.NewRecorder()
		m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/config", nil))
		return strings.Contains(res.Body.String(), `"dirty":false`) && strings.Contains(res.Body.String(), `"generation":1`)
	})
}

func TestManagerHealthMonitorMarksFailedAfterRetryLimit(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		m.RecordHealth("app", false)
	}
	assertWorkerStatus(t, m, "failed")
}

func TestManagerHealthFailureRestartsWorker(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	if len(starter.processes) != 1 {
		t.Fatalf("expected initial process, got %d", len(starter.processes))
	}

	m.RecordHealth("app", false)

	if len(starter.spawns) != 2 {
		t.Fatalf("expected unhealthy worker to be respawned, got %d spawns", len(starter.spawns))
	}
	if starter.processes[0].stops != 1 {
		t.Fatalf("expected old process to be stopped before respawn, got %d stops", starter.processes[0].stops)
	}
	assertWorkerStatus(t, m, "running")
}

func TestManagerHealthFailureUsesHealthLogger(t *testing.T) {
	var logBuf bytes.Buffer
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:      starter,
		Logger:       logging.New(&logBuf, "detail", logging.ComponentManagerSuper),
		HealthLogger: logging.New(&logBuf, "detail", logging.ComponentManagerHealth),
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}

	m.RecordHealth("app", false)

	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, logging.EventHealthFail) && strings.Contains(line, logging.ComponentManagerHealth) {
			return
		}
	}
	t.Fatalf("missing health.fail under manager.health: %s", logBuf.String())
}

func TestManagerHealthFailureStopsRestartingAfterRetryLimit(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		m.RecordHealth("app", false)
	}
	spawnsAfterFailure := len(starter.spawns)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "failed")
	if len(starter.spawns) != spawnsAfterFailure {
		t.Fatalf("expected failed worker not to respawn again, before=%d after=%d", spawnsAfterFailure, len(starter.spawns))
	}
}

func TestManagerManualRestartResetsHealthRetryCounter(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/workers/6767/restart", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected restart status %d: %s", res.Code, res.Body.String())
	}
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "running")
	if len(starter.spawns) < 11 {
		t.Fatalf("expected failed health after manual restart to retry instead of fail, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorDoesNotResetRetryCounterBeforeHealthyWindow(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}
	m.RecordHealth("app", true)
	now = now.Add(59 * time.Second)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "failed")
	if len(starter.spawns) != 10 {
		t.Fatalf("expected brief success not to allow another respawn, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorResetsRetryCounterAfterHealthyWindow(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: starter,
	})
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		m.RecordHealth("app", false)
	}
	m.RecordHealth("app", true)
	now = now.Add(60 * time.Second)
	m.RecordHealth("app", true)
	m.RecordHealth("app", false)

	assertWorkerStatus(t, m, "running")
	if len(starter.spawns) < 11 {
		t.Fatalf("expected retry after healthy window reset, got %d spawns", len(starter.spawns))
	}
}

func TestManagerHealthMonitorLoopRecordsCheckerResults(t *testing.T) {
	checker := &sequenceHealthChecker{results: []bool{false, false, true}}
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:       fakeStarter{},
		HealthChecker: checker,
	})
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	stop := m.StartHealthMonitor(5 * time.Millisecond)
	defer stop()
	time.Sleep(30 * time.Millisecond)
	assertWorkerStatus(t, m, "running")
	if checker.Calls() == 0 {
		t.Fatal("health checker was not called")
	}
}

func TestManagerWorkerLogsAreRedactedAndExposed(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	if _, err := m.LogSink("app").Write([]byte("Authorization: Bearer sk-secret\n")); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767/logs", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected logs status %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-secret") || !strings.Contains(res.Body.String(), "***REDACTED***") {
		t.Fatalf("logs response was not redacted: %s", res.Body.String())
	}
}

func TestManagerLogSinkHonorsWorkerLogLevel(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Plugins: testPluginDefinitions(),
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", LogLevel: "simple"},
				"cli": {Port: 11199, Upstream: "openai", LogLevel: "detail"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	if _, err := m.LogSink("app").Write([]byte("INFO request started\nWARN upstream retrying\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LogSink("cli").Write([]byte("INFO request started\nWARN upstream retrying\n")); err != nil {
		t.Fatal(err)
	}

	appLines := m.LogSink("app").Lines()
	if len(appLines) != 1 || !strings.Contains(appLines[0], "WARN upstream retrying") {
		t.Fatalf("simple worker should keep warn only, got %#v", appLines)
	}
	if strings.Contains(strings.Join(appLines, "\n"), "INFO request started") {
		t.Fatalf("simple worker kept info line: %#v", appLines)
	}

	cliLines := m.LogSink("cli").Lines()
	if len(cliLines) != 2 {
		t.Fatalf("detail worker should keep both lines, got %#v", cliLines)
	}
	if !strings.Contains(strings.Join(cliLines, "\n"), "INFO request started") {
		t.Fatalf("detail worker dropped info line: %#v", cliLines)
	}
}

type sequenceHealthChecker struct {
	mu      sync.Mutex
	results []bool
	calls   int
}

func (c *sequenceHealthChecker) Check(port int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := true
	if c.calls < len(c.results) {
		result = c.results[c.calls]
	}
	c.calls++
	return result
}

func (c *sequenceHealthChecker) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type recordingHealthChecker struct {
	results map[int]bool
	calls   map[int]int
}

func (c *recordingHealthChecker) Check(port int) bool {
	if c.calls == nil {
		c.calls = map[int]int{}
	}
	c.calls[port]++
	return c.results[port]
}

type fakeStarter struct{}

func (fakeStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	return fakeProcess{}, nil
}

type fakeProcess struct{}

func (fakeProcess) Stop() error { return nil }

type fixedStarter struct {
	process ManagedProcess
}

func (s fixedStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	return s.process, nil
}

type recordingManagedProcess struct {
	stopCount int
}

func (p *recordingManagedProcess) Stop() error {
	p.stopCount++
	return nil
}

type recordingWorkerClient struct {
	toggledPort      int
	toggledModule    string
	patchedPort      int
	patchedModule    string
	patchedConfig    config.ModuleConfig
	switchedPort     int
	switchedProvider upstream.RuntimeUpstream
	switchErr        error
	appliedPort      int
	appliedRuntime   appruntime.WorkerRuntime
	appliedRuntimes  map[int]appruntime.WorkerRuntime
	applyErr         error
	applyErrByPort   map[int]error
	statusBody       string
}

func (c *recordingWorkerClient) ToggleModule(port int, moduleName string) error {
	c.toggledPort = port
	c.toggledModule = moduleName
	return nil
}

func (c *recordingWorkerClient) PatchModule(port int, moduleName string, cfg config.ModuleConfig) error {
	c.patchedPort = port
	c.patchedModule = moduleName
	c.patchedConfig = cfg
	return nil
}

func (c *recordingWorkerClient) SwitchUpstream(port int, runtime upstream.RuntimeUpstream) error {
	c.switchedPort = port
	c.switchedProvider = runtime
	return c.switchErr
}

func (c *recordingWorkerClient) ApplyRuntime(port int, runtime appruntime.WorkerRuntime) (ApplyRuntimeStatus, error) {
	c.appliedPort = port
	c.appliedRuntime = runtime
	if c.appliedRuntimes == nil {
		c.appliedRuntimes = map[int]appruntime.WorkerRuntime{}
	}
	c.appliedRuntimes[port] = runtime
	if c.applyErrByPort != nil {
		if err := c.applyErrByPort[port]; err != nil {
			return ApplyRuntimeStatus{}, err
		}
	}
	if c.applyErr != nil {
		return ApplyRuntimeStatus{}, c.applyErr
	}
	return ApplyRuntimeStatus{AppliedGeneration: runtime.Generation}, nil
}

func (c *recordingWorkerClient) GetStatus(port int) (WorkerStatus, error) {
	if c.statusBody == "" {
		return WorkerStatus{}, nil
	}
	var status WorkerStatus
	if err := json.Unmarshal([]byte(c.statusBody), &status); err != nil {
		return WorkerStatus{}, err
	}
	return status, nil
}

func assertWorkerStatus(t *testing.T, m *Manager, want string) {
	t.Helper()
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers", nil))
	if !strings.Contains(res.Body.String(), `"status":"`+want+`"`) {
		t.Fatalf("expected worker status %q, got: %s", want, res.Body.String())
	}
}

func eventually(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
