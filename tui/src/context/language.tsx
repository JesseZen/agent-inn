import { createSignal } from "solid-js"
import { createSimpleContext } from "./helper"
import { useKV } from "./kv"
import { en, type TranslationKey, type TranslationParams } from "../i18n/en"
import { zhCN } from "../i18n/zh-CN"
import { zhTW } from "../i18n/zh-TW"
import { ja } from "../i18n/ja"

type TranslationDictionary = Partial<Record<TranslationKey, string>>

const englishKeyCount = Object.keys(en).length

function translationCoverage(dictionary: TranslationDictionary) {
  const translatedKeyCount = Object.entries(dictionary).filter(
    ([key, value]) => key in en && value.trim().length > 0,
  ).length
  return Math.round((translatedKeyCount / englishKeyCount) * 100)
}

const localeDefinitions = [
  { id: "en", name: "English", dictionary: en },
  { id: "zh-CN", name: "简体中文", dictionary: zhCN },
  { id: "zh-TW", name: "繁體中文", dictionary: zhTW },
  { id: "ja", name: "日本語", dictionary: ja },
] as const

export type Locale = (typeof localeDefinitions)[number]["id"]
type LocaleDefinition = (typeof localeDefinitions)[number] & { coverage: number }

export const localeRegistry = localeDefinitions.map((locale) => ({
  ...locale,
  coverage: locale.id === "en" ? 100 : translationCoverage(locale.dictionary),
})) as readonly LocaleDefinition[]

export const locales = localeRegistry.map((locale) => locale.id) as readonly Locale[]

export const localeLabels = Object.fromEntries(localeRegistry.map((locale) => [locale.id, locale.name])) as Record<Locale, string>

const LOCALE_KV_KEY = "locale"

export function detectLocale(value: string | undefined): Locale | undefined {
  if (!value) return undefined
  const normalized = value.replace("_", "-").toLowerCase()
  if (["zh-hant", "zh-tw", "zh-hk", "zh-mo"].some((locale) => normalized.startsWith(locale))) return "zh-TW"
  if (normalized.startsWith("zh")) return "zh-CN"
  if (normalized.startsWith("ja")) return "ja"
  if (normalized.startsWith("en")) return "en"
  return undefined
}

export function selectInitialLocale(
  persisted: string | undefined,
  environment: Record<string, string | undefined> = process.env,
): Locale {
  const persistedLocale = detectLocale(persisted)
  if (persistedLocale) return persistedLocale

  for (const name of ["LC_ALL", "LC_MESSAGES", "LANG"]) {
    const detected = detectLocale(environment[name])
    if (detected) return detected
  }
  return "en"
}

export function interpolate(template: string, params?: TranslationParams): string {
  if (!params) return template
  return template.replace(/\{\{([a-zA-Z][a-zA-Z0-9_]*)\}\}/g, (placeholder, name: string) => {
    const value = params[name]
    return value === undefined ? placeholder : String(value)
  })
}

function hasTranslation(dictionary: TranslationDictionary, key: string): key is TranslationKey {
  const value = dictionary[key as TranslationKey]
  return value !== undefined && value.trim().length > 0
}

function resolveTranslation(locale: Locale, key: string, params?: TranslationParams): string {
  const dictionary = localeRegistry.find((item) => item.id === locale)!.dictionary
  const template = hasTranslation(dictionary, key) ? dictionary[key]! : hasTranslation(en, key) ? en[key] : key
  return interpolate(template, params)
}

export function translate(locale: Locale, key: TranslationKey, params?: TranslationParams): string {
  return resolveTranslation(locale, key, params)
}

export const { use: useLanguage, provider: LanguageProvider } = createSimpleContext({
  name: "Language",
  init: () => {
    const kv = useKV()
    const persisted = kv.get(LOCALE_KV_KEY)
    const [locale, setLocale] = createSignal(selectInitialLocale(typeof persisted === "string" ? persisted : undefined))

    return {
      get locale() {
        return locale()
      },
      locales,
      labels: localeLabels,
      registry: localeRegistry,
      t(key: TranslationKey, params?: TranslationParams) {
        return resolveTranslation(locale(), key, params)
      },
      setLocale(next: Locale) {
        setLocale(next)
        kv.set(LOCALE_KV_KEY, next)
      },
    }
  },
})
