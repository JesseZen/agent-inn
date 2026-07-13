package logging

// Components are dot-namespaced origins of a log line. They occupy the third
// column of every line and let a reader grep all output from one subsystem.
const (
	ComponentRoot           = "root"
	ComponentRootSupervisor = "root.supervisor"
	ComponentTmuxSupervisor = "tmux.supervisor"
	ComponentManagerSuper   = "manager.super"
	ComponentManagerHealth  = "manager.health"
	ComponentManagerAPI     = "manager.api"
	ComponentWorkerProxy    = "worker.proxy"
	ComponentWorkerLife     = "worker.life"
)

// Events are stable, greppable names in the fourth column of every log line.
// They are a documented contract: docs are in internal/logging/EVENTS.md, and
// changing a name breaks anyone (human or AI) grepping for it. Add new events
// rather than repurposing existing ones.
const (
	EventRequestStart         = "request.start"
	EventRequestDone          = "request.done"
	EventUpstreamFail         = "upstream.fail"
	EventUpstreamFailover     = "upstream.failover"
	EventModuleFail           = "module.fail"
	EventWorkerStart          = "worker.start"
	EventWorkerReady          = "worker.ready"
	EventWorkerSignal         = "worker.signal"
	EventWorkerStop           = "worker.stop"
	EventSnapshotReload       = "snapshot.reload"
	EventWorkerSpawn          = "worker.spawn"
	EventWorkerExit           = "worker.exit"
	EventWorkerRestart        = "worker.restart"
	EventMetricsPersist       = "metrics.persist"
	EventHealthFail           = "health.fail"
	EventConfigPatch          = "config.patch"
	EventRootStart            = "root.start"
	EventRootStop             = "root.stop"
	EventRootSignal           = "root.signal"
	EventRootPanic            = "root.panic"
	EventRootStack            = "root.stack"
	EventRootServerError      = "root.server_error"
	EventTUIStart             = "tui.start"
	EventTUIExit              = "tui.exit"
	EventRootSupervisorStart  = "root.supervisor.start"
	EventRootSupervisorChild  = "root.supervisor.child"
	EventRootSupervisorSignal = "root.supervisor.signal"
	EventRootSupervisorExit   = "root.supervisor.exit"
	EventRootPreviousUnclean  = "root.previous_unclean"
	EventTmuxServerStart      = "tmux.server.start"
	EventTmuxServerSignal     = "tmux.server.signal"
	EventTmuxServerExit       = "tmux.server.exit"
	EventTmuxClientExit       = "tmux.client.exit"
	EventHostedTurnPoll       = "hosted_turn.poll"
)
