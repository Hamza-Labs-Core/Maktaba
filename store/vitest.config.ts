// The store's tests import the plugin SDK's flow checker from its source (54.16 C12): its
// one dependency, smol-toml, resolves from the store's own node_modules.
import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: { "smol-toml": fileURLToPath(new URL("./node_modules/smol-toml/dist/index.js", import.meta.url)) },
  },
});
