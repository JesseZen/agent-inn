import { expect, test } from "bun:test"
import type { Locale as LanguageLocale } from "../../src/context/language"
import * as Locale from "../../src/util/locale"

const timestamp = new Date(2024, 0, 2, 13, 5).getTime()

test("formats dates and times with the active language locale", () => {
  const locale: LanguageLocale = "zh-CN"
  const date = new Date(timestamp)

  expect({
    time: Locale.time(timestamp, locale),
    datetime: Locale.datetime(timestamp, locale),
    todayOrDateTime: Locale.todayTimeOrDateTime(timestamp, locale),
  }).toEqual({
    time: date.toLocaleTimeString(locale, { timeStyle: "short" }),
    datetime: `${date.toLocaleTimeString(locale, { timeStyle: "short" })} · ${date.toLocaleDateString(locale)}`,
    todayOrDateTime: `${date.toLocaleTimeString(locale, { timeStyle: "short" })} · ${date.toLocaleDateString(locale)}`,
  })
})

test("preserves default formatting and English number abbreviations", () => {
  const date = new Date(timestamp)

  expect({
    time: Locale.time(timestamp),
    datetime: Locale.datetime(timestamp),
    small: Locale.number(999),
    thousand: Locale.number(1_000),
    million: Locale.number(1_000_000),
  }).toEqual({
    time: date.toLocaleTimeString(undefined, { timeStyle: "short" }),
    datetime: `${date.toLocaleTimeString(undefined, { timeStyle: "short" })} · ${date.toLocaleDateString()}`,
    small: "999",
    thousand: "1.0K",
    million: "1.0M",
  })
})

test("formats active-locale numbers and USD costs", () => {
  const locale: LanguageLocale = "zh-CN"

  expect({
    small: Locale.number(12.5, locale),
    thousand: Locale.number(12_500, locale),
    million: Locale.number(1_250_000, locale),
    englishCost: Locale.currency(1.25, "en"),
    chineseCost: Locale.currency(1.25, locale),
  }).toEqual({
    small: new Intl.NumberFormat(locale, { useGrouping: false, maximumFractionDigits: 20 }).format(12.5),
    thousand: `${new Intl.NumberFormat(locale, {
      useGrouping: false,
      minimumFractionDigits: 1,
      maximumFractionDigits: 1,
    }).format(12.5)}K`,
    million: `${new Intl.NumberFormat(locale, {
      useGrouping: false,
      minimumFractionDigits: 1,
      maximumFractionDigits: 1,
    }).format(1.25)}M`,
    englishCost: new Intl.NumberFormat("en", { style: "currency", currency: "USD" }).format(1.25),
    chineseCost: new Intl.NumberFormat(locale, { style: "currency", currency: "USD" }).format(1.25),
  })
})

test("preserves ASCII truncation output", () => {
  expect({
    right: Locale.truncate("abcdef", 5),
    left: Locale.truncateLeft("abcdef", 5),
    middle: Locale.truncateMiddle("abcdef", 5),
  }).toEqual({
    right: "abcd…",
    left: "…cdef",
    middle: "ab…ef",
  })
})

test("truncates CJK text to terminal cell width", () => {
  const right = Locale.truncate("中文abc", 5)
  const left = Locale.truncateLeft("abc中文", 5)
  const middle = Locale.truncateMiddle("中文测试", 5)

  expect({
    right: { text: right, width: Bun.stringWidth(right) },
    left: { text: left, width: Bun.stringWidth(left) },
    middle: { text: middle, width: Bun.stringWidth(middle) },
  }).toEqual({
    right: { text: "中文…", width: 5 },
    left: { text: "…中文", width: 5 },
    middle: { text: "中…试", width: 5 },
  })
})

test("does not split graphemes while truncating mixed text", () => {
  const family = "👨‍👩‍👧‍👦"
  const right = Locale.truncate(`a${family}bc`, 4)
  const left = Locale.truncateLeft(`abc${family}`, 4)
  const middle = Locale.truncateMiddle(`ab${family}cd`, 5)

  expect({
    right: { text: right, width: Bun.stringWidth(right) },
    left: { text: left, width: Bun.stringWidth(left) },
    middle: { text: middle, width: Bun.stringWidth(middle) },
  }).toEqual({
    right: { text: `a${family}…`, width: 4 },
    left: { text: `…c${family}`, width: 4 },
    middle: { text: "ab…cd", width: 5 },
  })
})
