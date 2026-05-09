# `shared/testtier/ts` — TS/Web tier configuration

Story 20.1 deliverable. These configs are the canonical tier
definitions for any TS workspace under the repo (today: `web/`,
later: any other Node-based service).

## Files

| File | Tier | Used by |
|---|---|---|
| `vitest.unit.config.ts` | unit | `pnpm vitest run --config <file>` |
| `vitest.int.config.ts` | integration | `pnpm vitest run --config <file>` |
| `playwright.config.ts` | e2e | `pnpm playwright test` |
| `netguard.ts` | unit setup file | injected via `vitest.unit.config.ts` |

## Conventions

* Unit tests live under `src/` next to the code they cover, named
  `*.unit.spec.ts`. They must not touch the network or filesystem;
  `netguard.ts` patches `fetch` and `node:net.Socket` at vitest setup
  to enforce that.
* Integration tests live in `tests/integration/` and are named
  `*.int.spec.ts`. They may reach in-cluster services but must not
  bring up a browser.
* E2E tests live in `tests/e2e/` and are named `*.e2e.spec.ts`. They
  drive Playwright against the compose stack.

## Soft caps (Story 20.1 AC4)

The configs set `testTimeout` to the **hard cap** (3× the soft cap)
and `slowTestThreshold` to the soft cap so vitest reports each tier
in line with the cross-runtime convention:

| Tier | soft cap | hard cap |
|---|---|---|
| unit | 100 ms | 300 ms |
| integration | 5 s | 15 s |
| e2e | 30 s | 90 s |

These match the values in `shared/testtier/go/tier.go` and
`shared/testtier/py/maktaba_testtier/tiers.py`. If you change any
cap, change all three.

## Status

The `web/` package's `test:unit` script is currently a no-op stub
(Epic 22.1) — vitest itself isn't installed yet because the real web
app lands with Epic 11. The configs here are ready to be wired in by
that epic with no edits other than `pnpm add vitest @playwright/test
-D`.
