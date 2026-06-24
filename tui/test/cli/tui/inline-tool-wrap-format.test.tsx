import { describe, expect, test } from "bun:test"
import {
  formatCompletedSubagentDetail,
  formatSubagentRetry,
  formatSubagentTitle,
  formatSubagentToolcalls,
  parseApplyPatchFiles,
  parseDiagnostics,
  parseQuestionAnswers,
  parseQuestions,
  parseTodos,
  toolDisplay,
} from "../../../src/routes/session"

describe("TUI inline tool formatting", () => {
  test("falls back for unknown tool names", () => {
    expect(toolDisplay("bash")).toBe("bash")
    expect(toolDisplay("plugin_tool")).toBe("generic")
  })

  test("filters malformed nested tool wire data", () => {
    expect(
      parseApplyPatchFiles([
        null,
        { type: "add" },
        { type: "add", relativePath: "a.ts", filePath: "a.ts", patch: "diff", deletions: 0 },
      ]),
    ).toEqual([
      { type: "add", relativePath: "a.ts", filePath: "a.ts", patch: "diff", deletions: 0, movePath: undefined },
    ])
    expect(parseTodos([null, { status: "pending" }, { status: "pending", content: "Safe" }])).toEqual([
      { status: "pending", content: "Safe" },
    ])
    expect(parseQuestions([{}, { question: 1 }, { question: "Continue?" }])).toEqual([{ question: "Continue?" }])
    expect(parseQuestionAnswers([null, ["yes", 1], "no"])).toEqual([[], ["yes"], []])
    expect(parseQuestionAnswers({})).toBeUndefined()
  })

  test("ignores diagnostics with malformed nested ranges", () => {
    expect(
      parseDiagnostics(
        {
          "a.ts": [
            { severity: 1, message: "missing range" },
            { severity: 1, message: "bad line", range: { start: { line: "0", character: 1 } } },
            { severity: 1, message: "valid", range: { start: { line: 2, character: 3 } } },
          ],
        },
        "a.ts",
      ),
    ).toEqual([{ message: "valid", range: { start: { line: 2, character: 3 } } }])
  })

  test("formats completed subagent toolcall details", () => {
    expect(formatCompletedSubagentDetail(0, "501ms")).toBe("501ms")
    expect(formatCompletedSubagentDetail(1, "501ms")).toBe("1 toolcall · 501ms")
    expect(formatCompletedSubagentDetail(2, "501ms")).toBe("2 toolcalls · 501ms")
    expect(formatSubagentToolcalls(0)).toBe("0 toolcalls")
  })

  test("keeps background state attached to the subagent identity", () => {
    expect(formatSubagentTitle("Explore", "Inspect renderer", false)).toBe("Explore Task — Inspect renderer")
    expect(formatSubagentTitle("Explore", "Inspect renderer", true)).toBe(
      "Explore Task (background) — Inspect renderer",
    )
  })

  test("keeps retry status ahead of wrapping messages", () => {
    expect(formatSubagentRetry(2, "Rate limited by provider")).toBe("Retrying (attempt 2) · Rate limited by provider")
  })
})
