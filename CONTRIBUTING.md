# Contributing to Maktaba

This is the canonical day-1 setup and day-N inner loop for working on
Maktaba. CI runs the **same `make` targets** you run locally — no
divergent CI-only scripts (Story 22.8 AC3). If `make lint` and
`make test` are green on your laptop, the lint and unit gates in CI
will be green too.

If you only have one minute, run:

```sh
make prereqs && make dev && make test
```

That installs nothing on its own; it tells you what's missing,
brings up the live-reload stack, and runs the unit tier.

---

## Prerequisites

Run `make prereqs` to verify your host (it lists each tool with a ✓ or
✗ and what version was found). The required set:

| Tool | Min version | Why |
|---|---|---|
| Docker | 24+ | container runtime for the dev stack |
| Docker Compose | v2.27+ | needed for healthchecks + named profiles |
| Git | 2.40+ | working tree checks rely on `git diff --name-only` |
| Go | 1.23+ | `api/` and `streaming/` build |
| uv | 0.4+ | `pipeline/` deps (no separate venv to manage) |
| pnpm | 9+ | `web/` deps |
| Node | 20+ | runs the web frontend tooling |

Recommended (sharper feedback, not strictly required):
`pre-commit`, `golangci-lint`, `jq`, `shellcheck`.

### Apple Silicon vs. Intel (EC1)

Both work. Container images for postgres, chroma, golang, python, and
node all have `linux/arm64` and `linux/amd64` variants and Compose
picks the right one automatically. The MLX-backed transcription path
(Epic 4) requires Apple Silicon; on Intel the pipeline transparently
falls back to whisper.cpp.

### Behind a corporate proxy (EC2)

Two knobs in `.env` (copy from [`.env.example`](.env.example)):

- `MAKTABA_REGISTRY_MIRROR` — point Docker at an internal mirror so
  `make dev` doesn't time out pulling `golang:1.23-alpine` from Docker
  Hub. Apply via your daemon config (`registry-mirrors` in
  `~/.docker/daemon.json`).
- Standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` — respected by
  `go`, `uv`, `pnpm`, and `curl` when set in the shell or `.env`.

---

## First run (≤ 30 minutes from clone)

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba
cp .env.example .env             # tweak if you need non-default ports
make prereqs                     # verify host has docker/go/uv/pnpm/node
make dev                         # builds dev images + brings up the stack
make test                        # unit tier; no network, no sudo
```

Expected timing on a representative laptop:

| Phase | Cold | Warm |
|---|---|---|
| `make dev` (image pull + build) | ≤ 5 min | ≤ 90 s |
| `make test` (unit tier) | ≤ 60 s | ≤ 30 s |

