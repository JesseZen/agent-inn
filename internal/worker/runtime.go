package worker

import (
	"fmt"
	"sync/atomic"

	"github.com/jesse/codex-app-proxy/internal/module"
	"github.com/jesse/codex-app-proxy/internal/provider"
)

type RuntimeConfigSnapshot struct {
	Generation        int
	Provider          provider.RuntimeProvider
	Modules           []module.Middleware
	ConfigPatchState  module.ConfigPatchState
	ConfigPatchDetail map[string]string
}

func (s RuntimeConfigSnapshot) Validate() error {
	if s.Provider.BaseURL == "" {
		return fmt.Errorf("provider base URL is required")
	}
	return nil
}

type snapshotHolder struct {
	value atomic.Value
}

func newSnapshotHolder(snapshot RuntimeConfigSnapshot) *snapshotHolder {
	holder := &snapshotHolder{}
	holder.value.Store(snapshot)
	return holder
}

func (h *snapshotHolder) Load() RuntimeConfigSnapshot {
	return h.value.Load().(RuntimeConfigSnapshot)
}

func (h *snapshotHolder) Store(snapshot RuntimeConfigSnapshot) {
	h.value.Store(snapshot)
}
