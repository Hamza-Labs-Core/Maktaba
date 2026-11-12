// maktaba.tv's quality bar in a real browser (story 60.04): axe (WCAG 2.2 A and AA), the
// keyboard, focus, target-size, reflow, text-spacing, reduced-motion and language tests, layout
// shift, RTL mirroring, and the visual references. Every page of the built dist/ in both
// languages, served by the Worker under `wrangler dev`, so its headers count.
//
//   npm run build && npm run e2e          the tests (WEBSITE_BASE: a running wrangler dev)
//   npm run e2e:update                    the visual references, in the CI runner's image
//                                         (scripts/e2e-update.sh: Docker, never the host)
//
// The visual references are rendered in mcr.microsoft.com/playwright:v<version>-noble, the
// image the CI job runs in (_website.yml), so a Mac never writes one: on any other machine the
// visual spec skips itself (e2e/visual.spec.ts). Without WEBSITE_BASE, Playwright starts
// wrangler dev itself and writes its log to test-results/worker.log.
import { defineConfig, devices } from "@playwright/test";

const external = process.env.WEBSITE_BASE;
const base = external ?? "http://127.0.0.1:8788";

export default defineConfig({
  testDir: "e2e",
  testMatch: ["**/*.spec.ts"],
  timeout: 90_000,
  expect: {
    timeout: 10_000,
    toHaveScreenshot: { maxDiffPixelRatio: 0.001, animations: "disabled", scale: "css" },
  },
  // pixels, not bytes: e2e/__screenshots__/<page>-<width>-<lang>[-light].png
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}{ext}",
  // a reference that does not exist yet is written (in CI, in the runner's image), and the
  // workflow then fails the job and uploads it to be looked at and committed (_website.yml);
  // a reference that differs is never overwritten
  updateSnapshots: "missing",
  fullyParallel: true,
  workers: process.env.CI ? 4 : undefined,
  retries: 0,
  reporter: [
    [process.env.CI ? "github" : "list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
    ["json", { outputFile: "test-results/results.json" }],
  ],
  outputDir: "test-results/output",
  use: {
    baseURL: base,
    trace: "retain-on-failure",
    colorScheme: "dark",
    locale: "en-US",
    timezoneId: "UTC",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], colorScheme: "dark" } }],
  webServer: external
    ? undefined
    : {
        command:
          "mkdir -p test-results && npx wrangler dev --var CANONICAL_REDIRECTS:off --ip 127.0.0.1 --port 8788 --log-level info > test-results/worker.log 2>&1",
        url: `${base}/`,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
