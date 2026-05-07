# Epic 20 — Testing

> **Status:** spec + plans complete. **Source:** `specs/epics/20-testing/`.
> **Anchors:** [`architecture.md`](../../../specs/architecture.md) §4 (test infrastructure), §9 (cross-service integration).

## Goal

Every layer of Maktaba has a test posture proportional to its risk. The test pyramid is wide at the bottom (unit), substantial in the middle (integration with real Postgres, real FFmpeg, real ChromaDB, fixture media), focused at the top (a few end-to-end smoke flows). CI runs all three on every PR with strict runtime budgets. **Nothing merges red.** Specific test cases for features live in their respective epics; this is the meta-epic for "how we test."

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 20.1 | [Test pyramid & runtime budgets](../../../specs/epics/20-testing/story-20-01-test-pyramid.md) | [plan-20-01](../../../specs/epics/20-testing/plan-20-01-test-pyramid.md) | Three tiers (unit/integration/e2e); Go build tags; pytest marks; vitest configs; per-tier budgets and soft caps. |
| 20.2 | [Fixtures & seed data](../../../specs/epics/20-testing/story-20-02-fixtures-seed-data.md) | [plan-20-02](../../../specs/epics/20-testing/plan-20-02-fixtures-seed-data.md) | Reproducible royalty-free fixtures (≤50 MiB committed); 4K HDR download-on-demand; seeded DB dump (1 k videos / 10 k segments). |
| 20.3 | [Unit coverage & conventions](../../../specs/epics/20-testing/story-20-03-unit-test-coverage.md) | [plan-20-03](../../../specs/epics/20-testing/plan-20-03-unit-test-coverage.md) | Per-package coverage floors (85–90 %); generated code excluded; error-path lint; mutation testing on critical paths. |
| 20.4 | [Integration tests](../../../specs/epics/20-testing/story-20-04-integration-tests.md) | [plan-20-04](../../../specs/epics/20-testing/plan-20-04-integration-tests.md) | Real Postgres / ChromaDB / FFmpeg via testcontainers; cross-service gRPC via bufconn; replay tapes for SaaS mocks. |
| 20.5 | [E2E smoke flows](../../../specs/epics/20-testing/story-20-05-e2e-smoke-flows.md) | [plan-20-05](../../../specs/epics/20-testing/plan-20-05-e2e-smoke-flows.md) | Five golden Playwright flows on dockerized stack; visual diff <0.5 % for RTL; local `make e2e`. |
| 20.6 | [Contract tests](../../../specs/epics/20-testing/story-20-06-contract-tests.md) | [plan-20-06](../../../specs/epics/20-testing/plan-20-06-contract-tests.md) | GraphQL/proto/REST/WS schemas as single sources of truth; drift fails CI; backwards-compat lint with deprecation window. |
| 20.7 | [Performance regression CI](../../../specs/epics/20-testing/story-20-07-perf-regression-ci.md) | [plan-20-07](../../../specs/epics/20-testing/plan-20-07-perf-regression-ci.md) | Reduced perf suite per PR (≤5 m); full nightly; PR comment with delta vs main; auto-quarantine flapping budgets. |
| 20.8 | [Flaky test policy](../../../specs/epics/20-testing/story-20-08-flaky-test-policy.md) | [plan-20-08](../../../specs/epics/20-testing/plan-20-08-flaky-test-policy.md) | Flake registry; auto-quarantine at ≥3 in 7 days; 14-day SLA; retries gated to e2e tier only. |

## Tier conventions

| Tier | Go tag | Python mark | TS suffix | Soft cap (per test) | Total budget |
|---|---|---|---|---|---|
| Unit | `//go:build unit` | `@pytest.mark.unit` | `*.unit.spec.ts` | 100 ms | ≤60 s total |
| Integration | `//go:build integration` | `@pytest.mark.integration` | `*.int.spec.ts` | 5 s | ≤8 m total |
| E2E | `//go:build e2e` | `@pytest.mark.e2e` | `*.e2e.spec.ts` | 30 s | ≤12 m total |

Hard fail at 3× soft cap. Unit/Integration: no retries. E2E: retry once.

## Coverage floors

| Package | Floor |
|---|---|
| `api/internal/domain` | ≥85 % |
| `streaming/internal/transcode` | ≥80 % |
| `streaming/internal/manifest` | ≥90 % |
| `pipeline/src/maktaba_pipeline/domain` | ≥85 % |
| `web/src/lib` | ≥80 % |

Generated code (sqlc, gqlgen, protobuf) excluded.

## Key technical decisions

- **Test pyramid tiers** with build tags / pytest marks / TS file suffixes — discoverability and CI parallelization without test-name conventions alone.
- **Fixture corpus.** ~5 media samples (Arabic lecture, English clip, mixed-language, multi-track mkv, RTL filename) plus 4K HDR (download on demand). Seeded DB: 1 k videos, 10 k segments, load ≤5 s.
- **Contract single sources of truth:** `shared/graphql/schema.graphql`, `shared/proto/*.proto`, `shared/openapi/maktaba.yaml`, `shared/ws/events.ts`. Drift fails CI.
- **Retry policy.** Unit / Integration: zero retries. E2E: retry once. Flake policy: ≥3 in a 7-day window auto-quarantines and files a P2.
- **Perf CI.** Subset of endpoints with `ci_pr=true`, warm-only, reduced trial count. Full suite nightly. PR comment with deltas vs main baseline. >10 % regression blocks merge.

## Files & code paths introduced

- `shared/testtier/go/tier_*.go`, `shared/testtier/ts/vitest.*.config.ts`, `shared/testtier/py/conftest_unit.py`
- `shared/fixtures/samples/`, `shared/fixtures/expected/`, `shared/fixtures/seeded_db.sql.zst`
- `scripts/fixtures-make.sh`, `scripts/fixtures-check.sh`
- `tools/coverage/{floors.yaml,enforce.go,error_path_lint.go,mutation/}`
- `shared/integration/containers/postgres.go`, `shared/integration/containers/chroma.py`, `shared/integration/replay/tape.go`
- `web/e2e/flows/*.e2e.spec.ts`, `web/e2e/helpers/stack.ts`, `deploy/compose/test.yml`, `web/playwright.config.ts`
- `tests/perf/{ci_subset.go,delta_report.go,quarantine.go}`
- `.github/workflows/{test,perf-ci,perf-nightly,flake-*}.yml`
- `tools/flakes/{registry.json,recorder.go,auto_quarantine.go,test_skip_helper.go}`

## Migrations

This epic ships no SQL DDL.

## Dependencies

- Story 20.1 defines tiers; 20.2 provides fixtures; 20.4 consumes them.
- Story 20.6 contract tests depend on shared proto / GraphQL definitions.
- Story 20.7 perf CI depends on Story 18.1's `perf_budgets.yaml`.
- Epic 22 (DevOps) provides CI runners and compose orchestration.

## Out of scope

- Specific feature test content (owned by respective epics).
- Performance budgets and harness — [Epic 18](epic-18-performance.md).
- Security testing — [Epic 23](epic-23-security.md).
- tvOS / Android TV test suites — documented as out-of-scope for v1 in Epic 14.

## See also

- [Epic 18 — Performance](epic-18-performance.md) (perf budgets file consumed by 20.7).
- [Epic 22 — DevOps](epic-22-devops.md) (CI pipeline, runners).
- [Glossary](../glossary.md) — unit test, integration test, e2e test, smoke flow, fixture, contract test, flake, quarantine, replay tape, mutation testing.
