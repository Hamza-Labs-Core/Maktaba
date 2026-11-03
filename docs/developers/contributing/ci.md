# CI

CI is GitHub Actions. The main workflow is
[.github/workflows/ci.yml](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/.github/workflows/ci.yml).
A pull request runs only the jobs its changed files need, and one rollup job,
`ci-success`, is the merge gate.

## How jobs are picked

The first job, `changes`, reads the path filters in
[.github/ci-paths.yml](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/.github/ci-paths.yml)
with `dorny/paths-filter`. Each filter is a list of globs; it is true when any changed
file matches one of them. Each gated job runs only when its filter is true.

- A filter lists everything its job really builds or tests: the job's sources, files
  its tests read outside them, its workflow file, the `Makefile` when it runs `make`, and
  `ci.yml` itself (a change there can change any job).
- Shared groups (keys starting with `_`, such as `_go`, `_pipeline`, `_web`) are YAML
  anchors the filters reuse.
- On a push to `main` or `release/*`, every filter is true, so `main` and release tags
  always get the full run.
- `tools/ci/ci_paths.py` checks the filters on every pull request (in the lint job):
  every job is gated by a filter or marked always-run, every filter is used, and every
  tracked file is read by some filter or listed as built by no job. If you add a file
  that a job builds or tests, add it to that job's filter.

So a change to `pipeline/` runs the pipeline lint and unit jobs, not the Apple or
Android builds; a change to `apps/android/` runs the Android jobs, not the Go tests.

## The jobs

| Job | What it covers |
|---|---|
| `lint` | golangci-lint, gofmt, ruff, mypy, eslint, tsc, prettier, migration and log lint, and repository checks |
| `unit` | the unit tier for Go, the pipeline, the web app and design tokens |
| `integration` | tests against Postgres and Chroma service containers |
| `replica`, `e2e`, `media-e2e`, `perf-ci` | replica, end-to-end, media and performance suites against real stacks |
| `build-artifacts`, `node-packaging`, `launcher` | cross-compiled binaries, release node assets, the launcher |
| `mac-app`, `win-app`, `linux-app` | the desktop server apps |
| `apple-apps`, `android-apps`, `android-tv`, `desktop-player` | the native apps, their tests and UI tests |
| `devportal` | the maktaba.dev developer portal build and its tests |
| `pr-body-check` | the pull request body, including any force-merge reason |
| `affected-tests` | changed code has its tests changed (see [Running the tests](tests.md)) |

Each job calls a reusable workflow (`_lint.yml`, `_unit.yml` and so on) that runs the
same `make` targets you run locally. The cloud relay has its own workflow, `relay.yml`.

## The gate: ci-success

`ci-success` is the only status check branch protection requires. It needs every gate
and runs even when some were skipped:

- a job **skipped** by its path filter counts as passed;
- a job that **failed** or was **cancelled** fails `ci-success`.

Adding a new gate means adding it to `ci-success.needs`; branch protection never
changes.

## What must pass

Before merging, `ci-success` must be green. In practice:

- every job the change selected must pass; jobs for unrelated code do not run and do
  not hold up the merge;
- `affected-tests` must pass, or a commit must carry a `No-Test-Change: <reason>`
  trailer;
- CodeQL code scanning reports on the pull request too; new high-severity alerts
  fail it.

### Force-merge

In an emergency, the `force-merge` label with a `force-merge: <reason>` line in the PR
body lets a PR merge without every gate green. The line is the audit trail:
`pr-body-check` fails a PR that has the label but no reason.

### Fork pull requests

Forked workflows have no access to repository secrets, so fork PRs skip the gated
suites that need them. A bot comment explains this; a maintainer reviews the diff and
reruns those gates from a branch in the main repository before merging.

## Coverage map

After CI on each push to `main`, `coverage-map.yml` measures which tests execute which
files and publishes an artifact. The affected-tests gate uses it to find a file's tests.

## Reproducing a CI failure locally

Run the job's `make` target: `make lint`, `make test-unit`, `make test-integration`,
`make test-e2e`, `make perf-ci`. If CI and your machine disagree, that is a parity bug
worth an issue.
