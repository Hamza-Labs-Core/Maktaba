// brs-node uses source-map-resolve 0.5.3 via nanomatch and snapdragon.
// The fixed decoder is ESM, so the legacy CommonJS wrapper needs its default export.
const { readFileSync, writeFileSync } = require("node:fs");
const { createRequire } = require("node:module");

const requireFromBrs = createRequire(require.resolve("brs-node/package.json"));
const requireFromNanomatch = createRequire(requireFromBrs.resolve("nanomatch"));
const requireFromSnapdragon = createRequire(requireFromNanomatch.resolve("snapdragon"));
const requireFromResolver = createRequire(requireFromSnapdragon.resolve("source-map-resolve"));
const wrapper = requireFromResolver.resolve("./decode-uri-component");
const original = 'require("decode-uri-component")';
const patched = original + ".default";
const source = readFileSync(wrapper, "utf8");

if (!source.includes(patched)) {
  if (!source.includes(original)) {
    throw new Error("The source-map-resolve decoder wrapper has changed");
  }
  writeFileSync(wrapper, source.replace(original, patched));
}
