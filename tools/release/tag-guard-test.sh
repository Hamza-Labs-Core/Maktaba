#!/usr/bin/env bash
# Checks tools/release/tag-guard.sh against a scratch repository and a stubbed gh: a tag
# above the newest release on main passes (and is the latest); one on main that does not
# contain the newest release's commit is refused; a re-run of a published tag passes as
# published; a maintenance release passes from a release/* branch only, never the latest;
# a pre-release of a version already out is refused; CI is judged by its jobs (only the
# iPhone/iPad Player UI shards and ci-success may fail; a cancelled run or job blocks; a run
# in progress is waited for). Needs git, go and bash; touches
# nothing outside its temporary directory.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# Offline, with an empty module cache: the guard's `go run ./cmd/release-order` must build
# from the checkout alone. It once imported releaseverify (Sigstore, gRPC, protobuf: 40
# modules from proxy.golang.org), and one broken download failed this test on CI
# (run 41476114340) and would have failed a release's guard the same way.
export GOPROXY=off GOFLAGS=-mod=readonly GOMODCACHE="${work}/gomodcache"

fails=0
ok()  { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stubbed gh: the published releases (PUBLISHED, one tag per line), the CI runs (CI_RUNS:
# "status conclusion id" per line, default one passed run; CI_RUNS_LATER, when set, is what
# every call after the first returns: a run that finishes while the guard waits), a CI run's
# jobs (the file ${JOBS_DIR}/<id>: "conclusion<TAB>name" per line), the update rehearsal's
# runs with the commit's head_sha (REHEARSAL: "status conclusion [id event]" per line, default
# one passed push run) and its workflow_run runs of main (REHEARSAL_MAIN: "status conclusion id
# title" per line, default none)
mkdir -p "${work}/stubs" "${work}/jobs"
cat >"${work}/stubs/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
    "release list") printf '%s\n' "${PUBLISHED}" ;;
    "run list")
        if [[ "$*" == *cve-gate-nightly* && "$*" == *"--event schedule"* ]]; then
            printf '%s\n' "${CVE_NIGHTLY_SCHEDULE:-[]}"
        elif [[ "$*" == *cve-gate-nightly* && "$*" == *"--event workflow_dispatch"* ]]; then
            printf '%s\n' "${CVE_NIGHTLY_DISPATCH:-[]}"
        elif [[ "$*" == *update-rehearsal* && "$*" == *"--event workflow_run"* ]]; then
            [ -z "${REHEARSAL_MAIN:-}" ] || printf '%s\n' "${REHEARSAL_MAIN}"
        elif [[ "$*" == *update-rehearsal* ]]; then
            r="${REHEARSAL-completed success}"; [ -z "${r}" ] || awk 'NF == 2 { $0 = $0 " 900 push" } { print }' <<<"${r}"
        else
            n="$(cat "${JOBS_DIR}/.calls" 2>/dev/null || echo 0)"; echo $((n + 1)) >"${JOBS_DIR}/.calls"
            if [ "${n}" -gt 0 ] && [ -n "${CI_RUNS_LATER:-}" ]; then echo "${CI_RUNS_LATER}"; else echo "${CI_RUNS-completed success 1}"; fi
        fi ;;
    api*)
        [[ "$2" =~ ^repos/owner/repo/actions/runs/([0-9]+)/jobs ]] || { echo "gh stub: $*" >&2; exit 1; }
        [[ "$*" == *--paginate* ]] || { echo "gh stub: jobs without --paginate" >&2; exit 1; }
        cat "${JOBS_DIR}/${BASH_REMATCH[1]}" ;;
    *) echo "gh stub: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${work}/stubs/gh"

