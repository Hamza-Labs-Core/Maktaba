# Contributing to Maktaba

## Before you push

CI runs the same `make` targets you run locally. Run them before pushing
so you don't burn a CI cycle on something `make lint` would have caught:

```sh
make lint
make test-unit
```

If your change touches integration boundaries or end-to-end flows, also
run:

```sh
make test-integration   # needs Postgres + Chroma reachable
make test-e2e           # needs the compose stack up
```

The `Makefile` is the contract between developers and CI; see
[story-22-08](specs/epics/22-devops/story-22-08-developer-workflow.md)
for the parity requirement.

## Pull request gates

Every PR must satisfy the [`ci-success`](.github/workflows/ci.yml)
rollup, which bundles six gates:

1. `lint` — golangci-lint, gofmt, ruff, mypy, eslint, tsc, prettier.
2. `unit` — fast in-process tests across all services.
3. `integration` — tests against Postgres + Chroma service containers.
4. `e2e` — tests against the full docker-compose stack.
5. `perf-ci` — reduced perf suite (sub-2-minute fixture).
6. `build-artifacts` — cross-compile for linux/amd64, linux/arm64,
   darwin/arm64.

If you need to merge without all six green (production fire, etc.),
add the `force-merge` label and a `force-merge: <reason>` line to the
PR body. The line is the audit trail; PRs with the label but no
matching reason fail the `pr-body-check` gate.

## Fork PRs

Fork PRs skip `e2e` and `perf-ci` because forked workflows don't have
access to repo secrets. A bot comment links the maintainer-rerun flow.
This is a feature, not a bug — review the diff first, then trigger the
gated runs from a same-repo branch.

## Docs-only PRs

Edits limited to `specs/`, `docs/`, or `*.md` files (and not touching
`Makefile` or `.github/`) skip every heavy gate via `paths-filter`. The
`docs-only` label gets attached automatically so reviewers can see the
gate skip at a glance.
