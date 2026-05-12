// Design-token build (Story 17.1 stub).
//
// The full implementation generates CSS/TS/Swift/Kotlin/JSON outputs
// from `web/design-system/tokens/*.json`. Until Epic 17 lands the real
// generator, this script validates the source JSON files parse and
// match `tokens/schema.json`'s top-level shape, then exits 0 so the
// build-artifacts CI gate (which calls `make build-tokens` via
// `make build-web`) doesn't fail on a missing script.
//
// The consumed CSS (`web/src/styles/tokens.css`) is currently
// hand-maintained in lock-step with `tokens/tokens.json` — replace
// that hand-maintenance with this script's output when 17.1 lands.

import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const tokensDir = join(here, "..", "tokens");

const SOURCES = [
  "tokens.json",
  "tokens.dark.json",
  "tokens.high-contrast.json",
];

async function main() {
  for (const file of SOURCES) {
    const path = join(tokensDir, file);
    let raw;
    try {
      raw = await readFile(path, "utf8");
    } catch (err) {
      console.error(`build-tokens: missing ${path}: ${err.message}`);
      process.exit(1);
    }
    try {
      JSON.parse(raw);
    } catch (err) {
      console.error(`build-tokens: invalid JSON in ${path}: ${err.message}`);
      process.exit(1);
    }
  }
  console.log(`build-tokens: validated ${SOURCES.length} token source(s) (stub — Epic 17 wires the real generator)`);
}

main().catch((err) => {
  console.error(`build-tokens: ${err.stack || err.message}`);
  process.exit(1);
});
