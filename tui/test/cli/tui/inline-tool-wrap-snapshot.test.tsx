import { afterEach, describe, expect, test } from "bun:test"
import {
  AssistantErrorBeforeSubagentFixture,
  AssistantSummaryBeforeSubagentFixture,
  destroyInlineToolWrapFixture,
  FailedCompleteToolFixture,
  FailedPendingToolFixture,
  Fixture,
  LoadedReadBeforeSubagentFixture,
  renderFrame,
  renderStickyScrollFixture,
  SubagentGroupFixture,
} from "./inline-tool-wrap.fixture"

afterEach(() => {
  destroyInlineToolWrapFixture()
})

describe("TUI inline tool wrapping", () => {
  test("replaces pending copy when a tool fails before completion", async () => {
    const frame = await renderFrame(() => <FailedPendingToolFixture />, { width: 72, height: 3 })
    expect(frame).toContain("Patch failed")
    expect(frame).not.toContain("Preparing patch")
  })

  test("preserves useful completed copy when a tool fails", async () => {
    const frame = await renderFrame(() => <FailedCompleteToolFixture />, { width: 72, height: 3 })
    expect(frame).toContain("Read src/index.ts")
    expect(frame).not.toContain("Read failed")
  })

  test("snapshots consecutive grep, glob, and read rows at a narrow width", async () => {
    expect(await renderFrame(() => <Fixture />, { width: 72, height: 12 })).toMatchSnapshot()
  })

  test("snapshots expanded tool errors under the tool text", async () => {
    expect(await renderFrame(() => <Fixture errorExpanded />, { width: 72, height: 12 })).toMatchSnapshot()
  })

  test("keeps separation after a shell output block", async () => {
    expect(await renderFrame(() => <Fixture before="shell" />, { width: 72, height: 16 })).toMatchSnapshot()
  })

  test("keeps separation after a padded user message", async () => {
    expect(await renderFrame(() => <Fixture before="user" />, { width: 72, height: 14 })).toMatchSnapshot()
  })

  test("separates a contiguous subagent group from inline tools", async () => {
    expect(await renderFrame(() => <SubagentGroupFixture />, { width: 72, height: 10 })).toMatchSnapshot()
  })

  test("separates a subagent group after an expanded read", async () => {
    expect(await renderFrame(() => <LoadedReadBeforeSubagentFixture />, { width: 72, height: 8 })).toMatchSnapshot()
  })

  test("separates a subagent from the previous assistant summary", async () => {
    expect(
      await renderFrame(() => <AssistantSummaryBeforeSubagentFixture />, { width: 72, height: 5 }),
    ).toMatchSnapshot()
  })

  test("separates a subagent from the previous assistant error", async () => {
    expect(await renderFrame(() => <AssistantErrorBeforeSubagentFixture />, { width: 72, height: 7 })).toMatchSnapshot()
  })

  test("updates sticky-bottom geometry when a text separator mounts and unmounts", async () => {
    const { scroll, setSeparated, setup } = await renderStickyScrollFixture()

    await setup.renderOnce()
    expect(scroll?.scrollHeight).toBe(3)
    expect(scroll?.scrollTop).toBe(Math.max(0, scroll!.scrollHeight - scroll!.viewport.height))

    setSeparated(true)
    await setup.renderOnce()
    expect(scroll?.scrollHeight).toBe(5)
    expect(scroll?.scrollTop).toBe(Math.max(0, scroll!.scrollHeight - scroll!.viewport.height))

    setSeparated(false)
    await setup.renderOnce()
    expect(scroll?.scrollHeight).toBe(3)
    expect(scroll?.scrollTop).toBe(Math.max(0, scroll!.scrollHeight - scroll!.viewport.height))
  })
})