# the repository: main = v0.3.0 -> v0.3.1, and a side commit merged into main that does not
# contain v0.3.1; release/0.2 from before v0.3.0; an orphan commit on no branch
git init -q --bare "${work}/origin.git"
git clone -q "${work}/origin.git" "${work}/repo" 2>/dev/null
cd "${work}/repo"
git config user.email t@example.com
git config user.name test
git config commit.gpgsign false
git config tag.gpgsign false
commit() { echo "$1" >"file-$1"; git add "file-$1"; git commit -q -m "$1"; git rev-parse HEAD; }
git symbolic-ref HEAD refs/heads/main # whatever init.defaultBranch says
c0="$(commit base)"
c1="$(commit v0.3.0)"; git tag v0.3.0
c2="$(commit v0.3.1)"; git tag v0.3.1
git checkout -q -b side "${c1}"
s1="$(commit side)"
git checkout -q main
git merge -q --no-edit side >/dev/null
c3="$(commit next)"
git checkout -q -b release/0.2 "${c0}"
m1="$(commit hotfix)"
git checkout -q --orphan lost
o1="$(commit orphan)"
git checkout -q main
git push -q origin main release/0.2 --tags 2>/dev/null

# guard TAG SHA [ALLOW]: runs the guard; its outputs in ${work}/out, its log in ${work}/log
guard() {
    : >"${work}/out"
    rm -f "${work}/jobs/.calls"
    PATH="${work}/stubs:${PATH}" TAG="$1" SHA="$2" ALLOW_TAG="${3:-}" GITHUB_OUTPUT="${work}/out" \
        PUBLISHED=$'v0.2.0\nv0.3.0\nv0.3.1' CI_WAIT_SECONDS=5 CI_POLL_SECONDS=1 \
        GITHUB_REPOSITORY=owner/repo JOBS_DIR="${work}/jobs" \
        bash "${here}/tag-guard.sh" >"${work}/log" 2>&1
}
out() { sed -n "s/^$1=//p" "${work}/out"; }
expect_pass() { # expect_pass NAME TAG SHA ALLOW published latest maintenance
    if guard "$2" "$3" "$4" && [ "$(out published)" = "$5" ] && [ "$(out latest)" = "$6" ] &&
        [ "$(out maintenance)" = "$7" ]; then
        ok "$1"
    else
        fail "$1: $(cat "${work}/log") / $(tr '\n' ' ' <"${work}/out")"
    fi
}
expect_refused() { # expect_refused NAME TAG SHA ALLOW WORDS
    if guard "$2" "$3" "$4"; then
        fail "$1: passed"
    elif grep -q "$5" "${work}/log"; then
        ok "$1"
    else
        fail "$1: refused, but not for '$5': $(cat "${work}/log")"
    fi
}

expect_pass "a tag above the newest release on main: the latest" v0.3.2 "${c3}" "" false true false
[ "$(out newest)" = v0.3.1 ] || fail "newest: $(out newest)"
expect_refused "a tag on main without the newest release's commit" v0.3.2 "${s1}" "" "does not contain v0.3.1"
expect_pass "a re-run of a published tag" v0.3.1 "${c2}" "" true true false
expect_pass "a maintenance release from release/0.2: never the latest" v0.2.5 "${m1}" v0.2.5 false false true
expect_refused "a maintenance release on no branch" v0.2.6 "${o1}" v0.2.6 "on neither main nor a release/"
expect_refused "a pre-release of a version already out" v0.3.0-rc.7 "${c3}" "" "pre-release below"
expect_refused "a tag that is not on main" v0.3.2 "${m1}" "" "not on main"
# the update rehearsal (update-rehearsal.yml) must have passed on the very commit
REHEARSAL="completed failure" expect_refused "a commit the update rehearsal failed on" v0.3.2 "${c3}" "" \
    "the update rehearsal did not pass"
REHEARSAL="" expect_refused "a commit the update rehearsal never ran on" v0.3.2 "${c3}" "" \
    "no finished run of the update rehearsal"
REHEARSAL=$'completed failure\ncompleted success' expect_pass "a commit the update rehearsal passed on at its re-run" \
    v0.3.2 "${c3}" "" false true false
# main's rehearsal runs after CI (a workflow_run): found by its name, never by its head_sha,
# which is main's head when it started
REHEARSAL="" REHEARSAL_MAIN="completed success 901 Update rehearsal of ${c3}" expect_pass \
    "a commit main's rehearsal (after CI) passed on" v0.3.2 "${c3}" "" false true false
