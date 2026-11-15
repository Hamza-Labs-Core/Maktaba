#!/usr/bin/env bash
# Checks gh-retry.sh against a stubbed gh: a transient failure (HTTP 504, a stream CANCEL) is
# tried again and then succeeds; a 4xx or a missing file fails at once, after one try; tries
# that run out fail the step with gh's status; a read's stdout is the successful try's only.
# Then release-assets.sh, every call through it: upload on a draft and a published release;
# ensure's lookups and `release edit` (v0.3.147 failed on a stream CANCEL there); a create
# whose answer was lost is found on the next try, never made twice. And a guard: a bare gh
# call in release-assets.sh fails this test. Needs bash; touches nothing outside its temporary
# directory; waits no time (GH_RETRY_DELAYS of zeros).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stubbed gh: each `gh release upload` takes the next line of ${OUTCOMES} ("ok" or a
# message it fails with, status 1) and logs its arguments to ${CALLS}; past the last line it
# succeeds. The release lookups (release-assets.sh) answer one release, id 7, DRAFT, ASSETS;
# none while NO_RELEASE is set and no create has made it. The other calls take their outcomes
# from ${STATE}/KIND.outcomes and log to ${STATE}/KIND.calls, KIND one of list (the releases'
# lookup), draft, assets, edit, create: "partial:MSG" prints a first page, then fails with MSG
# (assets); "lost:MSG" makes the release, then fails with MSG, its answer lost (create).
mkdir -p "${work}/stubs" "${work}/state"
export STATE="${work}/state"
cat >"${work}/stubs/gh" <<'EOF'
#!/usr/bin/env bash
# next KIND ARG...: logs the call; prints its outcome
next() {
    local kind="$1" n
    shift
    echo "$*" >>"${STATE}/${kind}.calls"
    n="$(wc -l <"${STATE}/${kind}.calls" | tr -d ' ')"
    [ -f "${STATE}/${kind}.outcomes" ] && sed -n "${n}p" "${STATE}/${kind}.outcomes"
    return 0
}
# fail MSG: a failed call
fail() { echo "$1" >&2; exit 1; }
case "$1 $2" in
    "release upload")
        echo "$*" >>"${CALLS}"
        n="$(wc -l <"${CALLS}" | tr -d ' ')"
        outcome="$(sed -n "${n}p" "${OUTCOMES}")"
        case "${outcome}" in
            "" | ok) echo "uploaded"; exit 0 ;;
            *) echo "${outcome}" >&2; exit 1 ;;
        esac
        ;;
    "release edit")
        o="$(next edit "$@")"
        case "${o}" in "" | ok) exit 0 ;; *) fail "${o}" ;; esac
        ;;
    "release create")
        o="$(next create "$@")"
        case "${o}" in
            "" | ok) touch "${STATE}/created"; exit 0 ;;
            lost:*) touch "${STATE}/created"; fail "${o#lost:}" ;;
            *) fail "${o}" ;;
        esac
        ;;
    api*)
        case "$2" in
            *"/releases?per_page=100")
                o="$(next list "$@")"
                case "${o}" in "" | ok) ;; *) fail "${o}" ;; esac
                if [ -z "${NO_RELEASE:-}" ] || [ -f "${STATE}/created" ]; then echo 7; fi
                ;;
            *"/releases/7")
                o="$(next draft "$@")"
                case "${o}" in "" | ok) echo "${DRAFT:-true}" ;; *) fail "${o}" ;; esac
                ;;
            *"/releases/7/assets?per_page=100")
                o="$(next assets "$@")"
                case "${o}" in
                    "" | ok) printf '%s\n' "${ASSETS:-}" ;;
                    partial:*) printf '%s\n' "${ASSETS:-}" | sed -n 1p; fail "${o#partial:}" ;;
                    *) fail "${o}" ;;
                esac
                ;;
            *) echo "gh stub: $*" >&2; exit 1 ;;
        esac
        ;;
    *) echo "gh stub: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${work}/stubs/gh"

# outcomes LINE...: the uploads' outcomes, in order; the call log emptied
outcomes() { printf '%s\n' "$@" >"${work}/outcomes"; : >"${work}/calls"; }
calls() { wc -l <"${work}/calls" | tr -d ' '; }
# retry ARG...: gh-retry.sh on a stubbed upload; its log in ${work}/log, its status in rc
retry() {
    rc=0
    PATH="${work}/stubs:${PATH}" CALLS="${work}/calls" OUTCOMES="${work}/outcomes" GH_RETRY_DELAYS="0 0 0 0" \
        bash "${here}/gh-retry.sh" gh release upload v0.3.2 a.tar.gz --clobber >"${work}/log" 2>&1 || rc=$?
}

