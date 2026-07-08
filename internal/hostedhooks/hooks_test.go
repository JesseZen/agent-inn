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
	startCommand := testShellQuote(scriptPath) + " idle --capture-launcher-session-id"
	codexRunningCommand := testShellQuote(scriptPath) + " running --capture-launcher-session-id --watch-codex-turn"
	claudeRunningCommand := testShellQuote(scriptPath) + " running --capture-launcher-session-id"
	doneCommand := testShellQuote(scriptPath) + " done --capture-launcher-session-id"
	failedCommand := testShellQuote(scriptPath) + " failed stop_failure --capture-launcher-session-id"
	wantCodexHooks := map[string][]testHookMatcher{
		"SessionStart":     {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: startCommand}}}},
		"UserPromptSubmit": {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: codexRunningCommand}}}},
		"Stop":             {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: doneCommand}}}},
	}
	wantClaude := testHookRoot{
		Theme: "dark",
		Hooks: map[string][]testHookMatcher{
			"SessionStart":     {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: startCommand}}}},
			"UserPromptSubmit": {{Matcher: "", Hooks: []testCommandHook{{Type: "command", Command: claudeRunningCommand}}}},
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

func TestReconcileDisabledPreservesManagedTurnStatusHooks(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := hostedhooks.Install(); err != nil {
		t.Fatal(err)
	}
	scriptPath := hostedhooks.TurnStatusScriptPath()
	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	claudePath := filepath.Join(homeDir, ".claude", "settings.json")

	wantScript, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := readTestHookRoot(t, codexPath)
	wantClaude := readTestHookRoot(t, claudePath)

	if err := hostedhooks.Reconcile(config.Settings{}); err != nil {
		t.Fatal(err)
	}

	gotScript, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	gotCodex := readTestHookRoot(t, codexPath)
	gotClaude := readTestHookRoot(t, claudePath)
	if !reflect.DeepEqual(gotScript, wantScript) {
		t.Fatalf("bad shim script:\n got %q\nwant %q", gotScript, wantScript)
	}
	if !reflect.DeepEqual(gotCodex, wantCodex) {
		t.Fatalf("bad codex hooks:\n got %#v\nwant %#v", gotCodex, wantCodex)
	}
	if !reflect.DeepEqual(gotClaude, wantClaude) {
		t.Fatalf("bad claude settings:\n got %#v\nwant %#v", gotClaude, wantClaude)
	}
}

func TestUninstallRemovesOnlyManagedTurnStatusHooks(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := hostedhooks.Install(); err != nil {
		t.Fatal(err)
	}
	scriptPath := hostedhooks.TurnStatusScriptPath()
	userCommand := "echo user-stop"
	codexPath := filepath.Join(homeDir, ".codex", "hooks.json")
	claudePath := filepath.Join(homeDir, ".claude", "settings.json")
	for _, path := range []string{codexPath, claudePath} {
		root := readTestHookRoot(t, path)
		root.Theme = "dark"
		root.Hooks["Stop"][0].Hooks = append([]testCommandHook{{Type: "command", Command: userCommand}}, root.Hooks["Stop"][0].Hooks...)
		data, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	if err := hostedhooks.Uninstall(); err != nil {
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

func TestStatusReportsInstalledAndMissingState(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	missing, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	wantMissing := hostedhooks.StatusReport{}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Fatalf("bad missing status:\n got %#v\nwant %#v", missing, wantMissing)
	}

	if err := hostedhooks.Install(); err != nil {
		t.Fatal(err)
	}
	installed, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	wantInstalled := hostedhooks.StatusReport{
		ScriptInstalled: true,
		CodexInstalled:  true,
		ClaudeInstalled: true,
	}
	if !reflect.DeepEqual(installed, wantInstalled) {
		t.Fatalf("bad installed status:\n got %#v\nwant %#v", installed, wantInstalled)
	}

	if err := os.Remove(filepath.Join(homeDir, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	partial, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	wantPartial := hostedhooks.StatusReport{
		ScriptInstalled: true,
		ClaudeInstalled: true,
	}
	if !reflect.DeepEqual(partial, wantPartial) {
		t.Fatalf("bad partial status:\n got %#v\nwant %#v", partial, wantPartial)
	}
}

func TestStatusDoesNotCreateHookState(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	report, err := hostedhooks.Status()
	if err != nil {
		t.Fatal(err)
	}
	wantReport := hostedhooks.StatusReport{}
	if !reflect.DeepEqual(report, wantReport) {
		t.Fatalf("bad status:\n got %#v\nwant %#v", report, wantReport)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".ainn")); !os.IsNotExist(err) {
		t.Fatalf("expected status to leave hook state missing, got %v", err)
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
