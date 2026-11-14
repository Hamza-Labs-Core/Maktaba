// The TV build in a real browser (Epic 58 story 58.08, T03 and T04): `pnpm e2e:tv`
// after `pnpm build:tv`. `vite preview --mode tv` serves dist/tv at /tv/ on :5177; every
// /api call is answered by the test (e2e/tv/mockHome.ts), so no home runs. The screens
// are driven with D-pad keys only, at 1920×1080, dark, in English and Arabic.
//
// The report keeps each test's console and network log (attachments), its trace and the
// screenshots: _tv-web.yml uploads test-results for 30 days.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "e2e/tv",
  testMatch: ["**/*.ui.spec.ts"],
  outputDir: "test-results/tv",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI
    ? [["github"], ["html", { outputFolder: "test-results/tv-report", open: "never" }]]
    : "list",
  use: {
    baseURL: "http://localhost:5177",
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