e504="HTTP 504: We couldn't respond to your request in time. Sorry about that. Please try resubmitting your request and contact us if the problem persists."
cancel="Post \"https://uploads.github.com/repos/o/r/releases/7/assets?label=&name=a.tar.gz\": stream error: stream ID 1; CANCEL; received from peer"

outcomes "${e504}" "${cancel}" ok
retry
if [ "${rc}" = 0 ] && [ "$(calls)" = 3 ] && grep -q "trying again" "${work}/log"; then
    ok "a 504, then a stream CANCEL, then success: three tries, the step passes"
else
    fail "transient then success (rc ${rc}, $(calls) calls): $(cat "${work}/log")"
fi
if grep -q -- "--clobber" "${work}/calls" && [ "$(grep -c -- '--clobber' "${work}/calls")" = 3 ]; then
    ok "every try replaces what arrived (--clobber)"
else
    fail "a try without --clobber: $(cat "${work}/calls")"
fi

for e in "connection reset by peer" "net/http: TLS handshake timeout" "HTTP 502: Bad Gateway" \
    "read tcp 10.0.0.1:443: i/o timeout" "unexpected EOF"; do
    outcomes "${e}" ok
    retry
    if [ "${rc}" = 0 ] && [ "$(calls)" = 2 ]; then
        ok "retried: ${e}"
    else
        fail "not retried (rc ${rc}, $(calls) calls): ${e}: $(cat "${work}/log")"
    fi
done

for e in "HTTP 404: Not Found (https://api.github.com/repos/o/r/releases/7/assets)" \
    "HTTP 401: Bad credentials" "HTTP 403: Resource not accessible by integration" \
    "HTTP 422: Validation Failed (timeout of the asset name)" \
    "open a.tar.gz: no such file or directory"; do
    outcomes "${e}" ok
    retry
    if [ "${rc}" = 1 ] && [ "$(calls)" = 1 ] && grep -q "not a transient error" "${work}/log"; then
        ok "failed at once: ${e}"
    else
        fail "not failed at once (rc ${rc}, $(calls) calls): ${e}: $(cat "${work}/log")"
    fi
done

outcomes "${e504}" "${e504}" "${cancel}" "${e504}" "${e504}" ok
retry
if [ "${rc}" = 1 ] && [ "$(calls)" = 5 ] && grep -q "failed 5 times on transient errors" "${work}/log"; then
    ok "five transient failures: the tries run out and the step fails"
else
    fail "tries did not run out (rc ${rc}, $(calls) calls): $(cat "${work}/log")"
fi

rc=0
GH_RETRY_DELAYS="0" bash "${here}/gh-retry.sh" >"${work}/log" 2>&1 || rc=$?
if [ "${rc}" = 2 ]; then ok "no command: a usage error"; else fail "no command: rc ${rc}"; fi

# release-assets.sh upload: its uploads go through gh-retry.sh, on a draft (one upload of
# every file) and on a published release (one upload per file it lacks)
mkdir -p "${work}/dist"
printf a >"${work}/dist/a.tar.gz"
printf b >"${work}/dist/b.tar.gz"
upload() { # upload DRAFT ASSETS: release-assets.sh upload; its log in ${work}/log
    rc=0
    PATH="${work}/stubs:${PATH}" CALLS="${work}/calls" OUTCOMES="${work}/outcomes" GH_RETRY_DELAYS="0 0 0 0" \
        GITHUB_REPOSITORY=o/r DRAFT="$1" ASSETS="$2" \
        bash "${here}/release-assets.sh" upload v0.3.2 "${work}/dist/a.tar.gz" "${work}/dist/b.tar.gz" \
        >"${work}/log" 2>&1 || rc=$?
}

outcomes "${cancel}" ok
upload true ""
if [ "${rc}" = 0 ] && [ "$(calls)" = 2 ] && grep -q "there already (draft)" "${work}/log" &&
    [ "$(sed -n 2p "${work}/calls")" = "release upload v0.3.2 ${work}/dist/a.tar.gz ${work}/dist/b.tar.gz --clobber" ]; then
    ok "a draft's upload is tried again after a stream CANCEL, every file with --clobber"
else
    fail "draft upload (rc ${rc}, $(calls) calls): $(cat "${work}/log" "${work}/calls")"
fi

outcomes "HTTP 404: Not Found" ok
upload true ""
if [ "${rc}" != 0 ] && [ "$(calls)" = 1 ]; then
    ok "a draft's upload failing with a 404 fails the step at once"
