// The Samsung and LG launcher in a real browser (Epic 58 story 58.09, T04): `pnpm
// e2e:launcher` after `pnpm build:tv` and `pnpm build:launcher`. The launcher's page is
// opened from file://, as a packaged app's is, with a Samsung (Tizen) or LG (webOS) shim;
// its subnet probe, the home's discovery answer, /tv/ and the home's API are answered by the
// test (e2e/launcher/launcher.ui.spec.ts, on top of e2e/tv/mockHome.ts): /tv/ itself is
// dist/tv, served by `vite preview --mode tv` on :5177. No home runs.
//
// The report keeps the screenshots (dark, English and Arabic), the launcher's own log, the
// console and network logs and the traces: _tv-web.yml uploads them for 30 days.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "e2e/launcher",
  testMatch: ["**/*.ui.spec.ts"],
  outputDir: "test-results/launcher",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI
    ? [["github"], ["html", { outputFolder: "test-results/launcher-report", open: "never" }]]
    : "list",
  use: {
    trace: "on",
    colorScheme: "dark",
    viewport: { width: 1920, height: 1080 },
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        colorScheme: "dark",
        viewport: { width: 1920, height: 1080 },
      },
    },
  ],
  webServer: {
    command: "pnpm exec vite preview --mode tv --port 5177 --strictPort",
    url: "http://localhost:5177/tv/",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
