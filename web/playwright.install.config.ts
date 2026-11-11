// maktaba.tv's install pages in a real browser (Epic 59 story 59.14, T04 and T05):
// `pnpm e2e:install`. Vite serves web/packages/install/site on :5176 (vite.install.config.ts);
// the pages fetch nothing, so no server answers anything. web-ui.yml runs it in CI and keeps
// the dark screenshots of every guide in both languages as the run's artifact.
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "packages/install/e2e",
  testMatch: ["**/*.ui.spec.ts"],
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: "http://localhost:5176",
    trace: "retain-on-failure",
    colorScheme: "dark",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], colorScheme: "dark" } }],
  webServer: {
    command: "pnpm exec vite --config vite.install.config.ts --port 5176 --strictPort",
    url: "http://localhost:5176/install/nas/",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