else
    fail "draft 404 (rc ${rc}, $(calls) calls): $(cat "${work}/log")"
fi

outcomes "${e504}" ok
upload false "a.tar.gz"
if [ "${rc}" = 0 ] && [ "$(calls)" = 2 ] && grep -q "a.tar.gz kept as it is" "${work}/log" &&
    ! grep -q "dist/a.tar.gz" "${work}/calls"; then
    ok "a published release: only the file it lacks is uploaded, again after a 504"
else
    fail "published upload (rc ${rc}, $(calls) calls): $(cat "${work}/log" "${work}/calls")"
fi

# kinds KIND=OUTCOME,OUTCOME... ...: the other calls' outcomes, every log emptied
kinds() {
    rm -rf "${STATE}"
    mkdir -p "${STATE}"
    local k
    for k in "$@"; do
        tr ',' '\n' <<<"${k#*=}" >"${STATE}/${k%%=*}.outcomes"
    done
}
kcalls() { if [ -f "${STATE}/$1.calls" ]; then wc -l <"${STATE}/$1.calls" | tr -d ' '; else echo 0; fi; }
# assets: release-assets.sh CMD [ARG]... with the stub; its log in ${work}/log
assets() {
    rc=0
    PATH="${work}/stubs:${PATH}" CALLS="${work}/calls" OUTCOMES="${work}/outcomes" GH_RETRY_DELAYS="0 0 0 0" \
        GITHUB_REPOSITORY=o/r bash "${here}/release-assets.sh" "$@" >"${work}/log" 2>&1 || rc=$?
}

# a read: its stdout is the successful try's, once (a first page printed by a failed try is
# never in the $(...))
kinds "assets=partial:${cancel},ok"
rc=0
got="$(PATH="${work}/stubs:${PATH}" GH_RETRY_DELAYS="0 0 0 0" ASSETS="$(printf 'a.tar.gz\nb.tar.gz')" \
    bash "${here}/gh-retry.sh" gh api "repos/o/r/releases/7/assets?per_page=100" --paginate --jq '.[].name' \
    2>"${work}/log")" || rc=$?
if [ "${rc}" = 0 ] && [ "$(kcalls assets)" = 2 ] && [ "${got}" = "$(printf 'a.tar.gz\nb.tar.gz')" ] &&
    grep -q "trying again" "${work}/log"; then
    ok "a paginated read cut by a stream CANCEL: tried again, its stdout the second try's only"
else
    fail "partial read (rc ${rc}, $(kcalls assets) calls): stdout [${got}]: $(cat "${work}/log")"
fi

# ensure on a draft, with notes: the releases' lookup cut by a stream CANCEL (v0.3.147), the
# draft lookup by a 502, the notes' edit by a 504: each tried again, the step passes
kinds "list=${cancel},ok" "draft=HTTP 502: Bad Gateway,ok" "edit=${e504},ok"
NOTES="the notes" assets ensure v0.3.2
if [ "${rc}" = 0 ] && [ "$(kcalls list)" = 2 ] && [ "$(kcalls draft)" = 2 ] && [ "$(kcalls edit)" = 2 ] &&
    grep -q "there already (draft)" "${work}/log" && [ "$(grep -c 'trying again' "${work}/log")" = 3 ]; then
    ok "ensure: a lookup and \`release edit\` cut by transient errors are tried again"
else
    fail "ensure retries (rc ${rc}; list $(kcalls list), draft $(kcalls draft), edit $(kcalls edit)): $(cat "${work}/log")"
fi

# publish of a complete draft: the assets' read and `release edit` (publishing it) tried again
kinds "assets=partial:${cancel},ok" "edit=${e504},ok"
ASSETS="$(printf 'release.json\nchecksums.txt\nchecksums.txt.sigstore.json\ncomponents-0.3.2-rc.1.json')" \
    assets publish v0.3.2-rc.1
if [ "${rc}" = 0 ] && [ "$(kcalls assets)" = 2 ] && [ "$(kcalls edit)" = 2 ] &&
    grep -q "release v0.3.2-rc.1: published" "${work}/log" && grep -q -- "--draft=false" "${STATE}/edit.calls"; then
    ok "publish: the assets' read and the publishing \`release edit\` are tried again"
else
    fail "publish retries (rc ${rc}; assets $(kcalls assets), edit $(kcalls edit)): $(cat "${work}/log")"
fi

# a draft lookup that keeps failing (a 404) fails the step: never taken for "published"
kinds "draft=HTTP 404: Not Found"
outcomes ok
assets upload v0.3.2 "${work}/dist/a.tar.gz"
if [ "${rc}" != 0 ] && [ "$(kcalls draft)" = 1 ] && [ "$(calls)" = 0 ] && grep -q "could not read whether it is a draft" "${work}/log"; then
    ok "a draft lookup failing with a 404 fails the step at once, nothing uploaded"
