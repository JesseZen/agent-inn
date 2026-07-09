import type { WorkerSummary } from "./backend"

export type WorkerFrecency = { frequency: number; lastOpen: number }
export type WorkerFrecencyEntry = WorkerFrecency & { id: string }

export const WORKER_FRECENCY_FILE_NAME = "launch-worker-frecency.jsonl"
export const MAX_WORKER_FRECENCY_ENTRIES = 100
export const WORKER_FRECENCY_RECENT_LIMIT = 3
const FRECENCY_DAY_MS = 86_400_000
const FRECENCY_FREQUENCY_CAP = 3

function calculateWorkerFrecency(entry: WorkerFrecency | undefined, now: number) {
  if (!entry) return 0
  return Math.min(entry.frequency, FRECENCY_FREQUENCY_CAP) / (1 + (now - entry.lastOpen) / FRECENCY_DAY_MS)
}

export function parseWorkerFrecency(text: string) {
  const latest = text
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      try {
        const entry = JSON.parse(line) as WorkerFrecency & { id?: string; name?: string }
        const id = entry.id ?? entry.name
        if (!id) return undefined
        return { id, frequency: entry.frequency, lastOpen: entry.lastOpen }
      } catch {
        return undefined
      }
    })
    .filter((line): line is WorkerFrecencyEntry => line !== undefined)
    .reduce<Record<string, WorkerFrecencyEntry>>((result, entry) => {
      result[entry.id] = entry
      return result
    }, {})

  return Object.values(latest)
    .sort((a, b) => b.lastOpen - a.lastOpen)
    .slice(0, MAX_WORKER_FRECENCY_ENTRIES)
}

export function recordWorkerFrecency(id: string, data: Record<string, WorkerFrecency>, now = Date.now()) {
  return {
    id,
    frequency: (data[id]?.frequency ?? 0) + 1,
    lastOpen: now,
  }
}

export function trimWorkerFrecency(data: Record<string, WorkerFrecency>) {
  return Object.entries(data)
    .map(([id, entry]) => ({ id, frequency: entry.frequency, lastOpen: entry.lastOpen }))
    .sort((a, b) => b.lastOpen - a.lastOpen)
    .slice(0, MAX_WORKER_FRECENCY_ENTRIES)
}

export function sortLaunchWorkers<T extends Pick<WorkerSummary, "id">>(
  workers: T[],
  data: Record<string, WorkerFrecency>,
  now = Date.now(),
) {
  return workers
    .map((worker, index) => {
      const entry = data[worker.id]
      const score = calculateWorkerFrecency(entry, now)
      return {
        worker,
        index,
        lastOpen: score > 0 ? (entry?.lastOpen ?? 0) : 0,
        score,
      }
    })
    .sort((a, b) => {
      if (a.score !== b.score) return b.score - a.score
      if (a.lastOpen !== b.lastOpen) return b.lastOpen - a.lastOpen
      return a.index - b.index
    })
    .map((item) => item.worker)
}

export function launchWorkerSections<T extends Pick<WorkerSummary, "id">>(
  workers: T[],
  data: Record<string, WorkerFrecency>,
  now = Date.now(),
) {
  const recent = sortLaunchWorkers(
    workers.filter((worker) => (data[worker.id]?.frequency ?? 0) > 0),
    data,
    now,
  ).slice(0, WORKER_FRECENCY_RECENT_LIMIT)
  const recentIDs = new Set(recent.map((worker) => worker.id))

  return {
    recent,
    rest: workers.filter((worker) => !recentIDs.has(worker.id)),
  }
}
