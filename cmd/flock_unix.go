//go:build linux || darwin

package cmd

import (
	"os"
	"syscall"
)

// flockTryLock 尝试非阻塞独占锁。文件描述符关闭时锁自动释放。
func flockTryLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
