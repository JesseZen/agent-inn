//go:build linux || darwin

package hostedhooks_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/hostedhooks"
)

const hookLockWait = 100 * time.Millisecond

func TestReconcileWaitsForConcurrentHookWriter(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	lockPath := hostedhooks.TurnStatusScriptPath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestHookLockHelperProcess")
	cmd.Env = append(os.Environ(), "AINN_HOOK_LOCK_HELPER=1", "AINN_HOOK_LOCK_PATH="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatalf("lock helper did not report locked")
	}

	done := make(chan error, 1)
	go func() {
		done <- hostedhooks.Reconcile(config.Settings{
			Terminal: config.TerminalSettings{
				Tmux: config.TmuxSettings{TurnStatusHooks: true},
			},
		})
	}()

	select {
	case err := <-done:
		t.Fatalf("Reconcile returned before lock released: %v", err)
	case <-time.After(hookLockWait):
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Reconcile did not finish after lock released")
	}
}

func TestHookLockHelperProcess(t *testing.T) {
	if os.Getenv("AINN_HOOK_LOCK_HELPER") != "1" {
		return
	}
	lockPath := os.Getenv("AINN_HOOK_LOCK_PATH")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Hour)
}
