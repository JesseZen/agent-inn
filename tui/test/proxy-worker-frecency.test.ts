import { expect, test } from "bun:test"
import type { RedactedUpstream, WorkerSummary } from "../src/proxy/backend"
import {
  MAX_WORKER_FRECENCY_ENTRIES,
  WORKER_FRECENCY_FILE_NAME,
  launchWorkerSections,
  parseWorkerFrecency,
  recordWorkerFrecency,
  sortLaunchWorkers,
} from "../src/proxy/worker-frecency"
import { mountProxyApp, wait } from "./proxy-commands.fixture"

const DAY_MS = 86_400_000
const now = Date.UTC(2026, 0, 2)
const upstream: RedactedUpstream = { name: "openai", base_url: "", has_api_key: true }

function worker(name: string, port = 1000): WorkerSummary {
  return {
    name,
    port,
    role: "cli",
    upstream,
    status: "running",
    snapshot_generation: 1,
    log_level: "simple",
  }
}

test("sortLaunchWorkers puts higher frecency workers first and preserves config order for ties", () => {
  const workers = [worker("alpha"), worker("beta"), worker("gamma"), worker("delta")]
  const sorted = sortLaunchWorkers(
    workers,
    {
      beta: { frequency: 1, lastOpen: now },
      gamma: { frequency: 3, lastOpen: now - DAY_MS },
      delta: { frequency: 0, lastOpen: now },
    },
    now,
  )

  expect(sorted.map((item) => item.name)).toEqual(["gamma", "beta", "alpha", "delta"])
})

test("sortLaunchWorkers uses recency before config order when frecency scores tie", () => {
  const workers = [worker("beta"), worker("alpha")]
  const sorted = sortLaunchWorkers(
    workers,
    {
      beta: { frequency: 2, lastOpen: now - DAY_MS },
      alpha: { frequency: 1, lastOpen: now },
    },
    now,
  )

  expect(sorted.map((item) => item.name)).toEqual(["alpha", "beta"])
})

test("sortLaunchWorkers lets three recent launches overtake older heavy usage", () => {
  const workers = [worker("beta"), worker("gamma"), worker("alpha")]
  const sorted = sortLaunchWorkers(
    workers,
    {
      beta: { frequency: 10, lastOpen: now },
      alpha: { frequency: 3, lastOpen: now + 1 },
    },
    now + 1,
  )

  expect(sorted.map((item) => item.name)).toEqual(["alpha", "beta", "gamma"])
})

test("launchWorkerSections puts recent workers first and removes them from the original list", () => {
  const workers = [worker("alpha"), worker("beta"), worker("gamma"), worker("delta"), worker("epsilon")]
  const sections = launchWorkerSections(
    workers,
    {
      beta: { frequency: 1, lastOpen: now },
      gamma: { frequency: 3, lastOpen: now - DAY_MS },
      delta: { frequency: 1, lastOpen: now + 1 },
      epsilon: { frequency: 1, lastOpen: now - DAY_MS },
    },
    now,
  )

  expect({
    recent: sections.recent.map((item) => item.name),
    rest: sections.rest.map((item) => item.name),
  }).toEqual({
    recent: ["gamma", "delta", "beta"],
    rest: ["alpha", "epsilon"],
  })
})

test("recordWorkerFrecency increments the selected worker and stamps the current time", () => {
  const next = recordWorkerFrecency("beta", { beta: { frequency: 2, lastOpen: 10 } }, now)

  expect(next).toEqual({ name: "beta", frequency: 3, lastOpen: now })
})

test("parseWorkerFrecency skips corruption, keeps latest worker state, and limits entries", () => {
  const entries = Array.from({ length: MAX_WORKER_FRECENCY_ENTRIES + 1 }, (_, index) =>
    JSON.stringify({ name: `worker-${index}`, frequency: 1, lastOpen: index }),
  )
  entries.push("broken", JSON.stringify({ name: "worker-1000", frequency: 2, lastOpen: 2000 }))

  const result = parseWorkerFrecency(entries.join("\n"))

  expect(result).toHaveLength(MAX_WORKER_FRECENCY_ENTRIES)
  expect(result[0]).toEqual({ name: "worker-1000", frequency: 2, lastOpen: 2000 })
  expect(result.some((entry) => entry.name === "worker-0")).toBe(false)
})

test("external launch worker picker orders cli workers by stored frecency", async () => {
  const alphaWorker = "alpha-cli"
  const gammaWorker = "gamma-cli"

  const app = await mountProxyApp({
    settings: { launch: { default_mode: "external-window" } },
    stateFiles: {
      [WORKER_FRECENCY_FILE_NAME]: `${JSON.stringify({ name: gammaWorker, frequency: 5, lastOpen: Date.now() })}\n`,
    },
    workers: [worker(alphaWorker, 11200), worker(gammaWorker, 11202)],
  })

  try {
    app.api.keymap.dispatchCommand("proxy.launch")
    await wait(async () => {
      await app.render()
      const frame = app.frame()
      return frame.includes(alphaWorker) && frame.includes(gammaWorker)
    })

    const workerRows = app
      .lines()
      .flatMap((line) => [gammaWorker, alphaWorker].filter((name) => line.includes(name)))

    expect(app.frame()).toContain("Recent")
    expect(workerRows).toEqual([gammaWorker, alphaWorker])
  } finally {
    await app.cleanup()
  }
})
