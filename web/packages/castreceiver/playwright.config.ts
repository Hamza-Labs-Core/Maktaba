// The receiver page in Chromium at 1920×1080, dark, English and Arabic (story 58.07, T02):
// the built dist/ served by the Worker under `wrangler dev`, Google's SDK replaced by
// e2e/caf-stub.js and the home by e2e/mockHome.ts (page.route). The console and network logs,
// the screenshots and the traces land in test-results/, which the CI job keeps
// (.github/actions/tv-logs) passed or failed.
//
//   npm run build && npm run e2e
//
// The visual references (e2e/__screenshots__) are rendered in the CI runner's image
// (mcr.microsoft.com/playwright:v<version>-noble, scripts/e2e-update.sh), so a Mac never writes
// one: elsewhere the comparison skips itself (CAST_VISUAL unset).
import { defineConfig, devices } from "@playwright/test";

const port = 8791;
const base = process.env.CAST_BASE ?? `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "e2e",
  testMatch: ["**/*.spec.ts"],
  timeout: 60_000,
  expect: {
    timeout: 10_000,
    toHaveScreenshot: { maxDiffPixelRatio: 0.002, animations: "disabled", scale: "css" },
  },
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}{ext}",
  updateSnapshots: "missing",
  fullyParallel: true,
  workers: process.env.CI ? 2 : undefined,
  retries: 0,
  reporter: [
    [process.env.CI ? "github" : "list"],
    ["html", { open: "never", outputFolder: "test-results/report" }],
    ["json", { outputFile: "test-results/results.json" }],
  ],
  outputDir: "test-results/output",
  use: {
    baseURL: base,
    trace: "on",
    colorScheme: "dark",
    viewport: { width: 1920, height: 1080 },
    timezoneId: "UTC",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1920, height: 1080 },
        colorScheme: "dark",
      },
    },
  ],
  webServer: process.env.CAST_BASE
    ? undefined
    : {
        command: `mkdir -p test-results && npx wrangler dev --ip 127.0.0.1 --port ${port} --log-level info > test-results/worker.log 2>&1`,
        url: `${base}/r/`,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
