import { defineConfig } from "vitest/config";

export default defineConfig({
  define: { __DEBUG__: "false" },
  test: {
    include: ["test/**/*.test.ts"],
    environment: "jsdom",
    // the run's log beside the results, kept in the CI report (.github/actions/tv-logs)
    reporters: process.env.CI
      ? ["default", ["junit", { outputFile: "test-results/vitest.xml" }]]
      : ["default"],
  },
});
