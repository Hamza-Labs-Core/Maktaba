import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
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
      "design-system/**/*.test.{ts,tsx}",
      "packages/**/*.test.{ts,tsx}",
    ],
  },
});