If `make dev` exceeds five minutes on a cold cache, see
[Troubleshooting](#troubleshooting) below.

---

## The inner loop

```sh
make dev          # one-time per session
# edit a .go / .py / .tsx file …
# … see the change at https://localhost:5173 (web) or :8080 (api)
make lint         # before pushing — same as CI
make test         # before pushing — same as CI
```

### Live-reload SLAs (Story 22.8 AC1)

| Edit | Reload mechanism | Latency |
|---|---|---|
| `.go` in `api/` or `streaming/` | `air` rebuilds + re-execs the binary | ≤ 5 s |
| `.py` in `pipeline/src/` | `watchmedo auto-restart --signal SIGTERM` | ≤ 5 s |
| `.ts` / `.tsx` in `web/src/` | Vite HMR over websocket | ≤ 1 s |

The current api/streaming binaries are stubs (Stories 07/08 add the
real serve loops); air will still rebuild + re-exec on every save, you
just won't see a long-running process between rebuilds. The pipeline
stub loops with `time.sleep` so live-reload visibly tears it down and
brings it back.

### Service ports (dev stack)

| Service | URL |
|---|---|
| api | http://localhost:8080 |
| streaming | http://localhost:8081 |
| web | http://localhost:5173 |
| postgres | `postgres://maktaba:maktaba@localhost:5432/maktaba` |
| chroma | http://localhost:8000 |

---

## `make` cheat sheet

`make help` prints the live list grouped by section. The headline
targets:

| Target | What it does |
|---|---|
| `make prereqs` | Check host for required tools |
| `make dev` | Bring up the live-reload stack |
| `make dev-down` | Stop the stack, keep volumes |
| `make dev-clean` | Stop the stack and wipe volumes |
| `make dev-logs` | Tail logs from every service |
| `make lint` | Run every linter (CI gate 1) |
| `make test` | Unit tier — no network, no sudo |
| `make test-integration` | Integration tier (needs services up) |
| `make test-e2e` | End-to-end tier (needs the compose stack) |
| `make build` | Reproducible build for the host (Story 22.2) |
| `make format` | Auto-format every language in place |
| `make migrate` | Apply DB migrations against `$DATABASE_URL` |
| `make compose-up` | Bring up the **production** compose stack (no live reload — use `make dev` for the inner loop). |
| `make compose-mac` | Same as `compose-up`, plus the Mac MLX/FFmpeg overlay (Story 22.3). |

CI's [`_lint.yml`](.github/workflows/_lint.yml),
[`_unit.yml`](.github/workflows/_unit.yml), and friends invoke these
exact targets — that's the parity guarantee.

---

## Pre-commit hooks (Story 22.8 AC4)

Install once after cloning:

```sh
pre-commit install
```

The config at [`.pre-commit-config.yaml`](.pre-commit-config.yaml)
runs the cheap subset of `make lint` on staged files only:

- gofmt, go-vet, go-mod-tidy
- ruff + ruff-format on `pipeline/`
- prettier on `web/`
- trailing whitespace, EOF newline, JSON/YAML/TOML well-formedness
- migration-lint for any change under `shared/db/migrations/`

`golangci-lint`, `mypy`, `eslint`, and `tsc` stay in CI — too slow for
a per-commit hook, but the lint gate in CI catches anything they would.

### Bypassing hooks (`--no-verify`) is allowed (EC3)

You can run `git commit --no-verify` locally; CI's lint gate still has
to pass before merge, so the merge gate is the safety net. Don't
disable hooks globally — they exist to make the inner loop faster, not
to gatekeep.

---

## Changed code needs its tests changed (affected-tests)

The rule: a push that changes app or service code also changes the tests
that cover that code. It exists because PR #469 changed the Apple Player's
sign-in and the Mac UI tests that tap it were left stale.

Install the pre-push hook once (it covers every worktree of the clone, and
leaves the pre-commit hooks alone):

```sh
tools/affected-tests/install-hook.sh
```

On `git push` it runs [`tools/affected-tests`](tools/affected-tests/main.go)
on your branch against `origin/main`. It prints the affected tests by platform,
then blocks the push in two cases:

- a changed source file has **none of its tests changed**, or
- a test still uses a string or accessibility identifier that **no source has
  any more**. That test is stale and will fail.

A UI change also needs a change on each test platform whose tests use the
identifier or phrase it touched. Updating the iPhone UITests does not cover
the Mac UI tests.

When the push passes, the hook builds and runs the affected tests that are
safe to run locally: Go (`-short`), Python, web (vitest), Android/JVM unit
tests and MaktabaKit. Apple UI test targets are **only compiled**
(`xcodebuild build-for-testing`, `CODE_SIGNING_ALLOWED=NO`). No UI test and
no simulator ever runs from the hook. CI runs those.

- When no test change is right, add a trailer to any commit in the push:
  `No-Test-Change: <why>`.
- To run the gate without the tests: `AFFECTED_TESTS_NO_RUN=1 git push`.
- To skip the hook once: `git push --no-verify`. CI's `affected-tests` job
  applies the same gate to the pull request, and it honours the same trailer.
- To ask without pushing: `cd tools/affected-tests && go run . list` (or
  `gate`, or `check`; `--range a...b`, `--json`).

**How it finds a file's tests.** It uses three sources, and the web adds a
fourth:

1. **The coverage map.** After CI on each push to `main`,
   [`coverage-map.yml`](.github/workflows/coverage-map.yml) measures which
   tests execute which files and publishes the artifact `affected-tests-map`.
   The tool fetches it with `gh` and caches it for 6 hours in
   `.git/affected-tests/`. Without `gh`, or with no map yet, it works from
   the searches alone. Granularity by platform:

   | Platform                         | Measured as                                                                                                                                   |
   | -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
   | Go                               | Per package. Each package's `-short` tests run with `-coverpkg` over the whole module, so tests in other packages are found too.              |
   | Python pipeline                  | Per test (pytest-cov `--cov-context=test`; import-time lines are left out).                                                                   |
   | Web                              | Per test file. The static import graph, the same one `vitest related` walks. It is read live, with no CI step.                                |
   | MaktabaKit                       | Per test target (xccov).                                                                                                                      |
   | Mac Player                       | Per test target (xccov over the Mac tests and Mac UI tests run).                                                                              |
   | Android Player/Remote            | Per module (JaCoCo over each module's JVM unit tests, that module's own classes).                                                             |
   | iPhone/iPad UITests, TV, desktop | Not measured: the iPhone run already takes most of an hour. Found by the UI search and by name.                                               |

2. **The UI search.** For a changed UI file it collects the accessibility
   identifiers (`.accessibilityIdentifier`, Compose `testTag`, web
   `data-testid`) and the user-visible strings on the changed lines. It
   resolves keyed strings through the string tables: `words.text(.byCode)`
   becomes "With a code", and `t("key")` becomes its text in `en.json`.
   Then it looks for those strings in the tests.
3. **Naming.** `foo.go` → `foo_test.go` (and the package's other tests),
   `Foo.swift` → `FooTests.swift`, a Player screen folder `Home/` →
   `PlayerHomeUITests.swift`, `foo.py` → `test_foo.py`, `Foo.kt` →
   `FooTest.kt`, `foo.ts` → `foo.test.ts`, `x.sh` → `x-test.sh`.

A changed source file with no test found by any source passes when some test
of its project changed. Rust files count their own `#[cfg(test)]` module.

---

## Troubleshooting

**`make dev` is taking forever on a cold cache.**
First-run image pulls dominate. Watch progress with:

```sh
docker compose -f deploy/compose/docker-compose.yml \
               -f deploy/compose/docker-compose.dev.yml \
               pull
```

If a specific image is slow, set `MAKTABA_REGISTRY_MIRROR` per the
[Behind a corporate proxy](#behind-a-corporate-proxy-ec2) section.

**A dev image won't rebuild after I edited its Dockerfile.dev.**
Force a rebuild:

```sh
make dev-build
```

**The api/streaming binary keeps exiting.**
Expected today — the `main.go` for both is a Story 22.1 stub that
prints a banner and exits. Air still rebuilds on save and you'll see
the new banner in `make dev-logs`. Real serve loops land in Epics 7
and 8.

**Postgres data is wedged after a schema experiment.**
Wipe volumes:

```sh
make dev-clean && make dev
```

**Vite HMR isn't reaching my browser.**
Make sure `VITE_HMR_HOST` in `.env` matches the hostname your browser
sees. The default `localhost` works for the Compose port mapping; if
you proxy through a different hostname, set it to that.

**CI passes but `make lint` fails locally (or vice-versa).**
That's a parity bug — please file an issue with the diff and tag
`devops`. Story 22.8's whole point is that this never happens.

---

## Pull request gates

Every PR must satisfy the
[`ci-success`](.github/workflows/ci.yml) rollup, which bundles six
gates from [Story 22.1](specs/epics/22-devops/story-22-01-ci-pipeline.md):

1. `lint` — golangci-lint, gofmt, ruff, mypy, eslint, tsc, prettier.
2. `unit` — fast in-process tests across all services.
3. `integration` — tests against Postgres + Chroma service containers.
4. `e2e` — tests against the full docker-compose stack.
5. `perf-ci` — reduced perf suite (sub-2-minute fixture).
6. `build-artifacts` — cross-compile for linux/amd64, linux/arm64,
   darwin/arm64.

It also holds `affected-tests`: changed code needs its tests changed, or a
`No-Test-Change: <reason>` trailer. See
[Changed code needs its tests changed](#changed-code-needs-its-tests-changed-affected-tests).

If you need to merge without all six green (production fire, etc.),
add the `force-merge` label and a `force-merge: <reason>` line to the
PR body. The line is the audit trail; PRs with the label but no
matching reason fail the `pr-body-check` gate.

### Fork PRs

Fork PRs skip `e2e` and `perf-ci` because forked workflows don't have
access to repo secrets. A bot comment links the maintainer-rerun flow.
This is a feature, not a bug — review the diff first, then trigger the
gated runs from a same-repo branch.

### Docs-only PRs

Edits limited to `specs/`, `docs/`, or `*.md` files (and not touching
`Makefile` or `.github/`) skip every heavy gate via `paths-filter`. The
`docs-only` label gets attached automatically so reviewers can see the
gate skip at a glance.
