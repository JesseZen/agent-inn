export const logo = {
  left: [
    " ██████╗██████╗████████╗",
    "██╔════╝██╔══██╗╚══██╔══╝",
    "██║     ██████╔╝   ██║   ",
    "██║     ██╔═══╝    ██║   ",
    "╚██████╗██║        ██║   ",
    " ╚═════╝╚═╝        ╚═╝   ",
  ],
  right: [
    "  ░██████  ░█████████  ░██████████",
    " ░██   ░██ ░██     ░██     ░██    ",
    "░██        ░██     ░██     ░██    ",
    "░██        ░█████████      ░██    ",
    "░██        ░██             ░██    ",
    " ░██   ░██ ░██             ░██    ",
    "  ░██████  ░██             ░██",
  ],
}

export type LogoShape = {
  left: string[]
  right: string[]
}

export type LogoStyleID = "ascii-shadow" | "terrace"

export type LogoStyle = {
  id: LogoStyleID
  title: string
  description: string
  shape: LogoShape
  anchors: {
    c: readonly [number, number]
    p: readonly [number, number]
    t: readonly [number, number]
  }
}

const WIDTH = 36

function shape(lines: string[]): LogoShape {
  return {
    left: lines.map(() => ""),
    right: lines.map((line) => line.padEnd(WIDTH, " ")),
  }
}

export const defaultLogoStyleID = "ascii-shadow" satisfies LogoStyleID

export const logoStyleIDs = ["ascii-shadow", "terrace"] as const

export const logoStyles: Record<LogoStyleID, LogoStyle> = {
  "ascii-shadow": {
    id: "ascii-shadow",
    title: "ASCII Shadow",
    description: "Box-drawing CPT logo with compact geometric strokes.",
    shape: shape([
      " ██████╗██████╗████████╗",
      "██╔════╝██╔══██╗╚══██╔══╝",
      "██║     ██████╔╝   ██║   ",
      "██║     ██╔═══╝    ██║   ",
      "╚██████╗██║        ██║   ",
      " ╚═════╝╚═╝        ╚═╝   ",
      "",
      "",
      "",
      "",
    ]),
    anchors: {
      c: [2, 2],
      p: [9, 2],
      t: [20, 2],
    },
  },
  terrace: {
    id: "terrace",
    title: "Terrace",
    description: "Wide terminal banner with stepped shaded strokes.",
    shape: shape([
      "  ░██████  ░█████████  ░██████████",
      " ░██   ░██ ░██     ░██     ░██    ",
      "░██        ░██     ░██     ░██    ",
      "░██        ░█████████      ░██    ",
      "░██        ░██             ░██    ",
      " ░██   ░██ ░██             ░██    ",
      "  ░██████  ░██             ░██",
      "",
      "",
      "",
    ]),
    anchors: {
      c: [1, 2],
      p: [12, 2],
      t: [28, 2],
    },
  },
}

export function resolveLogoStyle(value: unknown) {
  return logoStyles[logoStyleIDs.find((id) => id === value) ?? defaultLogoStyleID]
}

export const cpt = logoStyles[defaultLogoStyleID].shape

export const go = {
  left: ["    ", "█▀▀▀", "█_^█", "▀▀▀▀"],
  right: ["    ", "█▀▀█", "█__█", "▀▀▀▀"],
}

export const marks = "_^~,"
