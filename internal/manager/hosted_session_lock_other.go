//go:build !(linux || darwin)

package manager

import "os"

func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
