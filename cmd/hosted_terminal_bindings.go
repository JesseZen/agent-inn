package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

func installTmuxTurnStatusHooks(runner launchRunner, settings config.Settings, configDir string, executable string) error {
	ownerOut, err := runner.Run(manager.TmuxTurnStatusOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux turn status owner: %w", err)
	}
	owner := strings.TrimSpace(ownerOut)
	if owner != "" {
		if owner != configDir {
			return fmt.Errorf("tmux turn status hooks are owned by config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", owner, configDir)
		}
	} else {
		hooksOut, err := runner.Run(manager.TmuxShowHooksCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux hooks: %w", err)
		}
		mouseBindingOut, err := runner.Run(manager.TmuxListAcknowledgeTurnMouseBindingCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux mouse binding: %w", err)
		}
		todoBindingOut, err := runner.Run(manager.TmuxListToggleTodoMouseBindingCommandForSettings(settings))
		if err != nil {
			return fmt.Errorf("inspect tmux todo mouse binding: %w", err)
		}
		legacyOwner, found, err := managedTurnStatusConfigDir(hooksOut, mouseBindingOut, todoBindingOut)
		if err != nil {
			return err
		}
		if found && legacyOwner != configDir {
			return fmt.Errorf("tmux turn status hooks are owned by legacy config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", legacyOwner, configDir)
		}
		if _, err := runner.Run(manager.TmuxSetTurnStatusOwnerCommandForSettings(settings, configDir)); err != nil {
			return fmt.Errorf("set tmux turn status owner: %w", err)
		}
	}
	if _, err := runner.Run(manager.TmuxAcknowledgeTurnHookCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux turn acknowledgement hook: %w", err)
	}
	if _, err := runner.Run(manager.TmuxToggleTodoMouseBindingCommandForSettings(settings, configDir, executable)); err != nil {
		return fmt.Errorf("install tmux hosted todo mouse binding: %w", err)
	}
	return nil
}

func installTmuxHostedPopupBinding(runner launchRunner, settings config.Settings, configDir string, executable string) error {
	key := strings.TrimSpace(settings.Terminal.Tmux.HostedPopupKey)
	ownerOut, err := runner.Run(manager.TmuxHostedPopupOwnerCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux hosted popup owner: %w", err)
	}
	owner := strings.TrimSpace(ownerOut)
	if owner != "" && owner != configDir {
		return fmt.Errorf("tmux hosted popup binding is owned by config dir %q, current config dir is %q; use a unique tmux socket/session for test instances", owner, configDir)
	}
	storedKeyOut, err := runner.Run(manager.TmuxHostedPopupKeyCommandForSettings(settings))
	if err != nil {
		return fmt.Errorf("inspect tmux hosted popup key owner: %w", err)
	}
	storedKey := strings.TrimSpace(storedKeyOut)
	if storedKey != key && storedKey != "" {
		bindingOut, err := runner.Run(manager.TmuxListHostedPopupBindingCommandForSettings(settings, storedKey))
		if err != nil {
			return fmt.Errorf("inspect tmux hosted popup binding: %w", err)
		}
		if !isAINNHostedPopupBinding(bindingOut, storedKey, configDir) {
			return fmt.Errorf("tmux hosted popup key %q already has a non-AINN binding; choose a different hosted_popup_key or use a unique tmux socket/session", storedKey)
		}
	}
	if key != "" {
		bindingOut, err := runner.Run(manager.TmuxListHostedPopupBindingCommandForSettings(settings, key))
		if err != nil {
			if !strings.HasSuffix(strings.TrimSpace(err.Error()), "unknown key: "+key) {
				return fmt.Errorf("inspect tmux hosted popup binding: %w", err)
			}
			bindingOut = ""
		}
		if strings.TrimSpace(bindingOut) != "" && (owner == "" || !isAINNHostedPopupBinding(bindingOut, key, configDir)) {
			return fmt.Errorf("tmux hosted popup key %q already has a non-AINN binding; choose a different hosted_popup_key or use a unique tmux socket/session", key)
		}
	}
	if owner == "" {
		if _, err := runner.Run(manager.TmuxSetHostedPopupOwnerCommandForSettings(settings, configDir)); err != nil {
			return fmt.Errorf("set tmux hosted popup owner: %w", err)
		}
	}
	if storedKey != key {
		if storedKey != "" {
			if _, err := runner.Run(manager.TmuxUnbindHostedPopupBindingCommandForSettings(settings, storedKey)); err != nil {
				return fmt.Errorf("remove tmux hosted popup binding: %w", err)
			}
		}
		if _, err := runner.Run(manager.TmuxSetHostedPopupKeyCommandForSettings(settings, key)); err != nil {
			return fmt.Errorf("set tmux hosted popup key owner: %w", err)
		}
	}
	managerURL := strings.TrimSpace(os.Getenv("AINN_URL"))
	if managerURL == "" {
		managerURL = defaultManagerURL
	}
	mode := manager.TmuxHostedPopupMouseModeSelect
	if settings.Terminal.Tmux.TurnStatusHooks {
		mode = manager.TmuxHostedPopupMouseModeAcknowledge
	}
	if _, err := runner.Run(manager.TmuxHostedPopupMouseBindingCommandForSettings(settings, configDir, managerURL, executable, mode)); err != nil {
		return fmt.Errorf("install tmux hosted popup mouse binding: %w", err)
	}
	if key == "" {
		return nil
	}
	if _, err := runner.Run(manager.TmuxHostedPopupBindingCommandForSettings(settings, key, configDir, managerURL, executable)); err != nil {
		return fmt.Errorf("install tmux hosted popup binding: %w", err)
	}
	return nil
}

