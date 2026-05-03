# Epic 20 — Testing

**Goal.** Every layer of Maktaba has a test posture proportional to its
risk. The test pyramid is wide at the bottom (unit), substantial in the
middle (integration with real Postgres, real FFmpeg, fixture media),
focused at the top (a few end-to-end smoke flows). CI runs all three on
every PR; nothing merges red.

This epic defines what the test suite looks like, what it covers, what
fixtures it uses, and how flakes are managed. Specific test cases for
features live in their respective epics; this is the meta-epic for "how
we test."

## Stories

- [Story 20.1 — Test pyramid and runtime budgets](story-20-01-test-pyramid.md)
- [Story 20.2 — Fixtures and seed data](story-20-02-fixtures-seed-data.md)
- [Story 20.3 — Unit test coverage and conventions](story-20-03-unit-test-coverage.md)
- [Story 20.4 — Integration tests with real backends](story-20-04-integration-tests.md)
- [Story 20.5 — End-to-end smoke flows](story-20-05-e2e-smoke-flows.md)
- [Story 20.6 — Contract tests for service boundaries](story-20-06-contract-tests.md)
- [Story 20.7 — Performance regression tests in CI](story-20-07-perf-regression-ci.md)
- [Story 20.8 — Flaky test policy](story-20-08-flaky-test-policy.md)
