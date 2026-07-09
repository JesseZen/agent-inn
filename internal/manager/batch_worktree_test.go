package manager

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCreateBatchWorktreeAddsDetachedWorktreeAtHead(t *testing.T) {
	var commands [][]string
	restore := setBatchCommandRunnerForTest(func(args []string) (string, error) {
		commands = append(commands, append([]string{}, args...))
		if reflect.DeepEqual(args, []string{"git", "-C", "/repo/sub", "rev-parse", "--show-toplevel"}) {
			return "/repo\n", nil
		}
		return "", nil
	})
	defer restore()

	targetDir := filepath.Join(t.TempDir(), "worktrees", "batch_1", "1")
	if err := createBatchWorktree(" /repo/sub ", " "+targetDir+" "); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"git", "-C", "/repo/sub", "rev-parse", "--show-toplevel"},
		{"git", "-C", "/repo", "worktree", "add", "--detach", targetDir, "HEAD"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands mismatch:\n got %#v\nwant %#v", commands, want)
	}
}

func TestCreateBatchWorktreeRejectsEmptyInput(t *testing.T) {
	for _, input := range [][2]string{{"", "/tmp/target"}, {"/repo", ""}} {
		if err := createBatchWorktree(input[0], input[1]); err == nil {
			t.Fatalf("expected error for %#v", input)
		}
	}
}

func TestCreateBatchWorktreeReturnsGitError(t *testing.T) {
	restore := setBatchCommandRunnerForTest(func(args []string) (string, error) {
		return "", errors.New("git failed")
	})
	defer restore()

	if err := createBatchWorktree("/repo", filepath.Join(t.TempDir(), "target")); err == nil {
		t.Fatal("expected error")
	}
}

func TestRemoveBatchWorktreeRunsGitWorktreeRemove(t *testing.T) {
	var commands [][]string
	restore := setBatchCommandRunnerForTest(func(args []string) (string, error) {
		commands = append(commands, append([]string{}, args...))
		return "", nil
	})
	defer restore()

	if err := removeBatchWorktree("/repo/sub", "/tmp/worktree"); err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"git", "-C", "/repo/sub", "worktree", "remove", "--force", "/tmp/worktree"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands mismatch:\n got %#v\nwant %#v", commands, want)
	}
}

func setBatchWorktreeCreatorForTest(fn func(string, string) error) func() {
	old := batchWorktreeCreator
	batchWorktreeCreator = fn
	return func() { batchWorktreeCreator = old }
}

func setBatchCommandRunnerForTest(fn func([]string) (string, error)) func() {
	old := batchCommandRunnerFactory
	batchCommandRunnerFactory = func() batchCommandRunner {
		return batchCommandRunnerFunc(fn)
	}
	return func() { batchCommandRunnerFactory = old }
}
