package logging

// Components are dot-namespaced origins of a log line. They occupy the third
// column of every line and let a reader grep all output from one subsystem.
const (
	ComponentRoot          = "root"
	ComponentManagerSuper  = "manager.super"
	ComponentManagerHealth = "manager.health"
	ComponentManagerAPI    = "manager.api"
	ComponentWorkerProxy   = "worker.proxy"
	ComponentWorkerLife    = "worker.life"
)

// Events are stable, greppable names in the fourth column of every log line.
// They are a documented contract: docs are in internal/logging/EVENTS.md, and
// changing a name breaks anyone (human or AI) grepping for it. Add new events
// rather than repurposing existing ones.
const (
	EventRequestStart   = "request.start"
	EventRequestDone    = "request.done"
	EventUpstreamFail   = "upstream.fail"
	EventModuleFail     = "module.fail"
	EventWorkerStart    = "worker.start"
	EventWorkerReady    = "worker.ready"
	EventSnapshotReload = "snapshot.reload"
	EventWorkerSpawn    = "worker.spawn"
	EventWorkerExit     = "worker.exit"
	EventWorkerRestart  = "worker.restart"
	EventHealthFail     = "health.fail"
	EventConfigPatch    = "config.patch"
	EventRootStart      = "root.start"
	EventRootStop       = "root.stop"
)
