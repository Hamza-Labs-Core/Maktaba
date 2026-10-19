# Plan 35.14 — Failure modes and end-to-end tests — implementation

> Implementation plan for [story-35-14-failure-modes-e2e](story-35-14-failure-modes-e2e.md). Design: [design.md](design.md) §14, §17.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Signed fixture releases are produced once by a dedicated workflow and committed. | Real Sigstore bundles (valid and broken) without network in unit tests; the staging identity differs from the production one, so fixtures can never pass production verification. |
| D2 | Test-only identity override via `MAKTABA_UPDATE_REPO` + a build tag `e2e`. | The production binary has no way to accept another identity at runtime except the documented fork setting, which changes repo and identity together. |
| D3 | Power loss simulated with VM resets where available, `kill -9` of the process tree otherwise. | VM reset is the real thing (page cache lost); the cheap variant runs on every PR. |
| D4 | Three-OS E2E nightly, not per PR. | Minutes and runner cost; per-PR coverage is the integration harness. |
| D5 | Every chaos point is a table row shared by the Go test and the VM harness. | One list of failure points, kept in step with the journal steps. |

## 1. Files

**Create**
- `.github/workflows/release-fixtures.yml` — manual workflow: builds tiny fake archives, signs good and
  bad variants (other workflow file, other tag), uploads; a script commits them to
  `shared/testdata/releases/`.
- `shared/testdata/releases/{good-v0.0.1,good-v0.0.2,bad-identity,bad-tag,bad-checksums,bad-manifest,no-hashes,no-tlog,older-replay}/`.
- `cmd/maktaba-server/internal/updater/chaos_test.go` — kill at every journal step.
- `chaos/update_power_loss_test.go`, `chaos/harness/` — VM reset driver (Lima/QEMU), assertions.
- `api/internal/cluster/clustertest/rollout_failures_test.go`.
- `.github/workflows/e2e-cluster-update.yml` — nightly three-OS job.
- `deploy/packaging/node/tests/full-node-convert-dry-run.sh` — conversion dry run (35.8) used by the E2E.

**Modify**
- `.github/workflows/_e2e.yml` — reuse its runner setup for the nightly job.
- `shared/go/releaseverify` — fixtures wiring; `e2e` build tag for the staging identity.
- `docs/testing.md` — how to run chaos and E2E locally.

## 2. Steps

1. Fixture workflow and committed fixtures; `TestVerifyRejects_*`.
2. Downgrade and replay tests on the target selector and node apply.
3. Supervisor kill-at-every-step test with fake children.
4. Rollout failure tests in `clustertest` (offline, platform hold, home lost mid-apply).
5. VM power-loss harness on Linux (Lima), then the macOS variant (Tart/UTM if a runner allows; else
   `kill -9` only, recorded in the runbook).
6. Nightly three-OS E2E: build v0.0.1-e2e/v0.0.2-e2e, install home and nodes, join, update, assert.
   Open point: GitHub-hosted runners cannot multicast to each other; the job uses `--home URL` joins
   over a tailnet, or runs on the owner's lab machines as self-hosted runners.
7. Triage: each failure fixed or listed in the runbook's known issues.

## 3. Tests

As listed in the story; the chaos table is asserted to cover every step name of design §5.3 (a test
fails when a step is added without a chaos row).

## 4. Rollout

Phase E. The unit and integration parts run on every PR from phase B on; the nightly E2E becomes a
required check for release tags (`release.yml` refuses to publish when the last nightly on the tagged
commit failed).
