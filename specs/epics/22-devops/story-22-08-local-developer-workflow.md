# Story 22.8 — Local developer workflow

Day-1 contributor must be able to make a change, see it live, and run
tests inside ≤ 30 minutes.

## Acceptance criteria

- AC1. `make dev` brings up the full stack with live-reload mounts
  for all four services; saving a `.go`, `.py`, or `.tsx` file shows
  the change in ≤ 5 s.
- AC2. `make test` (Epic 20.1 entry point) runs without external
  network and without sudo on a dev laptop.
- AC3. A `CONTRIBUTING.md` gives the canonical workflow; CI runs
  the same exact `make` targets a developer runs locally — no
  divergent CI-only scripts.
- AC4. `pre-commit` config is checked in; running it covers the lint
  gate's quick checks.

## Test cases

- TC1. Cold dev start: a fresh clone + `make dev` boots in ≤ 5
  minutes the first time, ≤ 90 s warm.
- TC2. Live-reload latency: edit a Go file in `api/`, save, refresh
  the browser; the change is visible within 5 s.
- TC3. Parity: `make lint` locally and `make lint` in CI produce the
  same set of pass/fail outcomes on a dirty fixture branch.

## Edge cases

- EC1. Apple Silicon vs. Intel Mac — both paths are tested; doc
  notes which features (MLX) require Apple Silicon.
- EC2. Slow corporate proxy — `make dev` resolves images from a
  configurable mirror; documented.
- EC3. Pre-commit hooks bypassed (`git commit --no-verify`) — CI
  catches the missed checks; merge gate enforces.
