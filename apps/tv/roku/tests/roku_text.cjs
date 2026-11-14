// Preloaded into brs-node by tests/sg.mjs. Roku's renderer draws a Label's characters in the
// order they are stored and never runs the bidi algorithm: that is why the channel's Arabic is
// shaped and reordered ahead of time (tools/shape_strings.py, source/visual.brs). brs-node draws
// with node-canvas, whose text layout (Pango) runs bidi again and would turn every Arabic line
// back to front. Wrapping each string in LEFT-TO-RIGHT OVERRIDE ... POP DIRECTIONAL FORMATTING
// makes the emulator draw in stored order, as a Roku does; the channel itself is unchanged.
"use strict";
const { CanvasRenderingContext2D } = require("canvas");
const LRO = "‭";
const PDF = "‬";
const p = CanvasRenderingContext2D.prototype;
for (const name of ["fillText", "strokeText", "measureText"]) {
  const original = p[name];
  p[name] = function (text, ...rest) {
    return original.call(this, typeof text === "string" ? LRO + text + PDF : text, ...rest);
  };
}