REHEARSAL="completed success 902 workflow_run" REHEARSAL_MAIN="completed success 902 Update rehearsal of ${c2}" \
    expect_refused "a workflow_run run whose head_sha is the commit but that rehearsed another" v0.3.2 "${c3}" "" \
    "no finished run of the update rehearsal"
REHEARSAL="" REHEARSAL_MAIN=$'completed failure 903 Update rehearsal of '"${c3}"$'\ncompleted skipped 904 Update rehearsal not run (CI cancelled)' \
    expect_refused "a commit main's rehearsal failed on" v0.3.2 "${c3}" "" "the update rehearsal did not pass"
REHEARSAL="" REHEARSAL_MAIN="completed success 905 Update rehearsal of ${c3}x" expect_refused \
    "a run named after a longer sha is not the commit's" v0.3.2 "${c3}" "" "no finished run of the update rehearsal"

# CI judged by its jobs: a failed run passes when its only failed jobs are the iPhone/iPad
# Player UI shards (a tag does not wait on them, since 2026-11-02) and the ci-success rollup
jobs() { # jobs ID LINES...: run ID's jobs, "conclusion name" (the first word is the conclusion)
    local id="$1"; shift
    : >"${work}/jobs/${id}"
    for l in "$@"; do printf '%s\t%s\n' "${l%% *}" "${l#* }" >>"${work}/jobs/${id}"; done
}
base=("success changes" "success unit" "skipped integration" "success apple-apps / Player UI tests (mac)"
    "success apple-apps / Player UI tests build (iphone)" "success apple-apps / Apple apps (Remote, Player, TV)"
    "success apple-apps / Player UI tests (iphone 1/6)" "success apple-apps / Player UI tests (ipad 6/6)")
jobs 7 "${base[@]}" "failure apple-apps / Player UI tests (iphone 2/6)" \
    "failure apple-apps / Player UI tests (ipad 5/6)" "cancelled apple-apps / Player UI tests (ipad 1/6)" "failure ci-success"
CI_RUNS="completed failure 7" expect_pass "a CI run failed only on iPhone/iPad UI shards and ci-success" \
    v0.3.2 "${c3}" "" false true false
grep -q 'ignored failures (flaky iPhone/iPad Player UI shards):.*Player UI tests (iphone 2/6) (failure).*(ipad 5/6) (failure)' "${work}/log" \
    || fail "the ignored shard failures are not printed: $(cat "${work}/log")"
jobs 8 "${base[@]}" "failure apple-apps / Player UI tests (iphone 2/6)" "failure ci-success" \
    "failure apple-apps / Player UI tests (mac)"
CI_RUNS="completed failure 8" expect_refused "a CI run the Mac UI tests failed on" v0.3.2 "${c3}" "" "CI did not pass"
grep -q 'required jobs: apple-apps / Player UI tests (mac) (failure)' "${work}/log" \
    || fail "the Mac UI failure is not named: $(cat "${work}/log")"
jobs 9 "${base[@]}" "failure apple-apps / Player UI tests build (ipad)" "failure ci-success"
CI_RUNS="completed failure 9" expect_refused "a CI run a UI test build failed on" v0.3.2 "${c3}" "" "CI did not pass"
jobs 10 "${base[@]}" "failure unit-go" "failure ci-success"
CI_RUNS="completed failure 10" expect_refused "a CI run a unit job failed on" v0.3.2 "${c3}" "" "CI did not pass"
jobs 11 "${base[@]}" "cancelled lint" "failure ci-success"
CI_RUNS="completed failure 11" expect_refused "a CI run with a cancelled job" v0.3.2 "${c3}" "" "CI did not pass"
jobs 12 "${base[@]}" "failure apple-apps / Player UI tests (iphone 2/6)"
CI_RUNS="completed cancelled 12" expect_refused "a cancelled CI run" v0.3.2 "${c3}" "" "CI did not pass"
CI_RUNS="in_progress  13" CI_RUNS_LATER="completed failure 7" expect_pass \
    "a CI run in progress is waited for, then judged" v0.3.2 "${c3}" "" false true false
