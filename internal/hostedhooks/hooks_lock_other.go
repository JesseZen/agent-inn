//go:build !(linux || darwin)

package hostedhooks

import "sync"

var hookConfigMu sync.Mutex

func withHookConfigLock(fn func() error) error {
	hookConfigMu.Lock()
	defer hookConfigMu.Unlock()
	return fn()
}
