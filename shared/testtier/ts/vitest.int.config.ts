// Vitest config for the integration tier (Story 20.1).
//
// Integration TS tests run against in-cluster services (Postgres,
// Chroma, the API binary) but stop short of bringing up a browser —
// e2e tests own the Playwright stack.
//
// @ts-expect-error vitest is not yet a workspace dependency (Epic 11).
import { defineConfig } from "vitest/config";

const SOFT_CAP_MS = 5_000;
const HARD_CAP_MS = SOFT_CAP_MS * 3;

export default defineConfig({
  test: {
    include: [
      "tests/integration/**/*.int.spec.ts",
      "tests/integration/**/*.int.spec.tsx",
    ],
    testTimeout: HARD_CAP_MS,
    slowTestThreshold: SOFT_CAP_MS,
    reporters: ["default"],
    // No netguard — integration is allowed to dial.
    setupFiles: [],
  },
});
