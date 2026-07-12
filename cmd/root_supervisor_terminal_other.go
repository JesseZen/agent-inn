//go:build !(linux || darwin)

package cmd

import (
	"io"
	"syscall"
)

func configureRootChildTerminal(io.Reader, *syscall.SysProcAttr) (func() error, error) {
	return func() error { return nil }, nil
}
