#!/usr/bin/env bash
# The tag-order guard: the first job of every tag workflow (release.yml, desktop-release.yml,
# mobile-release.yml, tv-release.yml, play-bundles.yml, through .github/workflows/_tag-guard.yml). A tag is
# released only when:
#   - it is above every published release (tools/release/releaseorder): v0.3.0-rc.7, tagged
#     after v0.3.0, was offered by no updater, and machines on it were offered the older
#     0.3.0. A tag already published is its own release run again (allowed: published=true,
#     and release.yml then builds and signs nothing again: only a draft is rebuilt);
#   - its commit is on main, and the newest published release's commit is in its history
#     (a tag cut from a branch that missed what the newest release carried would undo it);
#   - CI passed on that commit (the CI workflow's run for it; a run still going is waited for).
#     A finished run that failed only on the jobs IGNORED_CI_JOBS names (the flaky iPhone/iPad
#     Player UI shards, since 2026-11-02) and the ci-success rollup counts as passed;
#   - the update rehearsal passed on that exact commit (update-rehearsal.yml: every update
#     path of the Mac app, from the latest published release to this commit; waited for
#     likewise). Since 2026-10-28: no release before its updates are proven in CI.
# A deliberate maintenance release of an older line (story 22.5 EC1) is named in the
# repository variable MAKTABA_RELEASE_ALLOW_TAG: its commit is on main or a release/*
# branch (ci.yml runs on those), it skips the history check, and it never becomes the
# latest (publish: releaseorder.Latest; its images' :latest and the relay stay as they are).
#
# Env: TAG, SHA, GH_TOKEN, GITHUB_REPOSITORY; ALLOW_TAG (the variable); CI_WORKFLOW
# (default ci.yml); REHEARSAL_WORKFLOW (default update-rehearsal.yml); CI_WAIT_SECONDS
# (default 3600, for each); CI_POLL_SECONDS (default 30). Writes maintenance=, published=,
# latest= (true|false) and newest= to $GITHUB_OUTPUT when set.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${TAG:?}" "${SHA:?}"
ci="${CI_WORKFLOW:-ci.yml}"
wait_s="${CI_WAIT_SECONDS:-3600}"

err() { echo "::error::tag guard: $*" >&2; exit 1; }

# 1. its place among the published releases
published="$(gh release list --exclude-drafts --limit 1000 --json tagName --jq '.[].tagName')"
if ! out="$(printf '%s\n' "${published}" | (cd "${here}" && go run ./cmd/release-order guard --tag "${TAG}" \
    --allow "${ALLOW_TAG:-}") 2>&1)"; then
    err "${out#release-order: refused: }"
fi
field() { sed -n "s/^$1=//p" <<<"${out}" | head -1; }
maintenance="$(field maintenance)"
is_published="$(field published)"
latest="$(field latest)"
newest="$(field newest)"
echo "tag guard: ${TAG} may be released (maintenance: ${maintenance}, published already: ${is_published}," \
    "the latest: ${latest}, newest published: ${newest:-none})"

# 2. on main (a maintenance release: main or its release/* branch), with the newest release's
# commit in its history (not for a maintenance release, nor a re-run of a published one)
git fetch --quiet --no-tags origin +refs/heads/main:refs/remotes/origin/main '+refs/heads/release/*:refs/remotes/origin/release/*'
if [ "${maintenance}" != true ]; then
    git merge-base --is-ancestor "${SHA}" origin/main \
        || err "${TAG} points at ${SHA}, which is not on main: release from a commit main has (CI passed on it)"
else
    on=""
    for b in origin/main $(git for-each-ref --format='%(refname:short)' 'refs/remotes/origin/release/*'); do
        if git merge-base --is-ancestor "${SHA}" "${b}"; then on="${b}"; break; fi
    done
    [ -n "${on}" ] || err "${TAG} (a maintenance release) points at ${SHA}, which is on neither main nor a release/* branch"
    echo "tag guard: ${SHA} is on ${on}"
fi
if [ "${maintenance}" != true ] && [ "${is_published}" != true ] && [ -n "${newest}" ]; then
    git fetch --quiet origin "+refs/tags/${newest}:refs/tags/${newest}" 2>/dev/null || true
    newest_sha="$(git rev-list -n 1 "refs/tags/${newest}" 2>/dev/null)" \
        || err "the newest published release ${newest} has no tag here to compare with"
    git merge-base --is-ancestor "${newest_sha}" "${SHA}" \
        || err "${TAG} (${SHA}) does not contain ${newest} (${newest_sha}), the newest published release: a release cut beside it would undo what it carried. Tag a commit that has it"
fi

