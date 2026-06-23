import { describe, expect, test } from "bun:test"
import { defaultLogoStyleID, logoStyleIDs, logoStyles, resolveLogoStyle } from "../src/logo"

function fullLines(id: (typeof logoStyleIDs)[number]) {
  const shape = logoStyles[id].shape
  return shape.left.map((line, index) => `${line} ${shape.right[index]}`)
}

function metrics() {
  return Object.fromEntries(
    logoStyleIDs.map((id) => [
      id,
      {
        rows: logoStyles[id].shape.right.length,
        width: logoStyles[id].shape.right[0]?.length,
        fullWidth: fullLines(id)[0]?.length,
        anchors: logoStyles[id].anchors,
        anchorText: Object.fromEntries(
          Object.entries(logoStyles[id].anchors).map(([letter, [x, y]]) => [letter, fullLines(id)[y]?.[x]]),
        ),
      },
    ]),
  )
}

describe("logo styles", () => {
  test("registers selectable logo styles with stable dimensions and glyph anchors", () => {
    expect({
      defaultLogoStyleID,
      logoStyleIDs,
      resolvedDefault: resolveLogoStyle(undefined).id,
      resolvedInvalid: resolveLogoStyle("missing").id,
      metrics: metrics(),
    }).toEqual({
      defaultLogoStyleID: "ascii-shadow",
      logoStyleIDs: ["ascii-shadow", "terrace"],
      resolvedDefault: "ascii-shadow",
      resolvedInvalid: "ascii-shadow",
      metrics: {
        "ascii-shadow": {
          rows: 10,
          width: 36,
          fullWidth: 37,
          anchors: {
            c: [2, 2],
            p: [9, 2],
            t: [20, 2],
          },
          anchorText: {
            c: "█",
            p: "█",
            t: "█",
          },
        },
        terrace: {
          rows: 10,
          width: 36,
          fullWidth: 37,
          anchors: {
            c: [1, 2],
            p: [12, 2],
            t: [28, 2],
          },
          anchorText: {
            c: "░",
            p: "░",
            t: "░",
          },
        },
      },
    })
  })
})
