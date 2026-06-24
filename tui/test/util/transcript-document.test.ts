import { describe, expect, test } from "bun:test"
import { formatTranscript } from "../../src/util/transcript"
import { providers } from "./transcript.fixture"

describe("transcript document formatting", () => {
  test("formats complete transcript", () => {
    const session = {
      id: "ses_abc123",
      title: "Test Session",
      time: { created: 1000000000000, updated: 1000000001000 },
    }
    const messages = [
      {
        info: {
          id: "msg_1",
          sessionID: "ses_abc123",
          role: "user" as const,
          agent: "build",
          model: { providerID: "anthropic", modelID: "claude-sonnet-4-20250514" },
          time: { created: 1000000000000 },
        },
        parts: [{ id: "p1", sessionID: "ses_abc123", messageID: "msg_1", type: "text" as const, text: "Hello" }],
      },
      {
        info: {
          id: "msg_2",
          sessionID: "ses_abc123",
          role: "assistant" as const,
          agent: "build",
          modelID: "claude-sonnet-4-20250514",
          providerID: "anthropic",
          mode: "",
          parentID: "msg_1",
          path: { cwd: "/test", root: "/test" },
          cost: 0.001,
          tokens: { input: 100, output: 50, reasoning: 0, cache: { read: 0, write: 0 } },
          time: { created: 1000000000100, completed: 1000000000600 },
        },
        parts: [{ id: "p2", sessionID: "ses_abc123", messageID: "msg_2", type: "text" as const, text: "Hi!" }],
      },
    ]
    const options = {
      thinking: false,
      toolDetails: false,
      assistantMetadata: true,
      providers,
    }

    const result = formatTranscript(session, messages, options)

    expect(result).toContain("# Test Session")
    expect(result).toContain("**Session ID:** ses_abc123")
    expect(result).toContain("## User")
    expect(result).toContain("Hello")
    expect(result).toContain("## Assistant (Build · Claude Sonnet 4 · 0.5s)")
    expect(result).toContain("Hi!")
    expect(result).toContain("---")
  })

  test("falls back to raw model id when provider data is missing", () => {
    const session = {
      id: "ses_abc123",
      title: "Test Session",
      time: { created: 1000000000000, updated: 1000000001000 },
    }
    const messages = [
      {
        info: {
          id: "msg_1",
          sessionID: "ses_abc123",
          role: "assistant" as const,
          agent: "build",
          modelID: "claude-sonnet-4-20250514",
          providerID: "anthropic",
          mode: "",
          parentID: "msg_0",
          path: { cwd: "/test", root: "/test" },
          cost: 0.001,
          tokens: { input: 100, output: 50, reasoning: 0, cache: { read: 0, write: 0 } },
          time: { created: 1000000000100, completed: 1000000000600 },
        },
        parts: [{ id: "p1", sessionID: "ses_abc123", messageID: "msg_1", type: "text" as const, text: "Response" }],
      },
    ]

    const result = formatTranscript(session, messages, {
      thinking: false,
      toolDetails: false,
      assistantMetadata: true,
    })

    expect(result).toContain("## Assistant (Build · claude-sonnet-4-20250514 · 0.5s)")
  })

  test("formats transcript without assistant metadata", () => {
    const session = {
      id: "ses_abc123",
      title: "Test Session",
      time: { created: 1000000000000, updated: 1000000001000 },
    }
    const messages = [
      {
        info: {
          id: "msg_1",
          sessionID: "ses_abc123",
          role: "assistant" as const,
          agent: "build",
          modelID: "claude-sonnet-4-20250514",
          providerID: "anthropic",
          mode: "",
          parentID: "msg_0",
          path: { cwd: "/test", root: "/test" },
          cost: 0.001,
          tokens: { input: 100, output: 50, reasoning: 0, cache: { read: 0, write: 0 } },
          time: { created: 1000000000100, completed: 1000000000600 },
        },
        parts: [{ id: "p1", sessionID: "ses_abc123", messageID: "msg_1", type: "text" as const, text: "Response" }],
      },
    ]
    const options = { thinking: false, toolDetails: false, assistantMetadata: false }

    const result = formatTranscript(session, messages, options)

    expect(result).toContain("## Assistant\n\n")
    expect(result).not.toContain("Build")
    expect(result).not.toContain("claude-sonnet-4-20250514")
  })
})
