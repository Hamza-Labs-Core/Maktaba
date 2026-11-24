// The desktop app's own window frame in a real browser (Epic 13 story 13.10; the quality
// bar in docs/design/desktop/README.md): `pnpm e2e:desktop`. Vite serves the web app on
// :5178; the test stands in for the shell (window.__MAKTABA_FRAME_PREVIEW__, lib/frame.ts)
// and answers every /api call itself, so no shell and no home run. Dark, 1280×800 (the
// window's size, tauri.conf.json), English and Arabic, Windows, GNOME and the Mac.
//
// The references are e2e/desktop/__screenshots__/, taken in CI's Playwright image
// (_desktop-frame.yml): a missing one is written by the run, uploaded, and fails the job
// until it is committed (and listed under "Reference images:" in the pull request).
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "e2e/desktop",
  testMatch: ["**/*.ui.spec.ts"],
  outputDir: "test-results/desktop",
  timeout: 60_000,
  expect: {
    timeout: 10_000,
    toHaveScreenshot: { maxDiffPixelRatio: 0.001, animations: "disabled", scale: "css" },
  },
  snapshotPathTemplate: "{testDir}/__screenshots__/{arg}{ext}",
  updateSnapshots: "missing",
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI
    ? [["github"], ["html", { outputFolder: "test-results/desktop-report", open: "never" }]]
    : "list",
  use: {
    baseURL: "http://localhost:5178",
    trace: "retain-on-failure",
    colorScheme: "dark",
    viewport: { width: 1280, height: 800 },
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        colorScheme: "dark",
        viewport: { width: 1280, height: 800 },
      },
    },
  ],
  webServer: {
    command: "pnpm exec vite --port 5178 --strictPort",
    url: "http://localhost:5178",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
