# Story 20.3 — Unit test coverage and conventions

Per-language conventions and a numeric coverage floor for business
logic, not for generated code or boilerplate.

## Acceptance criteria

- AC1. Coverage floors (lines): `api/internal/domain` ≥ 85 %,
  `streaming/internal/transcode` ≥ 80 %, `streaming/internal/manifest`
  ≥ 90 %, `pipeline/src/maktaba_pipeline/domain` ≥ 85 %,
  `web/src/lib` ≥ 80 %.
- AC2. Generated code (sqlc, gqlgen, protobuf, GraphQL types) is
  excluded from coverage; the exclude list is checked into
  `.coveragerc` / `coverage.go.yml` / `vitest.config.ts`.
- AC3. Table-driven tests in Go for every public domain function;
  parametrized tests in pytest; each test asserts a single behavior.
- AC4. Mutation testing (`go-mutesting`, `mutmut`, `stryker`) runs
  weekly; surviving mutations on critical paths (auth, hash,
  signed-URL) are fixed within 1 sprint.

## Test cases

- TC1. Coverage gate: a PR that drops coverage below floor for a covered
  package fails the build.
- TC2. Negative space: every public function has at least one error-path
  test; CI lints for missing error tests on functions that return errors.
- TC3. Mutation: the weekly mutation report shows ≤ 5 surviving mutations
  on auth code and 0 on hash code.

## Edge cases

- EC1. New file with 100 % coverage but only happy-path — the error-
  path lint flags missing negative tests.
- EC2. Generated code accidentally counted — the build prints the file
  list being measured for inspection.
- EC3. Coverage flake from `init()` ordering — tests do not rely on
  `init()` side effects; lint forbids `init()` outside `cmd/`.
