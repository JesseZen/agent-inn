package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func TestMapHostedSessionSnapshotDerivesPublicState(t *testing.T) {
	createdAt := time.Date(2026, 7, 13, 1, 2, 3, 0, time.UTC)
	lastOpenedAt := createdAt.Add(time.Minute)
	record := HostedSessionRecord{
		SessionID:                  "hs_1",
		SessionLabel:               "solve problem A",
		WorkerID:                   "cli",
		WorkerName:                 "legacy-name",
		WorkerPort:                 11199,
		Workspace:                  "/tmp/work",
		Model:                      "gpt-5.5",
		TurnState:                  HostedTurnStateRunning,
		TurnStateReason:            "",
		TurnGeneration:             3,
		TurnAcknowledgedGeneration: 2,
		TurnTranscriptPath:         "/private/codex.jsonl",
		TurnTranscriptOffset:       321,
		TurnID:                     "turn-private",
		TurnWatchKind:              HostedTurnWatchKindCodex,
		TurnInputRequestID:         "call-private",
		LauncherSessionID:          "launcher-private",
		TmuxWindowID:               "@12",
		UserMarker:                 HostedUserMarkerTodo,
		CreatedAt:                  createdAt,
		LastOpenedAt:               lastOpenedAt,
	}
	worker := HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199}
	want := HostedSessionSnapshot{
		SessionID:    "hs_1",
		SessionLabel: "solve problem A",
		Worker:       worker,
		Workspace:    "/tmp/work",
		Model:        "gpt-5.5",
		AddDirs:      []string{},
		Status:       HostedSessionStatusActive,
		UserMarker:   HostedUserMarkerTodo,
		Turn: HostedSessionTurnSnapshot{
			State:      HostedTurnStateRunning,
			Reason:     "",
			Unread:     false,
			NeedsInput: true,
		},
		CreatedAt:    createdAt,
		LastOpenedAt: lastOpenedAt,
	}
	got := MapHostedSessionSnapshot(record, HostedSessionStatusActive, worker)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"call-private", "turn-private", "launcher-private", "codex.jsonl", "tmux_window_id", "turn_generation"} {
		if strings.Contains(string(encoded), prohibited) {
			t.Fatalf("public snapshot exposed %q: %s", prohibited, encoded)
		}
	}
}