func isAINNHostedPopupBinding(binding string, key string, configDir string) bool {
	popupIndex := strings.Index(binding, "display-popup ")
	if popupIndex < 0 {
		return false
	}
	fields := strings.Fields(binding[popupIndex+len("display-popup "):])
	popupWidth, popupHeight := "", ""
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		switch field {
		case "-E":
		case "-w", "-h", "-x", "-y":
			i++
			if i == len(fields) {
				return false
			}
			value := strings.Trim(fields[i], "\"'")
			if field == "-w" {
				popupWidth = value
			}
			if field == "-h" {
				popupHeight = value
			}
		case "-T":
			i++
			for i < len(fields) && !strings.HasSuffix(fields[i], "\"") && !strings.HasSuffix(fields[i], "'") {
				i++
			}
			if i == len(fields) {
				return false
			}
		default:
			if !strings.HasPrefix(field, "-") {
				i = len(fields)
			}
		}
	}
	recognizedGeometry := popupWidth == manager.TmuxHostedPopupWidth && popupHeight == manager.TmuxHostedPopupHeight || popupWidth == legacyHostedPopupWidth && popupHeight == legacyHostedPopupHeight
	return strings.Contains(binding, "bind-key -T prefix "+key+" ") && strings.Contains(binding, "-E") && strings.Contains(binding, "-x R") && strings.Contains(binding, "-y 0") && recognizedGeometry && strings.Contains(binding, manager.TmuxHostedPopupTitle) && strings.Contains(binding, "hosted-session popup") && strings.Contains(binding, "--config-dir") && strings.Contains(binding, configDir)
}

func managedTurnStatusConfigDir(hooksOutput string, acknowledgeBindingOutput string, todoBindingOutput string) (string, bool, error) {
	owner := ""
	found := false
	outputs := []struct {
		text    string
		matches func(string) bool
	}{
		{hooksOutput, func(line string) bool {
			return strings.Contains(line, manager.TmuxAcknowledgeTurnHook) && strings.Contains(line, hostedSessionAcknowledgeCommand)
		}},
		{acknowledgeBindingOutput, func(line string) bool {
			return strings.Contains(line, manager.TmuxAcknowledgeMouseKey) && strings.Contains(line, hostedSessionAcknowledgeCommand)
		}},
		{todoBindingOutput, func(line string) bool {
			return strings.Contains(line, manager.TmuxToggleTodoMouseKey) && strings.Contains(line, hostedSessionToggleTodoCommand)
		}},
	}
	for _, output := range outputs {
		for _, line := range strings.Split(output.text, "\n") {
			if !output.matches(line) {
				continue
			}
			marker := "--config-dir "
			index := strings.Index(line, marker)
			if index < 0 {
				return "", false, fmt.Errorf("failed to parse managed tmux turn status hook config dir from %q", line)
			}
			configDir, ok := parseTmuxSingleQuotedToken(line[index+len(marker):])
			if !ok {
				return "", false, fmt.Errorf("failed to parse managed tmux turn status hook config dir from %q", line)
			}
			if found && owner != configDir {
				return "", false, fmt.Errorf("tmux turn status hooks contain multiple legacy config dirs %q and %q; use a unique tmux socket/session for test instances", owner, configDir)
			}
			owner, found = configDir, true
		}
	}
	return owner, found, nil
}

func parseTmuxSingleQuotedToken(value string) (string, bool) {
	if value == "" || value[0] != '\'' {
		return "", false
	}
	var parsed strings.Builder
	for i := 1; i < len(value); {
		if strings.HasPrefix(value[i:], "'\\''") {
			parsed.WriteByte('\'')
			i += len("'\\''")
			continue
		}
		if value[i] == '\'' {
			return parsed.String(), true
		}
		parsed.WriteByte(value[i])
		i++
	}
	return "", false
}
