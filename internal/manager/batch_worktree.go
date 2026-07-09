package manager

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type batchCommandRunner interface {
	Run(args []string) (string, error)
}

type batchCommandRunnerFunc func(args []string) (string, error)

func (f batchCommandRunnerFunc) Run(args []string) (string, error) {
	return f(args)
}

var batchCommandRunnerFactory = func() batchCommandRunner {
	return batchCommandRunnerFunc(func(args []string) (string, error) {
		cmd := exec.Command(args[0], args[1:]...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil && strings.TrimSpace(stderr.String()) != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return stdout.String(), err
	})
}

var batchWorktreeCreator = createBatchWorktree

var batchWorktreeRemover = removeBatchWorktree

func createBatchWorktree(sourceDir string, targetDir string) error {
	sourceDir = strings.TrimSpace(sourceDir)
	targetDir = strings.TrimSpace(targetDir)
	if sourceDir == "" {
		return errors.New("source directory is required")
	}
	if targetDir == "" {
		return errors.New("target directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0700); err != nil {
		return err
	}
	runner := batchCommandRunnerFactory()
	stdout, err := runner.Run([]string{"git", "-C", sourceDir, "rev-parse", "--show-toplevel"})
	if err != nil {
		return err
	}
	repoRoot := strings.TrimSpace(stdout)
	if repoRoot == "" {
		return errors.New("git repository root is required")
	}
	_, err = runner.Run([]string{"git", "-C", repoRoot, "worktree", "add", "--detach", targetDir, "HEAD"})
	return err
}

func removeBatchWorktree(sourceDir string, targetDir string) error {
	sourceDir = strings.TrimSpace(sourceDir)
	targetDir = strings.TrimSpace(targetDir)
	if sourceDir == "" {
		return errors.New("source directory is required")
	}
	if targetDir == "" {
		return errors.New("target directory is required")
	}
	runner := batchCommandRunnerFactory()
	_, err := runner.Run([]string{"git", "-C", sourceDir, "worktree", "remove", "--force", targetDir})
	return err
}
