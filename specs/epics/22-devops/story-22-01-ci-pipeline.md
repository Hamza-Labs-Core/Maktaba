# Story 22.1 — Continuous integration pipeline

Every commit to a branch and every PR gets the same gated checks. CI
is the merge gate.

## Acceptance criteria

- AC1. The CI workflow has six gates, run in parallel where possible:
  1. `lint` — golangci-lint, ruff, eslint, tsc --noEmit, prettier
     check, gofmt check, mypy strict on pipeline.
  2. `unit` — Epic 20.1 unit tier across all services.
  3. `integration` — Epic 20.4 integration tier with Postgres + ChromaDB
     containers.
  4. `e2e` — Epic 20.5 against a docker-compose stack.
  5. `perf-ci` — Epic 20.7 reduced perf suite.
  6. `build-artifacts` — produces every binary, container image, and
     web bundle as artifacts.
- AC2. PR merge requires all six green; force-merge requires a recorded
  override with a documented reason in the PR body.
- AC3. CI runs on three OS / arch combos for the build gate: linux/
  amd64, linux/arm64, darwin/arm64. Test gates run on linux/amd64
  with darwin/arm64 spot-checks for darwin-only paths.
- AC4. Total wall-clock for a green PR ≤ 20 minutes (Epic 20.1).

## Test cases

- TC1. Gate independence: each gate fails for its own reason with no
  spillover; a `lint` failure does not also report `unit` as failed.
- TC2. Cross-platform build: a Go change that breaks linux/arm64 fails
  the `build-artifacts` gate visibly with the offending arch named.
- TC3. Override: a force-merge without the required PR body section is
  refused by a branch protection rule.

## Edge cases

- EC1. Flaky CI runner — the `flake` quarantine policy from Epic 20.8
  applies; retries are not a substitute for a fix.
- EC2. PR from a fork — secrets are unavailable; `e2e` and `perf-ci`
  skip with a clear "needs maintainer rerun" comment.
- EC3. PR touches only docs — non-doc gates skip with a labeled "docs-
  only" status.
