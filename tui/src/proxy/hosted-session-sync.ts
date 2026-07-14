import type { HostedSessionListResponse, HostedSessionSnapshot, ManagerEvent } from "./hosted-session-contract"

type HostedSessionSubscriptionOptions = {
  lastEventID?: string
  onEnd: () => void
}

type HostedSessionSyncInput = {
  list: () => Promise<HostedSessionListResponse>
  subscribe: (
    handler: (event: ManagerEvent) => void,
    options: HostedSessionSubscriptionOptions,
  ) => Promise<() => void>
  commit: (sessions: Record<string, HostedSessionSnapshot>, cursor: string) => void
  onManagerEvent?: (event: ManagerEvent) => void
  onError?: (error: unknown) => void
  initialCursor?: string
}

const snapshotChangedEvent = "hosted.session.snapshot.changed"
const sessionDeletedEvent = "hosted.session.deleted"
const resyncRequiredEvent = "manager.resync-required"

export function createHostedSessionSync(input: HostedSessionSyncInput) {
  let active = true
  let baselineInstalled = false
  let sessions: Record<string, HostedSessionSnapshot> = {}
  let cursor = input.initialCursor ?? "0"
  let buffered: ManagerEvent[] = []
  let unsubscribe = () => {}
  let streamEnded = false
  let baselineGeneration = 0

  function apply(event: ManagerEvent) {
    if (BigInt(event.id) <= BigInt(cursor)) return
    if (event.type === snapshotChangedEvent) {
      const snapshot = event.payload.snapshot as HostedSessionSnapshot
      sessions = { ...sessions, [snapshot.session_id]: snapshot }
    } else if (event.type === sessionDeletedEvent) {
      const sessionID = event.payload.session_id as string
      const next = { ...sessions }
      delete next[sessionID]
      sessions = next
    }
    cursor = event.id
  }

  async function loadBaseline() {
    const generation = ++baselineGeneration
    baselineInstalled = false
    const response = await input.list()
    if (generation !== baselineGeneration) return
    sessions = Object.fromEntries(response.sessions.map((snapshot) => [snapshot.session_id, snapshot]))
    cursor = response.event_cursor
    const pending = buffered.toSorted((left, right) => {
      const leftID = BigInt(left.id)
      const rightID = BigInt(right.id)
      return leftID < rightID ? -1 : leftID > rightID ? 1 : 0
    })
    buffered = []
    for (const event of pending) apply(event)
    baselineInstalled = true
    input.commit(sessions, cursor)
  }

  function handle(event: ManagerEvent) {
    if (event.type === resyncRequiredEvent) {
      if (baselineInstalled) void loadBaseline().catch(input.onError)
      return
    }
    if (event.type !== snapshotChangedEvent && event.type !== sessionDeletedEvent) {
      input.onManagerEvent?.(event)
      return
    }
    if (!baselineInstalled) {
      buffered.push(event)
      return
    }
    const previousCursor = cursor
    apply(event)
    if (cursor !== previousCursor) input.commit(sessions, cursor)
  }

  async function startPair() {
    streamEnded = false
    unsubscribe()
    unsubscribe = () => {}
    let ended = false
    const subscription = input.subscribe(handle, {
      ...(cursor === "0" ? {} : { lastEventID: cursor }),
      onEnd: () => {
        ended = true
        if (active) streamEnded = true
      },
    })
    const baseline = loadBaseline()
    let stop: (() => void) | undefined
    let stopped = false
    const close = () => {
      if (stopped) return
      stopped = true
      stop?.()
      if (unsubscribe === close) unsubscribe = () => {}
    }
    const observedSubscription = subscription.then((value) => {
      stop = value
      if (active) {
        unsubscribe = close
        streamEnded = ended
      } else close()
    })
    const [subscriptionResult, baselineResult] = await Promise.allSettled([observedSubscription, baseline])
    if (subscriptionResult.status === "rejected") {
      if (active) streamEnded = true
      close()
      throw subscriptionResult.reason
    }
    if (baselineResult.status === "rejected") {
      if (active) streamEnded = true
      close()
      throw baselineResult.reason
    }
    if (!active) close()
  }

  return {
    async start() {
      active = true
      await startPair()
    },
    refresh() {
      return streamEnded ? startPair() : loadBaseline()
    },
    stop() {
      active = false
      streamEnded = false
      baselineGeneration++
      unsubscribe()
      unsubscribe = () => {}
    },
  }
}
