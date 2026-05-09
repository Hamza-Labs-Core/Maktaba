#!/usr/bin/env bash
# tools/test-budget.sh — Story 20.1 wall-clock budget enforcement.
#
# Wraps the Go test-budget program at tools/test-budget/. Two
# top-level entrypoints, picked by the first arg:
#
#   tools/test-budget.sh unit
#       Re-runs `go test -short -race -count=1 -json ./...` for every
#       Go module and pipes the JSON stream into the budget checker.
#       Per-package budget = 30 s, per-test soft cap = 100 ms (Story
#       20.1 AC4).
#
#   tools/test-budget.sh wall <tier> <budget> -- <cmd...>
#       Times an arbitrary command and fails if its wall-clock
#       exceeds <budget>. Used for the integration / e2e / perf-ci
#       tiers where the per-package model doesn't apply.
#
# The script is the single source of CI vs. local parity (Story 22.8
# parity requirement): both call this same wrapper.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Modules to run the unit tier against. The Makefile passes its
# GO_MODULES variable in via the environment so the lint and test
# tier configurations stay in lock-step. When invoked standalone
# (e.g. from a one-off shell), fall back to the canonical list.
if [[ -n "${GO_MODULES:-}" ]]; then
  # shellcheck disable=SC2206
  modules=( $GO_MODULES )
else
  modules=(api streaming shared/log/go shared/testtier/go)
fi

build_budget_bin() {
  # Build the budget enforcer once into a tmp dir so each tier
  # invocation reuses it instead of `go run`-ing every time.
  local bin
  bin="$(mktemp -d)/test-budget"
  ( cd "$REPO_ROOT/tools/test-budget" && go build -o "$bin" . )
  echo "$bin"
}

cmd_unit() {
  local bin
  bin="$(build_budget_bin)"
  local rc=0
  local pkg_budget="${UNIT_PACKAGE_BUDGET:-30s}"
  local soft_cap="${UNIT_PER_TEST_SOFT_CAP:-100ms}"
  for mod in "${modules[@]}"; do
    echo "==> test-budget unit ($mod)"
    # `set +e` while we run the pipeline so a non-zero exit from
    # either side gets surfaced via PIPESTATUS instead of aborting.
    set +e
    ( cd "$REPO_ROOT/$mod" && go test -short -race -count=1 -json ./... ) \
      | "$bin" --mode=json --tier=unit \
          --per-package-budget="$pkg_budget" --per-test-soft-cap="$soft_cap"
    local pipe=("${PIPESTATUS[@]}")
    set -e
    if [[ "${pipe[0]}" != 0 ]]; then
      echo "::error::go test failed in $mod (exit ${pipe[0]})"
      rc=1
    fi
    if [[ "${pipe[1]}" != 0 ]]; then
      echo "::error::test-budget reported breach in $mod (exit ${pipe[1]})"
      rc=1
    fi
  done
  return "$rc"
}

cmd_wall() {
  local tier="$1"
  local budget="$2"
  shift 2
  if [[ "${1:-}" != "--" ]]; then
    echo "usage: $0 wall <tier> <budget> -- <cmd> [args...]" >&2
    return 2
  fi
  shift
  local bin
  bin="$(build_budget_bin)"
  "$bin" --mode=wall --tier="$tier" --budget="$budget" -- "$@"
}

case "${1:-}" in
  unit)         shift; cmd_unit "$@" ;;
  wall)         shift; cmd_wall "$@" ;;
  -h|--help|"") cat <<EOF
usage:
  $0 unit
      Run every Go module's unit tier under the test-budget enforcer.
  $0 wall <tier> <budget> -- <cmd...>
      Time <cmd...> and fail if wall-clock exceeds <budget>.
EOF
                exit "$([[ -z "${1:-}" ]] && echo 2 || echo 0)"
                ;;
  *)            echo "unknown subcommand: $1" >&2; exit 2 ;;
esac
