# Story 20.8 — Flaky test policy

Flakes are a debt category; the policy must enforce repair, not
retries-as-default.

## Acceptance criteria

- AC1. A test that fails on a `main` build but passes on retry is
  recorded in a flake registry with the failure log.
- AC2. A test with ≥ 3 recorded flakes in a 7-day window is auto-skipped
  (`t.Skip(reason="quarantined-flake-#issue")`) and a P2 issue is filed.
- AC3. Quarantined tests have a 2-week SLA to fix or delete; SLA breach
  pages the test owner.
- AC4. Retry-on-fail (`--rerun-failed=1`) is allowed only in CI and
  only at the e2e tier; unit and integration tiers fail on first
  failure.

## Test cases

- TC1. Synthetic: introduce a 5 % flake; the registry records 3
  failures in 7 days; the test is auto-skipped and an issue is filed.
- TC2. SLA breach: an open quarantine issue past 14 days fires a
  notification.
- TC3. Retry policy: a unit test failure is not retried; an e2e
  failure may retry once.

## Edge cases

- EC1. Genuine intermittent infra issue (DNS hiccup) — separate
  classification; not counted toward flake budget.
- EC2. Time-zone-dependent test — banned by lint; tests use injected
  clock.
- EC3. Order-dependent test — banned by running the suite in randomized
  order in CI; failures expose the dependency.
