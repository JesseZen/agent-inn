package hostedhooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
)

const (
	hooksDirName            = "hooks"
	turnStatusScriptName    = "hosted-turn-status"
	codexDir                = "~/.codex"
	claudeDir               = "~/.claude"
	codexHooksFileName      = "hooks.json"
	claudeSettingsFileName  = "settings.json"
	commandHookType         = "command"
	defaultHookMatcher      = ""
	hookScriptFileMode      = 0700
	hookConfigFileMode      = 0600
	hookConfigDirMode       = 0700
	hostedTurnFailureReason = "stop_failure"
)

type managedHook struct {
	event          string
	state          string
	reason         string
	watchCodexTurn bool
}

var (
	codexManagedHooks = []managedHook{
		{event: "SessionStart", state: constants.HostedTurnStateIdle},
		{event: "UserPromptSubmit", state: constants.HostedTurnStateRunning, watchCodexTurn: true},
		{event: "Stop", state: constants.HostedTurnStateDone},
	}
	claudeManagedHooks = []managedHook{
		{event: "SessionStart", state: constants.HostedTurnStateIdle},
		{event: "UserPromptSubmit", state: constants.HostedTurnStateRunning},
		{event: "Stop", state: constants.HostedTurnStateDone},
		{event: "StopFailure", state: constants.HostedTurnStateFailed, reason: hostedTurnFailureReason},
	}
)

func Reconcile(settings config.Settings) error {
	return withHookConfigLock(func() error {
		if settings.Terminal.Tmux.TurnStatusHooks {
			return install()
		}
		return uninstall()
	})
}

func TurnStatusScriptPath() string {
	return filepath.Join(expandHomePath(config.DefaultConfigDir), hooksDirName, turnStatusScriptName)
}

func Install() error {
	return withHookConfigLock(install)
}

func install() error {
	scriptPath := TurnStatusScriptPath()
	if err := os.MkdirAll(filepath.Dir(scriptPath), hookConfigDirMode); err != nil {
		return fmt.Errorf("create hook directory: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(turnStatusScript), hookScriptFileMode); err != nil {
		return fmt.Errorf("write turn status hook script: %w", err)
	}
	if err := os.Chmod(scriptPath, hookScriptFileMode); err != nil {
		return fmt.Errorf("chmod turn status hook script: %w", err)
	}
	if err := installManagedHooks(codexHooksPath(), scriptPath, codexManagedHooks); err != nil {
		return err
	}
	if err := installManagedHooks(claudeSettingsPath(), scriptPath, claudeManagedHooks); err != nil {
		return err
	}
	return nil
}

func Uninstall() error {
	return withHookConfigLock(uninstall)
}

func uninstall() error {
	scriptPath := TurnStatusScriptPath()
	if err := uninstallManagedHooks(codexHooksPath(), scriptPath); err != nil {
		return err
	}
	if err := uninstallManagedHooks(claudeSettingsPath(), scriptPath); err != nil {
		return err
	}
	if err := os.Remove(scriptPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove turn status hook script: %w", err)
	}
	return nil
}

func installManagedHooks(path string, scriptPath string, managed []managedHook) error {
	root, err := readHookRoot(path)
	if err != nil {
		return err
	}
	hooks, err := hookMap(path, root)
	if err != nil {
		return err
	}
	if _, err := removeManagedHookCommands(path, hooks, scriptPath); err != nil {
		return err
	}
	for _, item := range managed {
		existing, err := hookList(path, item.event, hooks[item.event])
		if err != nil {
			return err
		}
		entry := map[string]any{
			"matcher": defaultHookMatcher,
			"hooks": []any{
				map[string]any{
					"type":    commandHookType,
					"command": hookCommand(scriptPath, item),
				},
			},
		}
		hooks[item.event] = append(existing, entry)
	}
	root["hooks"] = hooks
	return writeHookRoot(path, root)
}

func uninstallManagedHooks(path string, scriptPath string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	root, err := readHookRoot(path)
	if err != nil {
		return err
	}
	hooks, err := hookMap(path, root)
	if err != nil {
		return err
	}
	changed, err := removeManagedHookCommands(path, hooks, scriptPath)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	root["hooks"] = hooks
	return writeHookRoot(path, root)
}

func readHookRoot(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hook config %s: %w", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse hook config %s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeHookRoot(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), hookConfigDirMode); err != nil {
		return fmt.Errorf("create hook config directory: %w", err)
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hook config %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, hookConfigFileMode); err != nil {
		return fmt.Errorf("write hook config %s: %w", path, err)
	}
	return nil
}