# 3. CI passed on the commit: its run for it (a push to main, or the pull request's head),
# waited for while it runs; and 4. the update rehearsal passed on that exact commit
# (update-rehearsal.yml, docs/update-rehearsal.md: every update path of the owner's Mac
# app, rehearsed; it runs on every push to main and release/*)
# The CI jobs a release does not wait on (decided 2026-11-02): the
# Apple iPhone/iPad Player UI test shards are flaky and are being fixed separately, so a
# finished CI run whose only failed jobs are these shards (and the ci-success rollup that
# reports them) still lets a tag through. Exactly the iphone/ipad shards: the Mac UI job
# "(mac)", the UI test builds and every other Apple job stay required. Remove this variable
# (set it to '') once the shards are reliable.
IGNORED_CI_JOBS='^apple-apps / Player UI tests [(](iphone|ipad) [0-9]+/[0-9]+[)]$'
# The rollup job: not judged itself; the jobs it rolls up are judged directly instead.
CI_ROLLUP_JOB='ci-success'

# jobs_pass RUN_ID: a finished CI run failed only on ignored jobs. Every other job is success
# or skipped (a failure, a cancellation, anything else blocks). The ignored jobs that failed
# are printed so the release notes them.
jobs_pass() {
    local id="$1" jobs blocking ignored
    jobs="$(gh api "repos/${GITHUB_REPOSITORY:?}/actions/runs/${id}/jobs?per_page=100" --paginate \
        --jq '.jobs[] | "\(.conclusion)\t\(.name)"')" || return 1
    [ -n "${jobs}" ] || return 1
    blocking="$(awk -F'\t' -v ign="${IGNORED_CI_JOBS}" -v rollup="${CI_ROLLUP_JOB}" '
        $2 == rollup { next }
        ign != "" && $2 ~ ign { next }
        $1 != "success" && $1 != "skipped" { print $2 " (" $1 ")" }' <<<"${jobs}")"
    if [ -n "${blocking}" ]; then
        echo "tag guard: CI run ${id} failed on required jobs: $(tr '\n' ',' <<<"${blocking}" | sed 's/,$//')"
        return 1
    fi
    ignored="$(awk -F'\t' -v ign="${IGNORED_CI_JOBS}" '
        ign != "" && $2 ~ ign && $1 != "success" && $1 != "skipped" { print $2 " (" $1 ")" }' <<<"${jobs}")"
    echo "::warning::tag guard: CI run ${id} passed every required job; ignored failures (flaky iPhone/iPad Player UI shards): $(tr '\n' ',' <<<"${ignored:-none}" | sed 's/,$//')"
    return 0
}

passed_on() { # passed_on WORKFLOW WHAT [judge-jobs]
    local workflow="$1" what="$2" judge="${3:-}" runs id
    local deadline=$(( $(date +%s) + wait_s ))
    while :; do
        runs="$(gh run list --workflow "${workflow}" --commit "${SHA}" --limit 50 --json status,conclusion,databaseId \
            --jq '[.[] | "\(.status) \(.conclusion) \(.databaseId)"] | .[]' 2>/dev/null || true)"
        if grep -q '^completed success ' <<<"${runs}"; then
            echo "tag guard: ${what} passed on ${SHA}"
            return 0
        fi
        # a finished, failed CI run whose only failed jobs are the ignored ones (a cancelled run
        # is never judged: it blocks)
        if [ -n "${judge}" ]; then
            while read -r id; do
                if jobs_pass "${id}" </dev/null; then
                    echo "tag guard: ${what} passed on ${SHA} (run ${id}, every required job)"
                    return 0
                fi
            done < <(awk '$1 == "completed" && $2 == "failure" { print $3 }' <<<"${runs}")
        fi
        if [ -n "${runs}" ] && ! grep -qv '^completed ' <<<"${runs}"; then
            err "${what} did not pass on ${SHA} ($(cut -d' ' -f1,2 <<<"${runs}" | tr '\n' ',' | sed 's/,$//')): fix main, then tag the commit ${what} passed on"
        fi
        [ "$(date +%s)" -lt "${deadline}" ] \
            || err "no finished run of ${what} on ${SHA} after ${wait_s}s (${runs:-none started}): tag a commit ${what} passed on"
        echo "tag guard: waiting for ${what} on ${SHA} (${runs:-not started yet})"
        sleep "${CI_POLL_SECONDS:-30}"
    done
}
passed_on "${ci}" "CI" judge-jobs
passed_on "${REHEARSAL_WORKFLOW:-update-rehearsal.yml}" "the update rehearsal"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
    {
        echo "maintenance=${maintenance}"
        echo "published=${is_published}"
        echo "latest=${latest}"
        echo "newest=${newest}"
    } >>"${GITHUB_OUTPUT}"
fi
