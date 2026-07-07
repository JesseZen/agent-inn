//go:build linux || darwin

package hostedhooks

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func withHookConfigLock(fn func() error) error {
	lockPath := TurnStatusScriptPath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), hookConfigDirMode); err != nil {
		return fmt.Errorf("create hook lock directory: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, hookConfigFileMode)
	if err != nil {
		return fmt.Errorf("open hook lock: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock hook config: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
