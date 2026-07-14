package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/manager"
)

const hostedTurnWatcherSidecarIdleTimeout = 5 * time.Minute

var hostedTurnWatcherSidecarPollInterval = 500 * time.Millisecond

type hostedTurnWatcherSidecarManager interface {
	Close()
	StartHostedTurnWatcherWithPollGuard(interval time.Duration, beforePoll func() bool) func()
}

var hostedTurnWatcherSidecarManagerFactory = func(cfg config.Config, configPath string) hostedTurnWatcherSidecarManager {
	return manager.New(manager.Config{Config: cfg, ConfigPath: configPath})
}

func runHostedSessionWatchAll(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("hosted-session watch-all", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configDir := flags.String("config-dir", expandHome(config.DefaultConfigDir), "config directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	rootRunning, err := rootInstanceRunning(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to inspect root instance: %v\n", err)
		return 1
	}
	if rootRunning {
		return 0
	}
	lockPath, err := hostedTurnWatcherSidecarLockPath(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to start hosted turn watcher: %v\n", err)
		return 1
	}
	release, err := rootLockerFactory(lockPath).Acquire()
	if errors.Is(err, errAlreadyLocked) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "failed to start hosted turn watcher: %v\n", err)
		return 1
	}
	defer release()
	rootRunning, err = rootInstanceRunning(*configDir)
	if err != nil {
		fmt.Fprintf(stderr, "failed to inspect root instance: %v\n", err)
		return 1
	}
	if rootRunning {
		return 0
	}

	configPath := filepath.Join(*configDir, config.ConfigFileName)
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	mgr := hostedTurnWatcherSidecarManagerFactory(cfg, configPath)
	defer mgr.Close()
	pollGuardErrors := make(chan error, 1)
	stopWatcher := mgr.StartHostedTurnWatcherWithPollGuard(hostedTurnWatcherSidecarPollInterval, func() bool {
		rootRunning, err := rootInstanceRunning(*configDir)
		if err != nil {
			pollGuardErrors <- err
			return false
		}
		return !rootRunning
	})
	defer stopWatcher()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(cfg.Settings.StateDir))
	runner := launchRunnerFactory(io.Discard, stderr)
	idleSince := time.Now()
	ticker := time.NewTicker(hostedTurnWatcherSidecarPollInterval)
	defer ticker.Stop()
	for {
		rootRunning, err := rootInstanceRunning(*configDir)
		if err != nil {
			fmt.Fprintf(stderr, "failed to inspect root instance: %v\n", err)
			return 1
		}
		if rootRunning {
			return 0
		}
		watches, err := registry.WatchedTurns()
		if err != nil {
			fmt.Fprintf(stderr, "failed to inspect hosted turns: %v\n", err)
			return 1
		}
		if len(watches) > 0 {
			idleSince = time.Time{}
		} else if idleSince.IsZero() {
			idleSince = time.Now()
		} else if time.Since(idleSince) >= hostedTurnWatcherSidecarIdleTimeout {
			return 0
		}

		if _, err := runner.Run(manager.TmuxHasSessionCommandForSettings(cfg.Settings)); err != nil {
			if isTmuxHostMissingError(err) {
				return 0
			}
			fmt.Fprintf(stderr, "failed to inspect tmux host session: %v\n", err)
			return 1
		}
		select {
		case err := <-pollGuardErrors:
			fmt.Fprintf(stderr, "failed to inspect root instance: %v\n", err)
			return 1
		case <-ticker.C:
		}
	}
}

func rootInstanceRunning(configDir string) (bool, error) {
	path, err := rootLockPath(configDir)
	if err != nil {
		return false, err
	}
	release, err := rootLockerFactory(path).Acquire()
	if errors.Is(err, errAlreadyLocked) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	release()
	return false, nil
}

func hostedTurnWatcherSidecarLockPath(configDir string) (string, error) {
	path, err := rootLockPath(configDir)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".lock") + "-hosted-watcher.lock", nil
}
