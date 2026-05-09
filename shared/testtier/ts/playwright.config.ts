// Playwright config for the e2e tier (Story 20.1).
//
// E2E tests live under `tests/e2e/`, named `*.e2e.spec.ts`, and
// drive the compose stack via `make compose-up`. The CI gate in
// .github/workflows/_e2e.yml is responsible for bringing the stack
// up before this config runs.
//
// @ts-expect-error @playwright/test is not yet a workspace dependency (Epic 11).
import { defineConfig, devices } from "@playwright/test";

const SOFT_CAP_MS = 30_000;
const HARD_CAP_MS = SOFT_CAP_MS * 3;

export default defineConfig({
  testDir: "tests/e2e",
  testMatch: ["**/*.e2e.spec.ts"],
  timeout: HARD_CAP_MS,
  expect: { timeout: 5_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: process.env.MAKTABA_E2E_BASE_URL ?? "http://localhost:8080",
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
  // Story 20.1 EC1: a global setup probe with a 60 s timeout would
  // live here once Epic 11 wires it. Until then the compose-up gate
  // covers readiness.
});

// Re-export the soft cap so tests can apply it via test.slow().
export const E2E_SOFT_CAP_MS = SOFT_CAP_MS;
