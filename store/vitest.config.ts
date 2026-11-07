import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

// every *.test.ts of the store: the signer on the shared vectors (signing/), paid plugins
// (commerce/, payments/), the flow check (checks/) and the store's independence (test/).
// The flow check imports the plugin SDK's checker from its source (54.16 C12): its one
// dependency, smol-toml, resolves from the store's own node_modules.
export default defineConfig({
  resolve: {
    alias: { "smol-toml": fileURLToPath(new URL("./node_modules/smol-toml/dist/index.js", import.meta.url)) },
  },
  test: { exclude: ["node_modules/**", "console/**", "build/**"] },
});
