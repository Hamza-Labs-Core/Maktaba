// Smoke test for the token builder (Story 17.1 stub).
//
// `make test-tokens` runs this directly via `node`. Mirrors the
// build-tokens.mjs validation pass so a CI signal exists even before
// the real generator (Epic 17) lands. When the generator does land,
// extend this to assert each output file's shape.

import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const builder = join(here, "build-tokens.mjs");

const result = spawnSync(process.execPath, [builder], { encoding: "utf8" });

if (result.status !== 0) {
  console.error(`test-tokens: builder exited ${result.status}`);
  console.error(result.stdout);
  console.error(result.stderr);
  process.exit(1);
}

if (!/validated \d+ token source/.test(result.stdout)) {
  console.error(`test-tokens: unexpected builder output: ${result.stdout}`);
  process.exit(1);
}

console.log("test-tokens: builder stub OK");