func TestManagerAPIHostedSessionCommandsReturnWholeSnapshots(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers: map[string]config.WorkerConfig{
			"cli": {Name: "CLI", Port: 11199, Launcher: "codex"},
		},
	}})
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil })
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	create := httptest.NewRecorder()
	m.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions", strings.NewReader(`{"worker_id":"cli","session_label":"solve problem A","workspace":"/tmp/work","model":"gpt-5.5"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", create.Code, create.Body.String())
	}
	var created HostedSessionSnapshot
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	wantCreated := HostedSessionSnapshot{
		SessionID:    created.SessionID,
		SessionLabel: "solve problem A",
		Worker:       HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199},
		Workspace:    "/tmp/work",
		Model:        "gpt-5.5",
		AddDirs:      []string{},
		Status:       HostedSessionStatusStale,
		Turn:         HostedSessionTurnSnapshot{State: HostedTurnStateIdle},
		CreatedAt:    created.CreatedAt,
		LastOpenedAt: created.LastOpenedAt,
	}
	if !reflect.DeepEqual(created, wantCreated) {
		t.Fatalf("created %#v, want %#v", created, wantCreated)
	}

	patch := httptest.NewRecorder()
	m.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, strings.NewReader(`{"user_marker":"todo"}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status %d: %s", patch.Code, patch.Body.String())
	}
	var patched HostedSessionSnapshot
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	wantPatched := wantCreated
	wantPatched.UserMarker = HostedUserMarkerTodo
	if !reflect.DeepEqual(patched, wantPatched) {
		t.Fatalf("patched %#v, want %#v", patched, wantPatched)
	}
	idempotent := httptest.NewRecorder()
	m.ServeHTTP(idempotent, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/hosted-sessions/"+created.SessionID, strings.NewReader(`{"user_marker":"todo"}`)))
	if idempotent.Code != http.StatusOK {
		t.Fatalf("idempotent patch status %d: %s", idempotent.Code, idempotent.Body.String())
	}
	var idempotentSnapshot HostedSessionSnapshot
	if err := json.Unmarshal(idempotent.Body.Bytes(), &idempotentSnapshot); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idempotentSnapshot, wantPatched) {
		t.Fatalf("idempotent patch %#v, want %#v", idempotentSnapshot, wantPatched)
	}

	get := httptest.NewRecorder()
	m.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "http://manager.local/api/hosted-sessions/"+created.SessionID, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", get.Code, get.Body.String())
	}
	var got HostedSessionSnapshot
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantPatched) {
		t.Fatalf("get %#v, want %#v", got, wantPatched)
	}

	list := httptest.NewRecorder()
	m.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "http://manager.local/api/hosted-sessions", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body.String())
	}
	var gotList HostedSessionListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &gotList); err != nil {
		t.Fatal(err)
	}
	wantList := HostedSessionListResponse{Sessions: []HostedSessionSnapshot{wantPatched}, EventCursor: "0"}
	if !reflect.DeepEqual(gotList, wantList) {
		t.Fatalf("list %#v, want %#v", gotList, wantList)
	}

	for _, request := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "unknown field", body: `{"worker_id":"cli","worker_name":"legacy"}`, status: http.StatusBadRequest},
		{name: "missing worker id", body: `{}`, status: http.StatusBadRequest},
		{name: "empty worker id", body: `{"worker_id":""}`, status: http.StatusBadRequest},
		{name: "empty supplied label", body: `{"worker_id":"cli","session_label":""}`, status: http.StatusBadRequest},
		{name: "null supplied label", body: `{"worker_id":"cli","session_label":null}`, status: http.StatusBadRequest},
		{name: "null workspace", body: `{"worker_id":"cli","workspace":null}`, status: http.StatusBadRequest},
		{name: "null model", body: `{"worker_id":"cli","model":null}`, status: http.StatusBadRequest},
		{name: "null add dirs", body: `{"worker_id":"cli","add_dirs":null}`, status: http.StatusBadRequest},
		{name: "missing worker", body: `{"worker_id":"missing"}`, status: http.StatusNotFound},
	} {
		t.Run(request.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			m.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://manager.local/api/hosted-sessions", strings.NewReader(request.body)))
			if res.Code != request.status {
				t.Fatalf("status %d body %s, want %d", res.Code, res.Body.String(), request.status)
			}
		})
	}
}

func TestManagerAPIHostedSessionListPreservesLargeEventCursor(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: stateDir}}})
	m.events.nextID = 9007199254740992
	m.events.Publish(EventWorkerStarted, map[string]any{"worker": "app"})
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil })
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://manager.local/api/hosted-sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionListResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := HostedSessionListResponse{Sessions: []HostedSessionSnapshot{}, EventCursor: "9007199254740993"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestManagerAPIHostedSessionPatchValidatesOneCommandWithoutSideEffects(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers:  map[string]config.WorkerConfig{"cli": {Name: "CLI", Port: 11199, Launcher: "codex"}},
	}})
	created, err := m.hostedSessions.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "cli",
		WorkerName:   "cli",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "missing field", body: `{}`, status: http.StatusBadRequest},
		{name: "multiple fields", body: `{"session_label":"work","user_marker":"todo"}`, status: http.StatusBadRequest},
		{name: "unknown field", body: `{"turn_state":"done"}`, status: http.StatusBadRequest},
		{name: "empty session label", body: `{"session_label":""}`, status: http.StatusBadRequest},
		{name: "empty worker id", body: `{"worker_id":""}`, status: http.StatusBadRequest},
		{name: "missing worker", body: `{"worker_id":"missing"}`, status: http.StatusNotFound},
		{name: "invalid marker", body: `{"user_marker":"done"}`, status: http.StatusBadRequest},
		{name: "trailing object", body: `{"user_marker":"todo"}{}`, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			path := "http://manager.local/api/hosted-sessions/" + created.SessionID
			m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(test.body)))
			if res.Code != test.status {
				t.Fatalf("status %d body %s, want %d", res.Code, res.Body.String(), test.status)
			}
			persisted, found, err := m.hostedSessions.Get(created.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if !found || !reflect.DeepEqual(persisted, created) {
				t.Fatalf("persisted %#v found=%v, want %#v", persisted, found, created)
			}
		})
	}
}

func TestManagerAPIHostedSessionIdempotentRenameReturnsCurrentActiveSnapshot(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{Config: config.Config{Settings: settings}})
	created, err := m.hostedSessions.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "cli",
		WorkerName:   "cli",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func(args []string) (string, error) {
			calls = append(calls, append([]string{}, args...))
			switch {
			case reflect.DeepEqual(args, TmuxHasSessionCommandForSettings(settings)):
				return "", nil
			case reflect.DeepEqual(args, TmuxListWindowDetailsCommandForSettings(settings)):
				return "@12\tsolve problem A\n", nil
			default:
				t.Fatalf("unexpected tmux call: %#v", args)
				return "", nil
			}
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	res := httptest.NewRecorder()
	path := "http://manager.local/api/hosted-sessions/" + created.SessionID
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"session_label":"solve problem A"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var got HostedSessionSnapshot
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := MapHostedSessionSnapshot(created, HostedSessionStatusActive, HostedSessionWorkerSnapshot{ID: "cli", Name: "cli", Port: 11199, Missing: true})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	wantCalls := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls %#v, want %#v", calls, wantCalls)
	}
}

func TestHostedSessionPublicTypesMatchCanonicalSchema(t *testing.T) {
	var data []byte
	var err error
	for _, path := range []string{
		filepath.Join("..", "..", "docs", "superpowers", "specs", "hosted-session-snapshot.schema.json"),
		filepath.Join("..", "..", "..", "..", "docs", "superpowers", "specs", "hosted-session-snapshot.schema.json"),
	} {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("read canonical hosted-session Schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	defs := schema["$defs"].(map[string]any)

	assertSchemaObject := func(name string, value any, wantRequired []string) {
		t.Helper()
		definition := defs[name].(map[string]any)
		properties := definition["properties"].(map[string]any)
		requiredValues := definition["required"].([]any)
		required := make([]string, 0, len(requiredValues))
		for _, value := range requiredValues {
			required = append(required, value.(string))
		}
		sort.Strings(required)
		propertyNames := make([]string, 0, len(properties))
		for property := range properties {
			propertyNames = append(propertyNames, property)
		}
		sort.Strings(propertyNames)
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(encoded, &object); err != nil {
			t.Fatal(err)
		}
		actual := make([]string, 0, len(object))
		for property := range object {
			actual = append(actual, property)
		}
		sort.Strings(actual)
		if wantRequired == nil {
			wantRequired = propertyNames
		}
		sort.Strings(wantRequired)
		if !reflect.DeepEqual(actual, propertyNames) || !reflect.DeepEqual(required, wantRequired) {
			t.Fatalf("%s keys=%v required=%v wantRequired=%v properties=%v", name, actual, required, wantRequired, propertyNames)
		}
	}

	now := time.Date(2026, 7, 13, 1, 2, 3, 0, time.UTC)
	assertSchemaObject("workerSnapshot", HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199}, nil)
	assertSchemaObject("turnSnapshot", HostedSessionTurnSnapshot{State: HostedTurnStateRunning}, nil)
	assertSchemaObject("hostedSessionSnapshot", HostedSessionSnapshot{
		SessionID: "hs_1", SessionLabel: "work", Worker: HostedSessionWorkerSnapshot{}, AddDirs: []string{}, Status: HostedSessionStatusActive,
		Turn: HostedSessionTurnSnapshot{State: HostedTurnStateRunning}, CreatedAt: now, LastOpenedAt: now,
	}, nil)
	assertSchemaObject("listResponse", HostedSessionListResponse{Sessions: []HostedSessionSnapshot{}, EventCursor: "0"}, nil)
	assertSchemaObject("createRequest", hostedSessionCreateRequest{WorkerID: "cli", AddDirs: []string{}}, []string{"worker_id"})

	patchDefinition := defs["patchRequest"].(map[string]any)
	variants := patchDefinition["oneOf"].([]any)
	wantPatchFields := []string{"session_label", "user_marker", "worker_id"}
	gotPatchFields := make([]string, 0, len(variants))
	for _, variant := range variants {
		required := variant.(map[string]any)["required"].([]any)
		gotPatchFields = append(gotPatchFields, required[0].(string))
	}
	sort.Strings(gotPatchFields)
	if !reflect.DeepEqual(gotPatchFields, wantPatchFields) {
		t.Fatalf("patch fields %v, want %v", gotPatchFields, wantPatchFields)
	}

	statusProperty := defs["hostedSessionSnapshot"].(map[string]any)["properties"].(map[string]any)["status"].(map[string]any)
	if got := statusProperty["enum"].([]any); !reflect.DeepEqual(got, []any{"active", "stale"}) {
		t.Fatalf("status enum %v", got)
	}
	markerProperty := defs["hostedSessionSnapshot"].(map[string]any)["properties"].(map[string]any)["user_marker"].(map[string]any)
	if got := markerProperty["enum"].([]any); !reflect.DeepEqual(got, []any{"", "todo"}) {
		t.Fatalf("marker enum %v", got)
	}
	turnStates := defs["turnSnapshot"].(map[string]any)["properties"].(map[string]any)["state"].(map[string]any)["enum"].([]any)
	if !reflect.DeepEqual(turnStates, []any{"idle", "running", "done", "failed", "interrupted"}) {
		t.Fatalf("turn state enum %v", turnStates)
	}
}

func TestHostedSessionSnapshotReconciliationPublishesExternalChanges(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{
		Settings: config.Settings{StateDir: stateDir},
		Workers:  map[string]config.WorkerConfig{"cli": {Name: "CLI", Port: 11199}},
	}})
	tmuxCalls := 0
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func([]string) (string, error) {
			tmuxCalls++
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	baselineTmuxCalls := tmuxCalls
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	if tmuxCalls != baselineTmuxCalls {
		t.Fatalf("unchanged registry triggered tmux lookup: before=%d after=%d", baselineTmuxCalls, tmuxCalls)
	}

	created, err := m.hostedSessions.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "cli",
		WorkerName:   "cli",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	baselineTmuxCalls = tmuxCalls
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	if tmuxCalls != baselineTmuxCalls {
		t.Fatalf("registry read triggered tmux lookup: before=%d after=%d", baselineTmuxCalls, tmuxCalls)
	}
	marked, err := m.hostedSessions.SetUserMarker(created.SessionID, HostedUserMarkerTodo)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	if err := m.hostedSessions.Delete(created.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}

	events := m.events.Replay(0)
	wantCreated := MapHostedSessionSnapshot(created, HostedSessionStatusStale, HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199})
	wantMarked := MapHostedSessionSnapshot(marked, HostedSessionStatusStale, HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199})
	wantEvents := []Event{
		{ID: events[0].ID, Type: EventHostedSessionSnapshotChanged, At: events[0].At, Payload: map[string]any{"snapshot": wantCreated}},
		{ID: events[1].ID, Type: EventHostedSessionSnapshotChanged, At: events[1].At, Payload: map[string]any{"snapshot": wantMarked}},
		{ID: events[2].ID, Type: EventHostedSessionDeleted, At: events[2].At, Payload: map[string]any{"session_id": created.SessionID}},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events %#v, want %#v", events, wantEvents)
	}
}

func TestHostedSessionSnapshotReconciliationRetriesTmuxProjection(t *testing.T) {
	stateDir := t.TempDir()
	settings := config.Settings{StateDir: stateDir}
	m := New(Config{Config: config.Config{
		Settings: settings,
		Workers:  map[string]config.WorkerConfig{"cli": {Name: "CLI", Port: 11199}},
	}})
	created, err := m.hostedSessions.Create(HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerID:     "cli",
		WorkerName:   "cli",
		WorkerPort:   11199,
		TmuxWindowID: "@12",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectionErr := errors.New("tmux projection failed")
	projectionCalls := 0
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
			projectionCalls++
			if projectionCalls == 2 {
				return "", projectionErr
			}
			return "", nil
		})
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	marked, err := m.hostedSessions.SetUserMarker(created.SessionID, HostedUserMarkerTodo)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileHostedSessionSnapshots(); !errors.Is(err, projectionErr) {
		t.Fatalf("got %v, want %v", err, projectionErr)
	}
	if err := m.reconcileHostedSessionSnapshots(); err != nil {
		t.Fatal(err)
	}
	wantCreated := MapHostedSessionSnapshot(created, HostedSessionStatusActive, HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199})
	wantMarked := MapHostedSessionSnapshot(marked, HostedSessionStatusActive, HostedSessionWorkerSnapshot{ID: "cli", Name: "CLI", Port: 11199})
	wantCalls := [][]string{
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForSnapshot(settings, "@12", wantCreated),
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForSnapshot(settings, "@12", wantMarked),
		TmuxHasSessionCommandForSettings(settings),
		TmuxListWindowDetailsCommandForSettings(settings),
		TmuxHostedTurnStatusCommandForSnapshot(settings, "@12", wantMarked),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("got calls %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestManagerAPIHostedSessionRegistryFailuresReturnInternalServerError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		path    func(HostedSessionRecord) string
		body    string
		prepare func(*testing.T, *Manager) HostedSessionRecord
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   func(HostedSessionRecord) string { return "/api/hosted-sessions" },
			body:   `{"worker_id":"cli","session_label":"new"}`,
			prepare: func(t *testing.T, m *Manager) HostedSessionRecord {
				return HostedSessionRecord{}
			},
		},
		{
			name:   "duplicate",
			method: http.MethodPost,
			path: func(record HostedSessionRecord) string {
				return "/api/hosted-sessions/" + record.SessionID + "/duplicate"
			},
			prepare: func(t *testing.T, m *Manager) HostedSessionRecord {
				record, err := m.hostedSessions.Create(HostedSessionRecord{SessionLabel: "one", WorkerID: "cli", WorkerName: "cli", WorkerPort: 11199})
				if err != nil {
					t.Fatal(err)
				}
				return record
			},
		},
		{
			name:   "mark unread",
			method: http.MethodPost,
			path: func(record HostedSessionRecord) string {
				return "/api/hosted-sessions/" + record.SessionID + "/mark-unread"
			},
			prepare: func(t *testing.T, m *Manager) HostedSessionRecord {
				record, err := m.hostedSessions.Create(HostedSessionRecord{SessionLabel: "one", WorkerID: "cli", WorkerName: "cli", WorkerPort: 11199, TurnState: HostedTurnStateDone, TurnGeneration: 1})
				if err != nil {
					t.Fatal(err)
				}
				return record
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Config{Config: config.Config{
				Settings: config.Settings{StateDir: t.TempDir()},
				Workers:  map[string]config.WorkerConfig{"cli": {Name: "CLI", Port: 11199}},
			}})
			record := tc.prepare(t, m)
			m.hostedSessions.lock = t.TempDir()
			request := httptest.NewRequest(tc.method, "http://manager.local"+tc.path(record), strings.NewReader(tc.body))
			request.Header.Set("content-type", "application/json")
			response := httptest.NewRecorder()
			m.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("got status %d body=%s, want 500", response.Code, response.Body.String())
			}
		})
	}
}

func TestHostedSessionSnapshotReconciliationRunsWhenTranscriptPollingFails(t *testing.T) {
	stateDir := t.TempDir()
	m := New(Config{Config: config.Config{Settings: config.Settings{StateDir: stateDir}}})
	created, err := m.hostedSessions.Create(HostedSessionRecord{
		SessionLabel:       "solve problem A",
		WorkerID:           "cli",
		WorkerName:         "cli",
		WorkerPort:         11199,
		TurnState:          HostedTurnStateRunning,
		TurnGeneration:     1,
		TurnTranscriptPath: filepath.Join(stateDir, "malformed.jsonl"),
		TurnID:             "turn_1",
		TurnWatchKind:      HostedTurnWatchKindCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created.TurnTranscriptPath, []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldRunner := hostedTMuxRunnerFactory
	hostedTMuxRunnerFactory = func() hostedTMuxRunner {
		return hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil })
	}
	defer func() { hostedTMuxRunnerFactory = oldRunner }()

	stop := m.StartHostedTurnWatcher(time.Millisecond)
	defer stop()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events := m.events.Replay(0)
		if len(events) > 0 {
			if events[0].Type != EventHostedSessionSnapshotChanged {
				t.Fatalf("event %#v, want snapshot change", events[0])
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("snapshot reconciliation did not publish while transcript polling failed")
}
