#!/usr/bin/env bash
# Checks tools/release/cloud-guard.sh (story 50.06 T03) against a stubbed gh: a passed rehearsal
# lets the deploy through; a failed one refuses it and names the failed scenarios; one still
# running is waited for (and passes when it finishes); none at all is refused after the wait.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
fails=0
ok()  { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stub: RUNS is what `gh run list` returns ("status conclusion id"), RUNS_LATER what every
# call after the first returns; the failed jobs of a run are the file ${JOBS} (one name per line)
mkdir -p "${work}/stubs"
cat >"${work}/stubs/gh" <<'STUB'
#!/usr/bin/env bash
case "$1 $2" in
    "run list")
        n="$(cat "${STATE}/calls" 2>/dev/null || echo 0)"; echo $((n + 1)) >"${STATE}/calls"
        [[ "$*" == *"--workflow cloud-rehearsal.yml"* ]] || { echo "stub: wrong workflow: $*" >&2; exit 1; }
        [[ "$*" == *"--commit abc123"* ]] || { echo "stub: wrong commit: $*" >&2; exit 1; }
        if [ "${n}" -gt 0 ] && [ -n "${RUNS_LATER:-}" ]; then echo "${RUNS_LATER}"; else echo "${RUNS:-}"; fi ;;
    api*) cat "${JOBS}" ;;
    *) echo "stub: $*" >&2; exit 1 ;;
esac
STUB
chmod +x "${work}/stubs/gh"

run() { # run <name> [env...]: the guard's status and output
    local name=$1; shift
    rm -rf "${work:?}/${name:?}"; mkdir -p "${work}/${name}"
    env PATH="${work}/stubs:${PATH}" STATE="${work}/${name}" JOBS="${work}/jobs" SHA=abc123 GITHUB_REPOSITORY=owner/repo \
        POLL_SECONDS=0 WAIT_SECONDS=2 "$@" bash "${here}/cloud-guard.sh" >"${work}/${name}/out" 2>&1
    echo $?
}

if [ "$(run passed RUNS='completed success 7')" = 0 ]; then ok "passed: deploy allowed"; else fail "passed: $(cat "${work}/passed/out")"; fi

printf 'failover-kill-leader\nrestore-pitr\n' >"${work}/jobs"
rc="$(run failed RUNS='completed failure 8')"
if [ "${rc}" != 0 ] && grep -q "failover-kill-leader,restore-pitr" "${work}/failed/out"; then
    ok "failed: refused, naming the scenarios"
else
    fail "failed: rc=${rc} $(cat "${work}/failed/out")"
fi

if [ "$(run waits RUNS='in_progress  9' RUNS_LATER='completed success 9')" = 0 ] && grep -q waiting "${work}/waits/out"; then
    ok "running: waited for, then allowed"
else
    fail "running: $(cat "${work}/waits/out")"
fi

if [ "$(run none RUNS='')" != 0 ] && grep -q "no cloud rehearsal ran" "${work}/none/out"; then
    ok "none: refused after the wait"
else
    fail "none: $(cat "${work}/none/out")"
fi

if [ "${fails}" -ne 0 ]; then echo "${fails} failed"; exit 1; fi
echo "all passed"
