// Vitest config for the unit tier (Story 20.1).
//
// Story 20.1 AC4 sets per-test soft caps; vitest's `testTimeout` and
// `slowTestThreshold` map onto the hard cap (3x soft cap) and the
// soft cap respectively.
//
// This file is consumed once Epic 11 installs vitest as a devDep.
// Until then it documents the intended config.
//
// @ts-expect-error vitest is not yet a workspace dependency (Epic 11).
import { defineConfig } from "vitest/config";

const SOFT_CAP_MS = 100;
const HARD_CAP_MS = SOFT_CAP_MS * 3;

export default defineConfig({
  test: {
    include: ["src/**/*.unit.spec.ts", "src/**/*.unit.spec.tsx"],
    setupFiles: ["../../shared/testtier/ts/netguard.ts"],
    testTimeout: HARD_CAP_MS,
    slowTestThreshold: SOFT_CAP_MS,
    reporters: ["default"],
  },
});
