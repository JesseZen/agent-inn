package manager

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/logging"
)

func hostedTurnPollFailureCategory(err error) string {
	var failure hostedTurnPollFailure
	if errors.As(err, &failure) {
		return failure.Category
	}
	return hostedTurnPollCategory
}

func TestLogHostedTurnPollErrorsRedactsErrorTextAndCallID(t *testing.T) {
	var output bytes.Buffer
	logger := logging.New(&output, "detail", logging.ComponentManagerSuper)
	failure := hostedTurnPollFailure{
		Category:  "transcript_parse",
		Path:      "/tmp/codex.jsonl",
		Position:  42,
		SessionID: "hs_1",
		Err:       errors.New(`call-secret question text answer text`),
	}

	logHostedTurnPollErrors(logger, failure)
	line := output.String()
	for _, prohibited := range []string{"call-secret", "question text", "answer text", "error="} {
		if strings.Contains(line, prohibited) {
			t.Fatalf("log contains prohibited value %q: %s", prohibited, line)
		}
	}
	for _, required := range []string{"hosted_turn.poll", "category=transcript_parse", "path=/tmp/codex.jsonl", "position=42", "session_id=hs_1"} {
		if !strings.Contains(line, required) {
			t.Fatalf("log missing %q: %s", required, line)
		}
	}
}

func TestHostedTurnPollMalformedTranscriptLogsLocationWithoutContent(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(HostedSessionRecord{SessionLabel: "one", WorkerName: "worker", WorkerPort: 11199})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex); err != nil {
		t.Fatal(err)
	}
	secret := `{"type":"response_item","payload":{"call_id":"call-secret","question":"private question"},broken}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	pollErr := watcher.pollOnce()
	if hostedTurnPollFailureCategory(pollErr) != hostedTurnTranscriptParseCategory {
		t.Fatalf("got %v", pollErr)
	}
	var output bytes.Buffer
	logHostedTurnPollErrors(logging.New(&output, "detail", logging.ComponentManagerSuper), pollErr)
	line := output.String()
	if strings.Contains(line, "call-secret") || strings.Contains(line, "private question") {
		t.Fatalf("transcript content leaked: %s", line)
	}
	if !strings.Contains(line, "path="+transcriptPath) || !strings.Contains(line, "position=") {
		t.Fatalf("location missing: %s", line)
	}
}

func TestHostedTurnPollRegistryAndProjectionFailuresKeepSecretsOutOfLogs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		prepare  func(*testing.T, *HostedSessionRegistry, *hostedTurnWatcher)
	}{
		{
			name: "registry write", category: hostedTurnRegistryWriteCategory,
			prepare: func(t *testing.T, registry *HostedSessionRegistry, watcher *hostedTurnWatcher) {
				if _, err := watcher.watchPlans(); err != nil {
					t.Fatal(err)
				}
				registry.lock = t.TempDir()
			},
		},
		{
			name: "tmux projection", category: hostedTurnProjectionCategory,
			prepare: func(t *testing.T, registry *HostedSessionRegistry, watcher *hostedTurnWatcher) {},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			transcriptPath := filepath.Join(stateDir, "codex.jsonl")
			registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
			created, err := registry.Create(HostedSessionRecord{SessionLabel: "one", WorkerName: "worker", WorkerPort: 11199, TmuxWindowID: "@12"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex); err != nil {
				t.Fatal(err)
			}
			line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-secret"}}` + "\n"
			if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
				t.Fatal(err)
			}
			watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) {
				return "", errors.New("tmux output contains call-secret and private answer")
			}))
			tc.prepare(t, registry, watcher)
			pollErr := watcher.pollOnce()
			if hostedTurnPollFailureCategory(pollErr) != tc.category {
				t.Fatalf("got category %q err=%v", hostedTurnPollFailureCategory(pollErr), pollErr)
			}
			var output bytes.Buffer
			logHostedTurnPollErrors(logging.New(&output, "detail", logging.ComponentManagerSuper), pollErr)
			if strings.Contains(output.String(), "call-secret") || strings.Contains(output.String(), "private answer") {
				t.Fatalf("secret leaked: %s", output.String())
			}
		})
	}
}

func TestHostedTurnPollAmbiguousAttributionDoesNotLogOrMutate(t *testing.T) {
	stateDir := t.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	var running []HostedSessionRecord
	for _, label := range []string{"one", "two"} {
		created, err := registry.Create(HostedSessionRecord{SessionLabel: label, WorkerName: "worker", WorkerPort: 11199})
		if err != nil {
			t.Fatal(err)
		}
		session, err := registry.MarkTurnStateWithWatch(created.SessionID, HostedTurnStateRunning, "", "", transcriptPath, "turn_1", HostedTurnWatchKindCodex)
		if err != nil {
			t.Fatal(err)
		}
		running = append(running, session)
	}
	line := `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","call_id":"call-secret"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func([]string) (string, error) { return "", nil }))
	if err := watcher.pollOnce(); err != nil {
		t.Fatal(err)
	}
	got, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].SessionID < got[j].SessionID })
	sort.Slice(running, func(i, j int) bool { return running[i].SessionID < running[j].SessionID })
	if !reflect.DeepEqual(got, running) {
		t.Fatalf("got %#v want %#v", got, running)
	}
}

func TestLogHostedTurnPollErrorsKeepsJoinedFailuresStructured(t *testing.T) {
	var output bytes.Buffer
	logger := logging.New(&output, "detail", logging.ComponentManagerSuper)
	joined := errors.Join(
		hostedTurnPollFailure{Category: "registry_write", SessionID: "hs_1", Position: 10, Err: errors.New("private call id")},
		hostedTurnPollFailure{Category: "tmux_projection", SessionID: "hs_2", Position: 20, Err: errors.New("private output")},
	)

	logHostedTurnPollErrors(logger, joined)
	line := output.String()
	if strings.Contains(line, "private") {
		t.Fatalf("joined error text leaked: %s", line)
	}
	if strings.Count(line, "hosted_turn.poll") != 2 || !strings.Contains(line, "session_id=hs_1") || !strings.Contains(line, "session_id=hs_2") {
		t.Fatalf("joined failures were not logged separately: %s", line)
	}
}
