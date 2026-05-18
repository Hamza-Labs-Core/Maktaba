#!/usr/bin/env bash
# tools/coverage-floor.sh — Story 20.3 AC1/TC1 coverage-floor gate.
#
# Collects statement coverage for every Go module in $GO_MODULES and
# for the Python pipeline, then runs the tools/coverage-floor checker
# against tools/coverage-floor/floors.yaml. Exit non-zero if any
# module's coverage is below its documented floor.
#
# The floors are a non-breaking ratchet set at the coverage measured on
# the integrated branch (see floors.yaml). This script is the single
# source of CI vs. local parity (Story 22.8): `make test-coverage`
# calls it locally and the CI Lint gate calls the same make target.
#
# Env (passed from the Makefile, with standalone fallbacks):
#   GO_MODULES   space-separated Go module roots (relative to repo root)
#   PIPELINE_DIR python pipeline dir (default: pipeline)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -n "${GO_MODULES:-}" ]]; then
  # shellcheck disable=SC2206
  modules=( $GO_MODULES )
else
  modules=(api streaming shared/log/go shared/health/go shared/metrics/go \
    shared/tracing/go shared/errrpt/go shared/testtier/go \
    tools/test-budget tools/log-lint)
fi
pipeline_dir="${PIPELINE_DIR:-pipeline}"

covdir="$(mktemp -d)"
trap 'rm -rf "$covdir"' EXIT

build_checker() {
  local bin
  bin="$covdir/coverage-floor"
  ( cd "$REPO_ROOT/tools/coverage-floor" && go build -o "$bin" . )
  echo "$bin"
}

checker="$(build_checker)"
report_args=()
rc=0

# --- Go modules: go test -short -coverprofile per module ---
# The reported percentage is taken from `go tool cover -func` (the
# canonical Go coverage number developers and `go test -cover` print),
# fed to the checker as a --report so the gate value always matches
# standard tooling. (The checker's --profile parser is kept as a
# tested capability but the canonical func-total is authoritative.)
for mod in "${modules[@]}"; do
  if [[ ! -d "$REPO_ROOT/$mod" ]]; then
    continue
  fi
  prof="$covdir/${mod//\//_}.cover"
  echo "==> coverage ($mod)"
  set +e
  ( cd "$REPO_ROOT/$mod" && \
    go test -short -count=1 -coverprofile="$prof" ./... >/dev/null 2>&1 )
  test_rc=$?
  set -e
  if [[ $test_rc -ne 0 ]]; then
    echo "::error::go test failed in $mod (exit $test_rc) — cannot measure coverage"
    rc=1
    continue
  fi
  if [[ ! -s "$prof" ]]; then
    echo "::error::no coverprofile produced for $mod"
    rc=1
    continue
  fi
  pct="$( ( cd "$REPO_ROOT/$mod" && go tool cover -func="$prof" ) \
    | tail -1 | awk '{gsub(/%/,"",$NF); print $NF}' )"
  if [[ -z "$pct" ]]; then
    echo "::error::could not parse coverage total for $mod"
    rc=1
    continue
  fi
  report_args+=( "--report=${mod}=${pct}" )
done

# --- Python pipeline: pytest -m unit --cov ---
# pytest-cov is pulled in ephemerally via `uv run --with` so the
# pipeline lockfile does not have to change for the gate to work.
if [[ -d "$REPO_ROOT/$pipeline_dir" ]]; then
  echo "==> coverage ($pipeline_dir)"
  covxml="$covdir/pipeline.cov.txt"
  set +e
  ( cd "$REPO_ROOT/$pipeline_dir" && \
    uv run --with pytest-cov pytest -m unit \
      --cov=src/maktaba_pipeline --cov-report= -q >/dev/null 2>&1 && \
    uv run --with pytest-cov coverage report --format=total > "$covxml" 2>/dev/null )
  py_rc=$?
  set -e
  if [[ $py_rc -ne 0 || ! -s "$covxml" ]]; then
    echo "::error::pipeline coverage collection failed (exit $py_rc)"
    rc=1
  else
    pct="$(tr -d '[:space:]' < "$covxml")"
    report_args+=( "--report=pipeline=${pct}" )
  fi
fi

if [[ $rc -ne 0 ]]; then
  echo "::error::coverage collection failed — gate cannot verify floors"
  exit "$rc"
fi

"$checker" \
  --floors="$REPO_ROOT/tools/coverage-floor/floors.yaml" \
  "${report_args[@]}"
