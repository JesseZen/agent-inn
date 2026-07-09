/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import { afterEach, expect, mock, test } from "bun:test"
import { onMount } from "solid-js"
import { SDKProvider, useSDK } from "../src/context/sdk"
import { createEventSource, createFetch, directory, json } from "./fixture/tui-sdk"

afterEach(() => {
  mock.restore()
})

type BatchClient = ReturnType<typeof useSDK>["client"] & {
  createBatch(input: {
    title: string
    prompt?: string
    worker_name: string
    count: number
    source_directory: string
    model?: string
  }): Promise<unknown>
  selectBatchWinner(batchID: string, variantID: string): Promise<unknown>
}

async function withBatchClient(fn: (client: BatchClient) => Promise<void>) {
  let done: Promise<void> | undefined
  const calls = {
    createBatch: [] as unknown[],
    selectBatchWinner: [] as string[],
  }

  function Probe() {
    const client = useSDK().client as BatchClient
    onMount(() => {
      done = fn(client)
    })
    return <box />
  }

  const events = createEventSource()
  const fetch = createFetch(async (url, request) => {
    if (url.pathname === "/api/batches" && request.method === "POST") {
      calls.createBatch.push(await request.json())
      return json({
        id: "batch_1",
        title: "Race parser fix",
        worker_name: "coder",
        worker_port: 19091,
        source_directory: "/repo",
        created_at: "2026-07-09T00:00:00Z",
        variants: [],
      })
    }
    if (url.pathname === "/api/batches/batch_1/variants/variant_2/select" && request.method === "POST") {
      calls.selectBatchWinner.push(url.pathname)
      return json({
        id: "batch_1",
        title: "Race parser fix",
        worker_name: "coder",
        worker_port: 19091,
        source_directory: "/repo",
        created_at: "2026-07-09T00:00:00Z",
        winner_variant_id: "variant_2",
        variants: [],
      })
    }
    return undefined
  })

  const app = await testRender(() => (
    <SDKProvider url="http://test" directory={directory} events={events.source} fetch={fetch.fetch}>
      <Probe />
    </SDKProvider>
  ))

  try {
    await done
  } finally {
    app.renderer.destroy()
  }

  return calls
}

test("createBatch POSTs the exact request body to /api/batches", async () => {
  let created: unknown

  const calls = await withBatchClient(async (client) => {
    created = await client.createBatch({
      title: "Race parser fix",
      prompt: "fix it",
      worker_name: "coder",
      count: 3,
      source_directory: "/repo",
      model: "gpt-5",
    })
  })

  expect(calls.createBatch).toEqual([
    {
      title: "Race parser fix",
      prompt: "fix it",
      worker_name: "coder",
      count: 3,
      source_directory: "/repo",
      model: "gpt-5",
    },
  ])
  expect(created).toEqual({
    id: "batch_1",
    title: "Race parser fix",
    worker_name: "coder",
    worker_port: 19091,
    source_directory: "/repo",
    created_at: "2026-07-09T00:00:00Z",
    variants: [],
  })
})

test("selectBatchWinner POSTs to the batch variant select endpoint", async () => {
  let selected: unknown

  const calls = await withBatchClient(async (client) => {
    selected = await client.selectBatchWinner("batch_1", "variant_2")
  })

  expect(calls.selectBatchWinner).toEqual(["/api/batches/batch_1/variants/variant_2/select"])
  expect(selected).toEqual({
    id: "batch_1",
    title: "Race parser fix",
    worker_name: "coder",
    worker_port: 19091,
    source_directory: "/repo",
    created_at: "2026-07-09T00:00:00Z",
    winner_variant_id: "variant_2",
    variants: [],
  })
})

test("pasteHostedPrompt sends literal prompt text without Enter", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      const child = {
        stdout: { on() {} },
        stderr: { on() {} },
        on(event: string, handler: (code?: number) => void) {
          if (event === "exit") queueMicrotask(() => handler(0))
          return child
        },
      }
      return child
    },
  }))

  const launchModule = await import(`../src/proxy/launch?paste-hosted-prompt=${Date.now()}`)
  const pasted = await launchModule.pasteHostedPrompt({
    prompt: "prompt",
    tmuxSocketName: "ainn",
    tmuxWindowID: "@12",
  })

  expect(pasted).toBe(true)
  expect(spawns).toEqual([
    {
      cmd: "tmux",
      args: ["-L", "ainn", "send-keys", "-l", "-t", "@12", "prompt"],
    },
  ])
})

test("pasteHostedPrompt returns false for an empty prompt", async () => {
  const spawns: Array<{ cmd: string; args: string[] }> = []

  mock.module("node:child_process", () => ({
    spawn(cmd: string, args: string[]) {
      spawns.push({ cmd, args })
      throw new Error("empty prompt should not spawn")
    },
  }))

  const launchModule = await import(`../src/proxy/launch?paste-empty-prompt=${Date.now()}`)
  const pasted = await launchModule.pasteHostedPrompt({
    prompt: "",
    tmuxWindowID: "@12",
  })

  expect(pasted).toBe(false)
  expect(spawns).toEqual([])
})
