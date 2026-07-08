import type { WorkerSummary } from "./backend"

export type WorkerFrecency = { frequency: number; lastOpen: number }
export type WorkerFrecencyEntry = WorkerFrecency & { name: string }

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
        return JSON.parse(line) as WorkerFrecencyEntry
      } catch {
        return undefined
      }
    })
    .filter((line): line is WorkerFrecencyEntry => line !== undefined)
    .reduce<Record<string, WorkerFrecencyEntry>>((result, entry) => {
      result[entry.name] = entry
      return result
    }, {})

  return Object.values(latest)
    .sort((a, b) => b.lastOpen - a.lastOpen)
    .slice(0, MAX_WORKER_FRECENCY_ENTRIES)
}

export function recordWorkerFrecency(name: string, data: Record<string, WorkerFrecency>, now = Date.now()) {
  return {
    name,
    frequency: (data[name]?.frequency ?? 0) + 1,
    lastOpen: now,
  }
}

export function trimWorkerFrecency(data: Record<string, WorkerFrecency>) {
  return Object.entries(data)
    .map(([name, entry]) => ({ name, frequency: entry.frequency, lastOpen: entry.lastOpen }))
    .sort((a, b) => b.lastOpen - a.lastOpen)
    .slice(0, MAX_WORKER_FRECENCY_ENTRIES)
}

export function sortLaunchWorkers<T extends Pick<WorkerSummary, "name">>(
  workers: T[],
  data: Record<string, WorkerFrecency>,
  now = Date.now(),
) {
  return workers
    .map((worker, index) => {
      const entry = data[worker.name]
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

export function launchWorkerSections<T extends Pick<WorkerSummary, "name">>(
  workers: T[],
  data: Record<string, WorkerFrecency>,
  now = Date.now(),
) {
  const recent = sortLaunchWorkers(
    workers.filter((worker) => (data[worker.name]?.frequency ?? 0) > 0),
    data,
    now,
  ).slice(0, WORKER_FRECENCY_RECENT_LIMIT)
  const recentNames = new Set(recent.map((worker) => worker.name))

  return {
    recent,
    rest: workers.filter((worker) => !recentNames.has(worker.name)),
  }
}
