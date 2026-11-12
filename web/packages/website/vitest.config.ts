import { defineConfig } from "vitest/config";

// The Worker's, the build's and the deploy script's tests. site.test.ts reads dist/: run
// `npm run build` first.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
