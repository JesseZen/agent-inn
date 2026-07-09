import path from "path"
import { onMount } from "solid-js"
import { createStore } from "solid-js/store"
import { createSimpleContext } from "../context/helper"
import { useTuiPaths } from "../context/runtime"
import { appendText, readText, writeText } from "../util/persistence"
import type { WorkerSummary } from "./backend"
import {
  MAX_WORKER_FRECENCY_ENTRIES,
  launchWorkerSections,
  parseWorkerFrecency,
  recordWorkerFrecency,
  trimWorkerFrecency,
  WORKER_FRECENCY_FILE_NAME,
  type WorkerFrecency,
} from "./worker-frecency"

export const { use: useWorkerFrecency, provider: WorkerFrecencyProvider } = createSimpleContext({
  name: "WorkerFrecency",
  init: () => {
    const paths = useTuiPaths()
    const filePath = path.join(paths.state, WORKER_FRECENCY_FILE_NAME)
    const [store, setStore] = createStore({ ready: false, data: {} as Record<string, WorkerFrecency> })

    onMount(async () => {
      const entries = parseWorkerFrecency(await readText(filePath).catch(() => ""))
      setStore(
        "data",
        Object.fromEntries(entries.map((entry) => [entry.id, { frequency: entry.frequency, lastOpen: entry.lastOpen }])),
      )
      if (entries.length > 0) writeText(filePath, entries.map((entry) => JSON.stringify(entry)).join("\n") + "\n").catch(() => {})
      setStore("ready", true)
    })

    function record(workerID: string) {
      const entry = recordWorkerFrecency(workerID, store.data)
      const nextData = {
        ...store.data,
        [workerID]: { frequency: entry.frequency, lastOpen: entry.lastOpen },
      }
      setStore("data", nextData)
      appendText(filePath, JSON.stringify(entry) + "\n").catch(() => {})

      if (Object.keys(nextData).length <= MAX_WORKER_FRECENCY_ENTRIES) return
      const entries = trimWorkerFrecency(nextData)
      setStore(
        "data",
        Object.fromEntries(entries.map((entry) => [entry.id, { frequency: entry.frequency, lastOpen: entry.lastOpen }])),
      )
      writeText(filePath, entries.map((entry) => JSON.stringify(entry)).join("\n") + "\n").catch(() => {})
    }

    return {
      get ready() {
        return store.ready
      },
      sections<T extends Pick<WorkerSummary, "id">>(workers: T[]) {
        return launchWorkerSections(workers, store.data)
      },
      record,
      data: () => store.data,
    }
  },
})
