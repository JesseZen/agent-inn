//go:build linux || darwin

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

func configureRootChildTerminal(stdin io.Reader, attr *syscall.SysProcAttr) (func() error, error) {
	restore := func() error { return nil }
	terminal, ok := stdin.(*os.File)
	if !ok {
		return restore, nil
	}

	fd := int(terminal.Fd())
	foregroundPGID, err := terminalForegroundProcessGroup(fd)
	if errors.Is(err, syscall.ENOTTY) {
		return restore, nil
	}
	if err != nil {
		return restore, fmt.Errorf("read terminal foreground process group: %w", err)
	}
	if foregroundPGID != syscall.Getpgrp() {
		return restore, nil
	}

	attr.Foreground = true
	attr.Ctty = fd
	return func() error {
		if err := setTerminalForegroundProcessGroup(fd, foregroundPGID); err != nil {
			return fmt.Errorf("restore terminal foreground process group: %w", err)
		}
		return nil
	}, nil
}

func terminalForegroundProcessGroup(fd int) (int, error) {
	var pgid int32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCGPGRP),
		uintptr(unsafe.Pointer(&pgid)),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(pgid), nil
}

func setTerminalForegroundProcessGroup(fd int, pgid int) error {
	wasIgnored := signal.Ignored(syscall.SIGTTOU)
	if !wasIgnored {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
	}
	value := int32(pgid)
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TIOCSPGRP),
		uintptr(unsafe.Pointer(&value)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
