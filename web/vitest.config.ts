import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import { availableParallelism } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Test-only config: jsdom DOM + Testing Library matchers. The Vite build
// (vite.config.ts) is intentionally kept separate so the production
// bundle never pulls in the test harness.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@ds": path.resolve(__dirname, "design-system"),
      "@maktaba/tvplayer": path.resolve(__dirname, "packages/tvplayer/src"),
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: [
      "src/**/*.test.{ts,tsx}",
      "cloud/src/**/*.test.{ts,tsx}",
      "design-system/**/*.test.{ts,tsx}",
      "packages/**/*.test.{ts,tsx}",
    ],
    // the developer portal has its own vitest (node, over its built site): packages/devportal
    exclude: ["**/node_modules/**", "packages/devportal/**"],
    // Half the cores, 2 to 8 (vitest's own default is every core but one). The run is
    // bound by the main process past about 7 workers: 27 forks on a 28-core Mac took
    // 88 s, 7 took 90 s. More only starve each other, and on a runner or machine that
    // is busy with something else each test's waits run out first (#697).
    maxWorkers: Math.max(2, Math.min(8, Math.floor(availableParallelism() / 2))),
  },
});
