//go:build linux || darwin

package manager

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const hostedSessionLockWait = 100 * time.Millisecond

func TestHostedSessionRegistryWaitsForConcurrentWriter(t *testing.T) {
	dir := t.TempDir()
	registry := NewHostedSessionRegistry(filepath.Join(dir, "sessions.json"))

	cmd := exec.Command(os.Args[0], "-test.run=TestHostedSessionLockHelperProcess")
	cmd.Env = append(os.Environ(), "AINN_HOSTED_SESSION_LOCK_HELPER=1", "AINN_HOSTED_SESSION_LOCK_PATH="+registry.lock)
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
		_, err := registry.Create(HostedSessionRecord{
			SessionLabel: "solve problem A",
			WorkerName:   "worker",
			WorkerPort:   11199,
		})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Create returned before lock released: %v", err)
	case <-time.After(hostedSessionLockWait):
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Create did not finish after lock released")
	}
}

func TestHostedSessionLockHelperProcess(t *testing.T) {
	if os.Getenv("AINN_HOSTED_SESSION_LOCK_HELPER") != "1" {
		return
	}
	lockPath := os.Getenv("AINN_HOSTED_SESSION_LOCK_PATH")
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