func hookMap(path string, root map[string]any) (map[string]any, error) {
	raw, ok := root["hooks"]
	if !ok {
		hooks := map[string]any{}
		root["hooks"] = hooks
		return hooks, nil
	}
	hooks, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hooks in %s must be an object", path)
	}
	return hooks, nil
}

func hookList(path string, event string, raw any) ([]any, error) {
	if raw == nil {
		return []any{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("hooks.%s in %s must be an array", event, path)
	}
	return items, nil
}

func removeManagedHookCommands(path string, hooks map[string]any, scriptPath string) (bool, error) {
	changed := false
	quotedScriptPath := shellQuote(scriptPath)
	for event, raw := range hooks {
		items, ok := raw.([]any)
		if !ok {
			return false, fmt.Errorf("hooks.%s in %s must be an array", event, path)
		}
		nextItems := make([]any, 0, len(items))
		for _, item := range items {
			matcher, ok := item.(map[string]any)
			if !ok {
				nextItems = append(nextItems, item)
				continue
			}
			rawCommands, ok := matcher["hooks"].([]any)
			if !ok {
				nextItems = append(nextItems, item)
				continue
			}
			nextCommands := make([]any, 0, len(rawCommands))
			for _, rawCommand := range rawCommands {
				command, ok := rawCommand.(map[string]any)
				if !ok {
					nextCommands = append(nextCommands, rawCommand)
					continue
				}
				commandType, _ := command["type"].(string)
				commandLine, _ := command["command"].(string)
				if commandType == commandHookType && (strings.Contains(commandLine, scriptPath) || strings.Contains(commandLine, quotedScriptPath)) {
					changed = true
					continue
				}
				nextCommands = append(nextCommands, rawCommand)
			}
			if len(nextCommands) == 0 {
				changed = true
				continue
			}
			matcher["hooks"] = nextCommands
			nextItems = append(nextItems, matcher)
		}
		if len(nextItems) == 0 {
			delete(hooks, event)
			changed = true
		} else {
			hooks[event] = nextItems
		}
	}
	return changed, nil
}

func hookCommand(scriptPath string, item managedHook) string {
	command := shellQuote(scriptPath) + " " + item.state
	if item.reason != "" {
		command += " " + item.reason
	}
	command += " --capture-launcher-session-id"
	if item.watchCodexTurn {
		command += " --watch-codex-turn"
	}
	return command
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func codexHooksPath() string {
	return filepath.Join(expandHomePath(codexDir), codexHooksFileName)
}

func claudeSettingsPath() string {
	return filepath.Join(expandHomePath(claudeDir), claudeSettingsFileName)
}

func expandHomePath(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

const turnStatusScript = `#!/bin/sh
if [ -z "${AINN_HOSTED_SESSION_ID:-}" ]; then
  exit 0
fi
: "${AINN_CONFIG_DIR:?}"
: "${AINN_EXECUTABLE:?}"
state="$1"
reason=""
shift
if [ "$#" -gt 0 ] && [ "$1" != "--capture-launcher-session-id" ]; then
  reason="$1"
  shift
fi
exec "$AINN_EXECUTABLE" hosted-session mark --config-dir "$AINN_CONFIG_DIR" --session-id "$AINN_HOSTED_SESSION_ID" --state "$state" --reason "$reason" "$@"
`
