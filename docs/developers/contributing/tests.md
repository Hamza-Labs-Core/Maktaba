# Running the tests

Every suite has a `make` target, and CI calls the same targets. Run `make test` before
you push; it runs the unit tier for Go, Python and the web app.

## Tiers

| Tier | What runs | I/O allowed | Make target |
|---|---|---|---|
| unit | pure logic | none: sockets are blocked, files only in temp dirs | `make test-unit` (or `make test`) |
| integration | real Postgres, Chroma, FFmpeg, fixture media | in-cluster services | `make test-integration` |
| e2e | Playwright and pytest against the compose stack | full stack | `make test-e2e` |
| media e2e | real FFmpeg and Postgres | full stack | `make test-media-e2e` |
| perf-ci | reduced performance suite | full stack | `make perf-ci` |

Each tier has a time budget, and each test a soft cap (warns) and a hard cap of three
times the soft cap (fails). The full rules are in
[docs/testing.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/testing.md).

## Go

```sh
make test-unit-go          # every Go module, -short, under -race, with budgets
cd api && go test -short ./...           # one module, quickly
cd api && go test -short ./internal/handlers/...   # one area
```

- Unit tests are the default and run under `go test -short`.
- Integration tests carry `//go:build integration`. Tests that need Postgres read
  `MAKTABA_TEST_PG_URL` and create, migrate and drop their own scratch database.
- The cloud relay has its own targets: `make relay-test` (and `make relay-ci` for lint,
  test and build).

## Pipeline (Python, via uv)

```sh
make test-unit-py                        # pytest -m unit
cd pipeline && uv run --extra dev pytest -m unit
cd pipeline && uv run --extra dev ruff check . && uv run --extra dev mypy
```

Mark tests with `@pytest.mark.unit`, `@pytest.mark.integration` or `@pytest.mark.e2e`.
`mypy --strict` covers `tests/` too, so test functions need return annotations
(`-> None`). Run uv commands one at a time; parallel runs can race on the virtualenv.

## Web (vitest)

```sh
make test-unit-web                       # pnpm -C web run test:unit
cd web && pnpm install && pnpm test:unit
cd web && pnpm exec vitest related src/path/to/file.tsx   # tests for one file
```

`make lint-web` runs eslint, `tsc --noEmit` and prettier. The packages in
`web/packages/` (such as `devportal`) have their own tests.

## Native apps

- **Apple:** `swift test` in `apps/apple/MaktabaKit/` runs the kit's tests without a
  simulator. `xcodebuild test -scheme "Maktaba Player Mac"` runs the Mac Player's hosted
  tests. The iPhone, iPad and Mac UI test schemes run in CI; avoid running UI tests on a
  shared machine, as they take over the screen.
- **Android:** from `apps/android/`:

```sh
./gradlew :client:test
./gradlew :core:testDebugUnitTest
./gradlew :player:testDebugUnitTest :remote:testDebugUnitTest
./gradlew :player:verifyRoborazziDebug   # screenshot tests against references
```

- **TV:** `make tv-build-ios` and `make tv-build-android` build the TV apps.
- **Design tokens:** `make test-tokens` fails on stale generated `Tokens.swift` or
  `Tokens.kt`, or text under 4.5:1 contrast.

## The affected-tests gate

A push that changes app or service code must also change the tests that cover it.
`tools/affected-tests` enforces this in a pre-push hook and in CI.

Install the hook once per clone:

```sh
tools/affected-tests/install-hook.sh
```

On `git push` it compares your branch with `origin/main`, prints the affected tests by
platform, and blocks the push when:

- a changed source file has none of its tests changed, or
- a test still uses a string or accessibility identifier that no source has any more
  (the test is stale and will fail).

A UI change needs a test change on each platform whose tests use the identifier or
string it touched. When the gate passes, the hook runs the affected tests that are safe
locally (Go `-short`, Python, vitest, Android JVM, MaktabaKit). Apple UI test targets are
only compiled; no simulator runs from the hook.

It finds a file's tests from a coverage map published by CI on `main`, a search of UI
identifiers and strings (resolved through the string tables), and naming conventions
(`foo.go` to `foo_test.go`, `foo.ts` to `foo.test.ts`, `Foo.kt` to `FooTest.kt`).

Options:

- No test change is right: add `No-Test-Change: <why>` as a trailer to any commit in
  the push.
- Gate only, run no tests: `AFFECTED_TESTS_NO_RUN=1 git push`.
- Ask without pushing: `cd tools/affected-tests && go run . list` (or `gate`, `check`;
  `--range a...b`, `--json`).
- `git push --no-verify` skips the hook, but CI's `affected-tests` job applies the same
  gate to the pull request.

A failing test after your change is a stale test to fix in the same change, not a flake.
