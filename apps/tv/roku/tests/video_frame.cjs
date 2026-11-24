// Preloaded into brs-node by tests/sg.mjs; it acts only when a run names a picture
// (MAKTABA_VIDEO_FRAME, a PNG or JPEG). brs-engine plays no video: its CLI drops the Video
// node's media messages, so the node stays in state "none", never presents a frame, and the
// screen under the player's overlay is the scene's background. A Roku draws the film's picture
// in the Video node's rectangle, under every node drawn after it. This draws that picture there:
// one still of the film, decoded by the test, at the moment brs-engine's Video node renders
// itself, in its own rectangle, before its children and the nodes after it (the channel's
// subtitle line and overlay), so all of those are drawn over it by brs-engine exactly as the
// channel lays them out. Nothing the channel draws is touched.
//
// The picture shows while the node is the scene's video and has been told to play (and is not
// stopped, finished or failed). brs-engine renders in a worker thread whose console the test
// does not see, so the first picture drawn is recorded in MAKTABA_VIDEO_FRAME + ".drawn".
//
// brs-node is pinned (package.json): its SceneGraph bundle is matched by the exact text of the
// Video node's draw step, and a bundle without that text fails the run instead of drawing black.
"use strict";
const fs = require("node:fs");
const Module = require("node:module");

const FRAME = process.env.MAKTABA_VIDEO_FRAME;
// the Video node's draw step in brs-sg.node.js (Video.renderNodeContent): `n` the draw target
// (a paint pass), `l` the node's rectangle, `s` its opacity, `u` whether it is the scene's video
const STEP = "n&&(this.isDirty=!1,c?n.doDrawClearedRect(l):g&&n.doDrawRotatedRect(l,255,0,[0,0],s))";
const HOOKED = "n&&(this.isDirty=!1,c?n.doDrawClearedRect(l):g&&n.doDrawRotatedRect(l,255,0,[0,0],s),globalThis.__maktabaVideoFrame(this,n,l,s,u))";
// the states in which a Roku shows no picture
const HIDDEN = new Set(["stopped", "finished", "error"]);

if (FRAME) {
  const { Image } = require("canvas");
  const img = new Image();
  img.src = fs.readFileSync(FRAME);
  let drawn = false;
  globalThis.__maktabaVideoFrame = (node, n, l, s, u) => {
    if (!u || !n?.component || l.width <= 0 || l.height <= 0) return;
    if (String(node.getValueJS("control")).toLowerCase() !== "play" || HIDDEN.has(node.getValueJS("state"))) return;
    const ctx = n.component.getContext();
    const x = n.component.x + l.x;
    const y = n.component.y + l.y;
    // the picture fitted to the rectangle, its aspect kept, black around it (as a Roku shows it)
    const k = Math.min(l.width / img.width, l.height / img.height);
    const w = img.width * k;
    const h = img.height * k;
    ctx.save();
    ctx.globalAlpha = s;
    ctx.fillStyle = "#000";
    ctx.fillRect(x, y, l.width, l.height);
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(img, x + (l.width - w) / 2, y + (l.height - h) / 2, w, h);
    ctx.restore();
    n.component.makeDirty?.();
    if (!drawn) {
      drawn = true;
      fs.writeFileSync(`${FRAME}.drawn`, `${img.width}x${img.height} in ${l.width}x${l.height} at ${l.x},${l.y}\n`);
    }
  };
  const compile = Module.prototype._compile;
  Module.prototype._compile = function (content, filename) {
    if (filename.endsWith("brs-sg.node.js")) {
      if (!content.includes(STEP)) throw new Error("video_frame.cjs: brs-sg.node.js has no Video draw step to draw the picture in (brs-node changed?)");
      content = content.replace(STEP, HOOKED);
    }
    return compile.call(this, content, filename);
  };
}
