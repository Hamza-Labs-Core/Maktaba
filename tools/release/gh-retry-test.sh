#!/usr/bin/env bash
# Checks gh-retry.sh against a stubbed gh: a transient failure (HTTP 504, a stream CANCEL) is
# tried again and then succeeds; a 4xx or a missing file fails at once, after one try; tries
# that run out fail the step with gh's status. Then release-assets.sh upload, on a draft and a
# published release, retries its upload through it. Needs bash; touches nothing outside its
# temporary directory; waits no time (GH_RETRY_DELAYS of zeros).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# the stubbed gh: each `gh release upload` takes the next line of ${OUTCOMES} ("ok" or a
# message it fails with, status 1) and logs its arguments to ${CALLS}; past the last line it
# succeeds. The release lookups (release-assets.sh) answer one release, id 7, DRAFT, ASSETS.
mkdir -p "${work}/stubs"
cat >"${work}/stubs/gh" <<'EOF'
#!/usr/bin/env bash
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
    "release edit") exit 0 ;;
    api*)
        case "$2" in
            *"/releases?per_page=100") echo 7 ;;
            *"/releases/7") echo "${DRAFT:-true}" ;;
            *"/releases/7/assets?per_page=100") printf '%s\n' "${ASSETS:-}" ;;
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

if [ "${fails}" -gt 0 ]; then
    echo "gh-retry-test: ${fails} failed"
    exit 1
fi
echo "gh-retry-test: all passed"
