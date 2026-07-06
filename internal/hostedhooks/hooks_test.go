package hostedhooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/hostedhooks"
)

type testHookRoot struct {
	Theme string                       `json:"theme,omitempty"`
	Hooks map[string][]testHookMatcher `json:"hooks,omitempty"`
}

type testHookMatcher struct {
	Matcher string            `json:"matcher"`
	Hooks   []testCommandHook `json:"hooks"`
}

type testCommandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func TestReconcileInstallsTurnStatusHooks(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	claudePath := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudePath), 0700); err != nil {
		t.Fatal(err)
	}
	claudeBefore := testHookRoot{
		Theme: "dark",
		Hooks: map[string][]testHookMatcher{
			"Stop": {{
				Matcher: "",
				Hooks:   []testCommandHook{{Type: "command", Command: "echo user-stop"}},
			}},
		},
	}
	data, err := json.Marshal(claudeBefore)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, data, 0600); err != nil {
		t.Fatal(err)
	}

	err = hostedhooks.Reconcile(config.Settings{
		Terminal: config.TerminalSettings{
			Tmux: config.TmuxSettings{TurnStatusHooks: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	scriptPath := hostedhooks.TurnStatusScriptPath()
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "AINN_HOSTED_SESSION_ID") || !strings.Contains(string(script), "hosted-session mark") || !strings.Contains(string(script), "\"$@\"") {
		t.Fatalf("unexpected shim script:\n%s", script)
	}
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0100 == 0 {
		t.Fatalf("shim script is not executable: %v", info.Mode())
	}

	codex := readTestHookRoot(t, filepath.Join(homeDir, ".codex", "hooks.json"))
	claude := readTestHookRoot(t, claudePath)
	runningCommand := testShellQuote(scriptPath) + " running --capture-launcher-session-id"
	doneCommand := testShellQuote(scriptPath) + " done --capture-launcher-session-id"
	failedCommand := testShellQuote(scriptPath) + " failed stop_failure --capture-launcher-session-id"
	wantCodexHooks := map[string][]testHookMatcher{
		"UserPromptSubmit": {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: runningCommand}}}},
		"Stop":             {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: doneCommand}}}},
	}
	wantClaude := testHookRoot{
		Theme: "dark",
		Hooks: map[string][]testHookMatcher{
			"UserPromptSubmit": {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: runningCommand}}}},
			"Stop": {
				{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: "echo user-stop"}}},
				{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: doneCommand}}},
			},
			"StopFailure": {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: failedCommand}}}},
		},
	}
	if !reflect.DeepEqual(codex.Hooks, wantCodexHooks) {
		t.Fatalf("bad codex hooks:\n got %#v\nwant %#v", codex.Hooks, wantCodexHooks)
	}
	if !reflect.DeepEqual(claude, wantClaude) {
		t.Fatalf("bad claude settings:\n got %#v\nwant %#v", claude, wantClaude)
	}
}

func TestReconcileDisablesOnlyManagedTurnStatusHooks(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	scriptPath := hostedhooks.TurnStatusScriptPath()
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	doneCommand := testShellQuote(scriptPath) + " done"
	userCommand := "echo user-stop"
	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	claudePath := filepath.Join(homeDir, ".claude", "settings.json")
	for _, path := range []string{codexPath, claudePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		root := testHookRoot{
			Theme: "dark",
			Hooks: map[string][]testHookMatcher{
				"Stop": {{
					Matcher: "",
					Hooks: []testCommandHook{
						{Type: "command", Command: userCommand},
						{Type: "command", Command: doneCommand},
					},
				}},
			},
		}
		data, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := hostedhooks.Reconcile(config.Settings{}); err != nil {
		t.Fatal(err)
	}

	want := testHookRoot{
		Theme: "dark",
		Hooks: map[string][]testHookMatcher{
			"Stop": {{
				Matcher: "",
				Hooks:   []testCommandHook{{Type: "command", Command: userCommand}},
			}},
		},
	}
	for _, path := range []string{codexPath, claudePath} {
		got := readTestHookRoot(t, path)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("bad hooks in %s:\n got %#v\nwant %#v", path, got, want)
		}
	}
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Fatalf("expected shim script to be removed, got %v", err)
	}
}

func readTestHookRoot(t *testing.T, path string) testHookRoot {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root testHookRoot
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func testShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
