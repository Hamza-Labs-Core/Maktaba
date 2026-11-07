import { defineConfig } from "vitest/config";

// every *.test.ts of the store: the signer on the shared vectors (signing/), paid plugins
// (commerce/, payments/) and the store's independence (test/)
export default defineConfig({
  test: { exclude: ["node_modules/**", "console/**", "build/**"] },
});
