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
  // the install page's tests read the published Docker compose (story 59.11), outside web/
  server: {
    fs: {
      allow: [
        __dirname,
        path.resolve(__dirname, "../cloud/installers/docker"),
        path.resolve(__dirname, "../tools/plugin-sdk"),
        // the Samsung and LG launcher (story 58.09) and the shared compat vectors (58.01)
        path.resolve(__dirname, "../apps/tv/web"),
        path.resolve(__dirname, "../shared/compat"),
      ],
    },
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      "@ds": path.resolve(__dirname, "design-system"),
      "@maktaba/tvplayer": path.resolve(__dirname, "packages/tvplayer/src"),
      "@cloud-shared": path.resolve(__dirname, "cloud/src/shared"),
      "@maktaba/flowchart": path.resolve(__dirname, "../tools/plugin-sdk/js/src/flowchart.ts"),
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: [
      "src/**/*.test.{ts,tsx}",
      "cloud/src/**/*.test.{ts,tsx}",
      "admin/src/**/*.test.{ts,tsx}",
      "design-system/**/*.test.{ts,tsx}",
      "packages/**/*.test.{ts,tsx}",
      // the Samsung and LG launcher (story 58.09): its source is apps/tv/web
      "../apps/tv/web/src/**/*.test.ts",
    ],
    // the developer portal and maktaba.tv have their own vitest (node, over their built site):
    // packages/devportal, packages/website (_website.yml)
    exclude: ["**/node_modules/**", "packages/devportal/**", "packages/website/**"],
    // Half the cores, 2 to 8 (vitest's own default is every core but one). The run is
    // bound by the main process past about 7 workers: 27 forks on a 28-core Mac took
    // 88 s, 7 took 90 s. More only starve each other, and on a runner or machine that
    // is busy with something else each test's waits run out first (#697).
    maxWorkers: Math.max(2, Math.min(8, Math.floor(availableParallelism() / 2))),
  },
});
