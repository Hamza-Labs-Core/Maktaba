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

## A pull request that conflicts with its base

`ci.yml` tests a pull request's merge ref. When the pull request conflicts with its base,
GitHub cannot build that ref and starts no `pull_request` run, so the branch would get no
CI result until the base is merged in again. A second workflow,
[.github/workflows/ci-branch.yml](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/.github/workflows/ci-branch.yml),
covers that case:

- It runs on every push to a branch other than `main` and `release/*`. Its first job,
  `pr` (`tools/ci/branch_head.py`), finds the branch's open pull request into `main` or
  `release/*`.
- No pull request, or a mergeable one: nothing more runs. A mergeable pull request's own
  `pull_request` run tests the merge ref, so there is no double run.
- A pull request that conflicts (or whose mergeability GitHub has not worked out after two
  minutes): it calls `ci.yml` on the branch head itself. The path filters compare the head
  with its merge-base with the pull request's base, not with the base's current tip, so
  the same jobs run as for the pull request, except `pr-body-check` (it reads the pull
  request event). `affected-tests` checks the commits since the merge-base.
- Its checks are named `ci / <job>`, and its rollup is `ci / ci-success`. The required
  `ci-success` check only ever comes from `ci.yml`'s own runs, and a pull request needs no
  conflicts to merge anyway, so its `pull_request` run still gates the merge. The tag guard
  waits only on `ci.yml`'s runs.
- One concurrency group per branch: a newer push cancels the branch's older run. It does
  not share the `pull_request` run's group, where whichever event came second would
  cancel the other.
- No job in `ci.yml` reads a secret. A push event fires only for a push to this
  repository, which needs write access; a fork's pushes run in the fork.

Merging the base in is still needed to merge. When you open a pull request that already
conflicts, re-run the branch's last `CI (branch head)` run, or push a commit that changes a
file, to get a result (an empty commit starts no run: the trigger ignores pushes that change
only the trail, and an empty one changes nothing).
`tools/ci/ci_paths.py which --branch <files>` says which jobs a branch head runs.

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
