import type { AssistantMessage, Part, Provider, UserMessage } from "@agent-inn/sdk/v2"
import { Locale } from "./locale"
import * as Model from "./model"
import { translate, type Locale as LanguageLocale } from "../context/language"

export type TranscriptOptions = {
  thinking: boolean
  toolDetails: boolean
  assistantMetadata: boolean
  providers?: Provider[]
  locale?: LanguageLocale
}

export type SessionInfo = {
  id: string
  title: string
  time: {
    created: number
    updated: number
  }
}

export type MessageWithParts = {
  info: UserMessage | AssistantMessage
  parts: Part[]
}

export function formatTranscript(
  session: SessionInfo,
  messages: MessageWithParts[],
  options: TranscriptOptions,
): string {
  const providers = Model.index(options.providers)
  const locale = options.locale ?? "en"
  const t = (key: Parameters<typeof translate>[1], params?: Record<string, string | number>) =>
    translate(locale, key, params)
  let transcript = `# ${session.title}\n\n`
  transcript += `**${t("transcript.sessionId")}** ${session.id}\n`
  transcript += `**${t("transcript.created")}** ${Locale.datetime(session.time.created, locale)}\n`
  transcript += `**${t("transcript.updated")}** ${Locale.datetime(session.time.updated, locale)}\n\n`
  transcript += `---\n\n`

  for (const msg of messages) {
    transcript += formatMessage(msg.info, msg.parts, options, providers)
    transcript += `---\n\n`
  }

  return transcript
}

export function formatMessage(
  msg: UserMessage | AssistantMessage,
  parts: Part[],
  options: TranscriptOptions,
  providers?: Provider[] | ReadonlyMap<string, Provider>,
): string {
  let result = ""

  if (msg.role === "user") {
    result += `## ${translate(options.locale ?? "en", "transcript.user")}\n\n`
  } else {
    result += formatAssistantHeader(msg, options.assistantMetadata, providers ?? options.providers, options.locale)
  }

  for (const part of parts) {
    result += formatPart(part, options)
  }

  return result
}

export function formatAssistantHeader(
  msg: AssistantMessage,
  includeMetadata: boolean,
  providers?: Provider[] | ReadonlyMap<string, Provider>,
  locale: LanguageLocale = "en",
): string {
  if (!includeMetadata) {
    return `## ${translate(locale, "transcript.assistant")}\n\n`
  }

  const duration =
    msg.time.completed && msg.time.created ? ((msg.time.completed - msg.time.created) / 1000).toFixed(1) + "s" : ""

  const modelName = Model.name(providers, msg.providerID, msg.modelID)

  return `## ${translate(locale, "transcript.assistant")} (${Locale.titlecase(msg.agent)} · ${modelName}${duration ? ` · ${duration}` : ""})\n\n`
}

export function formatPart(part: Part, options: TranscriptOptions): string {
  if (part.type === "text" && !part.synthetic) {
    return `${part.text}\n\n`
  }

  if (part.type === "reasoning") {
    if (options.thinking) {
      return `_${translate(options.locale ?? "en", "transcript.thinking")}_\n\n${part.text}\n\n`
    }
    return ""
  }

  if (part.type === "tool") {
    const locale = options.locale ?? "en"
    let result = `**${translate(locale, "transcript.tool")} ${part.tool}**\n`
    if (options.toolDetails && part.state.input) {
      result += `\n**${translate(locale, "transcript.input")}**\n\`\`\`json\n${JSON.stringify(part.state.input, null, 2)}\n\`\`\`\n`
    }
    if (options.toolDetails && part.state.status === "completed" && part.state.output) {
      result += `\n**${translate(locale, "transcript.output")}**\n\`\`\`\n${part.state.output}\n\`\`\`\n`
    }
    if (options.toolDetails && part.state.status === "error" && part.state.error) {
      result += `\n**${translate(locale, "transcript.error")}**\n\`\`\`\n${part.state.error}\n\`\`\`\n`
    }
    result += `\n`
    return result
  }

  return ""
}
