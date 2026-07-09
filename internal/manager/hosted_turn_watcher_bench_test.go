package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
)

func BenchmarkHostedTurnWatcherIdlePoll1000ActiveTurns(b *testing.B) {
	stateDir := b.TempDir()
	transcriptPath := filepath.Join(stateDir, "codex.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"event_msg","payload":{"type":"unrelated"}}`+"\n"), 0600); err != nil {
		b.Fatal(err)
	}

	registry := NewHostedSessionRegistry(HostedSessionRegistryPath(stateDir))
	now := time.Now().UTC()
	file := &hostedSessionFile{
		NextSessionID:  1000,
		WorkerCounters: map[string]int{"worker": 1000},
		Sessions:       map[string]HostedSessionRecord{},
	}
	for i := 0; i < 1000; i++ {
		sessionID := fmt.Sprintf("hs_%d", i+1)
		file.Sessions[sessionID] = HostedSessionRecord{
			SessionID:          sessionID,
			SessionLabel:       fmt.Sprintf("solve problem %d", i),
			WorkerName:         "worker",
			WorkerPort:         11000 + i,
			TmuxWindowID:       fmt.Sprintf("@%d", i),
			TurnState:          HostedTurnStateRunning,
			TurnGeneration:     1,
			TurnTranscriptPath: transcriptPath,
			TurnID:             fmt.Sprintf("turn_%d", i),
			CreatedAt:          now,
			LastOpenedAt:       now,
		}
	}
	if err := registry.saveFile(file); err != nil {
		b.Fatal(err)
	}

	watcher := newHostedTurnWatcher(config.Settings{StateDir: stateDir}, registry, hostedTMuxRunnerFunc(func(args []string) (string, error) {
		b.Fatalf("unexpected tmux call: %#v", args)
		return "", nil
	}))
	if err := watcher.pollOnce(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := watcher.pollOnce(); err != nil {
			b.Fatal(err)
		}
	}
}
