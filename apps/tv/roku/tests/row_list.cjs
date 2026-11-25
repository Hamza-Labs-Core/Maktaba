// Preloaded into brs-node by tests/sg.mjs. A RowList with a floating focus (the channel's, in
// Arabic) keeps the focused card in view: jumpToRowItem to a row's last card scrolls that row to
// it. brs-engine does so only within the row that already has the focus; a jump that also
// changes rows leaves the new row where it was, so an Arabic row, which starts on its last card
// (the right-most; common.brs, ui_RowsReady), was drawn from its other end, its newest cards off
// the screen. This drops that condition in brs-engine's jump; the channel itself is unchanged.
// (Up and Down jump to a card already in view, so nothing else moves.)
//
// brs-node is pinned (package.json): the step is matched by its exact text, and a bundle without
// it fails the run.
"use strict";
const Module = require("node:module");

const STEP = "n&&o?this.rowScrollOffset[t]=0===i||i<e?0:Math.max(0,i-e+1):n||(i<this.rowScrollOffset[t]?";
const KEPT = "n&&o?this.rowScrollOffset[t]=0===i||i<e?0:Math.max(0,i-e+1):(i<this.rowScrollOffset[t]?";
const compile = Module.prototype._compile;
Module.prototype._compile = function (content, filename) {
  if (filename.endsWith("brs-sg.node.js")) {
    if (!content.includes(STEP)) throw new Error("row_list.cjs: brs-sg.node.js has no floating-focus step to correct (brs-node changed?)");
    content = content.replace(STEP, KEPT);
  }
  return compile.call(this, content, filename);
};
