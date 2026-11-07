// The cloud web app's UI tests in a real browser (Epic 50 story 50.13, T11): `pnpm e2e:cloud`.
// Vite serves web/cloud on :5174 (vite.cloud.config.ts); every /v1 call is answered by the
// test itself (page.route), so no cloud and no home is involved. web-ui.yml runs it in CI.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "cloud/e2e",
  testMatch: ["**/*.ui.spec.ts"],
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: "http://localhost:5174",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "pnpm exec vite --config vite.cloud.config.ts --port 5174 --strictPort",
    url: "http://localhost:5174",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
