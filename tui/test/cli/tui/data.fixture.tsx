/** @jsxImportSource @opentui/solid */
import { testRender } from "@opentui/solid"
import type { Event, GlobalEvent } from "@codex-proxy/sdk/v2"
import { onMount } from "solid-js"
import { ProjectProvider } from "../../../src/context/project"
import { SDKProvider } from "../../../src/context/sdk"
import { DataProvider, useData } from "../../../src/context/data"
import { createEventSource, createFetch, directory, json, type FetchHandler } from "../../fixture/tui-sdk"
import { TestTuiContexts } from "../../fixture/tui-environment"

export async function wait(fn: () => boolean | Promise<boolean>, timeout = 2000) {
  const start = Date.now()
  while (!(await fn())) {
    if (Date.now() - start > timeout) throw new Error("timed out waiting for condition")
    await Bun.sleep(10)
  }
}

export function global(payload: Event): GlobalEvent {
  return { directory, project: "proj_test", payload }
}

export function emitEvent(events: ReturnType<typeof createEventSource>, payload: Event) {
  events.emit(global(payload))
}

export async function mountData(override?: FetchHandler) {
  const events = createEventSource()
  const calls = createFetch(override)
  let data!: ReturnType<typeof useData>
  let done!: () => void
  const mounted = new Promise<void>((resolve) => {
    done = resolve
  })

  function Probe() {
    data = useData()
    onMount(done)
    return <box />
  }

  const app = await testRender(() => (
    <TestTuiContexts>
      <SDKProvider url="http://test" directory={directory} events={events.source} fetch={calls.fetch}>
        <ProjectProvider>
          <DataProvider>
            <Probe />
          </DataProvider>
        </ProjectProvider>
      </SDKProvider>
    </TestTuiContexts>
  ))

  await mounted
  return { app, calls, data, events }
}

export { directory, json }