grep -q 'waiting for CI' "${work}/log" || fail "did not wait for the CI run: $(cat "${work}/log")"
CI_RUNS="in_progress  13" expect_refused "a CI run still going at the deadline" v0.3.2 "${c3}" "" \
    "no finished run of CI"
CI_RUNS=$'completed failure 10\ncompleted success 14' expect_pass "a commit CI passed on in another run" \
    v0.3.2 "${c3}" "" false true false

# the nightly CVE gate (cve-gate-nightly.yml, unified through next_tag.py --check-cve):
# a failed newest run on the commit (or on a trail-only commit above it, or on a newer
# descendant commit, even behind >20 intervening cancelled runs) refuses the tag; a newer
# passed dispatch lets it through; a failed run on an older code commit does not block a
# newer commit
mkdir -p docs/trail/events
echo "event" >docs/trail/events/0001-note.md
git add docs/trail/events/0001-note.md
git commit -q -m "trail"
t1="$(git rev-parse HEAD)"
git push -q origin main 2>/dev/null

cve_run_json() { # cve_run_json SHA CONCLUSION EVENT CREATED [STATUS]
    printf '{"headSha":"%s","headBranch":"main","event":"%s","status":"%s","conclusion":"%s","createdAt":"%s"}' \
        "$1" "$3" "${5:-completed}" "$2" "$4"
}

CVE_NIGHTLY_SCHEDULE="[$(cve_run_json "${c3}" failure schedule 2026-11-26T04:23:00Z)]" \
    expect_refused "a commit whose newest nightly CVE gate failed" \
    v0.3.2 "${c3}" "" "nightly CVE gate run on ${c3}"
CVE_NIGHTLY_SCHEDULE="[$(cve_run_json "${t1}" failure schedule 2026-11-26T04:23:00Z)]" \
    expect_refused "a commit whose trail-only descendant failed the nightly CVE gate" \
    v0.3.2 "${c3}" "" "nightly CVE gate run on ${c3} (run on ${t1})"

# >20 intervening cancelled runs do not push a failed nightly out of the window
many_runs=""
for i in $(seq -w 1 25); do
    many_runs="${many_runs}$(cve_run_json "${c3}" cancelled schedule "2026-11-26T05:${i}:00Z"),"
done
many_runs="${many_runs}$(cve_run_json "${c3}" failure schedule 2026-11-26T04:23:00Z)"
CVE_NIGHTLY_SCHEDULE="[${many_runs}]" \
    expect_refused "a failed nightly CVE gate behind 25 cancelled runs still refuses the tag" \
    v0.3.2 "${c3}" "" "nightly CVE gate run on ${c3}"

CVE_NIGHTLY_SCHEDULE="[$(cve_run_json "${c3}" failure schedule 2026-11-26T04:23:00Z)]" \
    CVE_NIGHTLY_DISPATCH="[$(cve_run_json "${c3}" success workflow_dispatch 2026-11-26T06:00:00Z)]" \
    expect_pass "a newer green dispatch overrides an older failed nightly CVE gate" \
    v0.3.2 "${c3}" "" false true false
CVE_NIGHTLY_SCHEDULE="[$(cve_run_json "${c2}" failure schedule 2026-11-25T04:23:00Z)]" \
    expect_pass "a failed nightly CVE gate on an older code commit does not block a newer commit" \
    v0.3.2 "${c3}" "" false true false
CVE_NIGHTLY_SCHEDULE="[$(cve_run_json "${c2}" failure schedule 2026-11-26T04:23:00Z)]" \
    expect_pass "a re-run of an already-published tag is not blocked by a later nightly CVE failure" \
    v0.3.1 "${c2}" "" true true false

if [ "${fails}" -gt 0 ]; then
    echo "tag-guard-test: ${fails} failed"
    exit 1
fi
echo "tag-guard-test: all passed"