else
    fail "draft lookup 404 (rc ${rc}, draft $(kcalls draft), uploads $(calls)): $(cat "${work}/log")"
fi

# a create whose answer was lost (it made the release, then the stream was cancelled): the next
# try finds the release, and it is made once
kinds "create=lost:${cancel}"
outcomes ok
NO_RELEASE=1 assets ensure v0.3.2
if [ "${rc}" = 0 ] && [ "$(kcalls create)" = 1 ] && grep -q "trying again" "${work}/log" &&
    grep -q "there already (made by an earlier try" "${work}/log"; then
    ok "a create cut after it made the release: found on the next try, made once"
else
    fail "lost create (rc ${rc}, $(kcalls create) creates): $(cat "${work}/log")"
fi

# the same, and the next try's lookup is cut too: that try fails (never taken for "no release"),
# the one after finds it; made once
kinds "create=lost:${cancel}" "list=ok,ok,${e504},ok"
NO_RELEASE=1 assets ensure v0.3.2
if [ "${rc}" = 0 ] && [ "$(kcalls create)" = 1 ] && [ "$(kcalls list)" = 4 ] &&
    grep -q "there already (made by an earlier try" "${work}/log"; then
    ok "a lookup cut inside a create's try fails that try: the release is still made once"
else
    fail "lookup cut in create-once (rc ${rc}, $(kcalls create) creates, $(kcalls list) lookups): $(cat "${work}/log")"
fi

# a create cut before it made anything: tried again, made on the second try
kinds "create=${e504},ok"
NO_RELEASE=1 assets ensure v0.3.2
if [ "${rc}" = 0 ] && [ "$(kcalls create)" = 2 ] && grep -q "created as a draft" "${work}/log"; then
    ok "a create failing with a 504: tried again, created"
else
    fail "504 create (rc ${rc}, $(kcalls create) creates): $(cat "${work}/log")"
fi

# a create refused (422) because another workflow made the release meanwhile: the step passes
kinds "create=lost:HTTP 422: Validation Failed (already_exists)"
NO_RELEASE=1 assets ensure v0.3.2
if [ "${rc}" = 0 ] && [ "$(kcalls create)" = 1 ] && grep -q "not a transient error" "${work}/log"; then
    ok "a create refused as made meanwhile by another workflow: not tried again, the step passes"
else
    fail "made meanwhile (rc ${rc}, $(kcalls create) creates): $(cat "${work}/log")"
fi

# a create refused (422) with no release made: the step fails
kinds "create=HTTP 422: Validation Failed"
NO_RELEASE=1 assets ensure v0.3.2
if [ "${rc}" != 0 ] && [ "$(kcalls create)" = 1 ] && grep -q "could not be created" "${work}/log"; then
    ok "a create refused with no release made fails the step"
else
    fail "422 create (rc ${rc}, $(kcalls create) creates): $(cat "${work}/log")"
fi

# the guard: every gh call in release-assets.sh goes through gh-retry.sh (gh_); a bare one is
# allowed only on gh_'s own line inside a try (marked "# raw: already inside a gh-retry.sh try")
# bare FILE: the lines of FILE with a gh call outside gh-retry.sh
# shellcheck disable=SC2016 # the script's own text, not expanded
bare() {
    grep -nE '(^|[^-_./[:alnum:]])gh( |$)' "$1" |
        grep -vE '^[0-9]+:[[:space:]]*#' |
        grep -vF '# raw: already inside a gh-retry.sh try' |
        grep -vF '"${here}/gh-retry.sh" gh ' || true
}
found="$(bare "${here}/release-assets.sh")"
if [ -z "${found}" ]; then
    ok "release-assets.sh: no gh call outside gh-retry.sh"
else
    fail "release-assets.sh has gh calls outside gh-retry.sh (use gh_):"$'\n'"${found}"
fi
# and the guard itself catches one
# shellcheck disable=SC2016 # the script's own text, not expanded
sed 's|gh_ release edit "${tag}" --latest|gh release edit "${tag}" --latest|' "${here}/release-assets.sh" >"${work}/bare.sh"
if [ -n "$(bare "${work}/bare.sh")" ]; then
    ok "the guard catches a bare gh call"
else
    fail "the guard missed a bare gh release edit"
fi

if [ "${fails}" -gt 0 ]; then
    echo "gh-retry-test: ${fails} failed"
    exit 1
fi
echo "gh-retry-test: all passed"
