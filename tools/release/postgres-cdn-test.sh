#!/usr/bin/env bash
# Checks postgres-cdn.sh against a scratch "CDN" and "GitHub" (file:// folders) with a
# stub upload: every archive uploaded once and skipped on the next run; one that does not
# match its pin never uploaded; a CDN copy that is not the pin replaced; a failed upload
# exit 3; a malformed list refused. Touches nothing outside its temporary directory.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

sha256() {
    if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

src="${work}/github" cdn="${work}/cdn"
mkdir -p "${src}/9.9.0" "${cdn}"
printf 'archive one' >"${src}/9.9.0/postgresql-9.9.0-a.tar.gz"
printf 'archive two!' >"${src}/9.9.0/postgresql-9.9.0-b.zip"
list="${work}/list.txt"
{
    echo "# a comment"
    for n in postgresql-9.9.0-a.tar.gz postgresql-9.9.0-b.zip; do
        echo "postgres/9.9.0/${n} $(sha256 "${src}/9.9.0/${n}") $(wc -c <"${src}/9.9.0/${n}" | tr -d ' ')"
    done
} >"${list}"

# the stub upload: copies into the CDN folder, and counts
cat >"${work}/upload" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
echo "$1" >>"${UPLOADS}"
[ -z "${UPLOAD_FAIL:-}" ] || exit 1
mkdir -p "${CDN_DIR}/$(dirname "${1#*/}")"
cp "$2" "${CDN_DIR}/${1#*/}"
STUB
chmod +x "${work}/upload"
export UPLOAD="${work}/upload" UPLOADS="${work}/uploads" CDN_DIR="${cdn}"
export CDN_URL="file://${cdn}" SOURCE_URL="file://${src}" BUCKET=test-bucket GH_CLI=false
run() { PATH="/usr/bin:/bin" bash "${here}/postgres-cdn.sh" "$1" "${work}/dl-$2" >"${work}/out-$2" 2>&1; }

echo "a first run uploads each archive"
: >"${UPLOADS}"
if run "${list}" 1 && [ "$(wc -l <"${UPLOADS}" | tr -d ' ')" = 2 ] &&
    grep -qx "test-bucket/postgres/9.9.0/postgresql-9.9.0-a.tar.gz" "${UPLOADS}" &&
    cmp -s "${cdn}/postgres/9.9.0/postgresql-9.9.0-b.zip" "${src}/9.9.0/postgresql-9.9.0-b.zip"; then
    ok "two archives uploaded to the bucket's postgres/<version>/<name>"
else
    fail "first run: $(cat "${work}/out-1") uploads: $(cat "${UPLOADS}")"
fi

echo "a second run skips them"
: >"${UPLOADS}"
if run "${list}" 2 && [ ! -s "${UPLOADS}" ] && grep -q "0 uploaded, 2 there already" "${work}/out-2"; then
    ok "nothing uploaded again"
else
    fail "second run: $(cat "${work}/out-2") uploads: $(cat "${UPLOADS}")"
fi

echo "a CDN copy that is not the pin is replaced"
printf 'tampered' >"${cdn}/postgres/9.9.0/postgresql-9.9.0-a.tar.gz"
: >"${UPLOADS}"
if run "${list}" 3 && grep -qx "test-bucket/postgres/9.9.0/postgresql-9.9.0-a.tar.gz" "${UPLOADS}" &&
    [ "$(wc -l <"${UPLOADS}" | tr -d ' ')" = 1 ] && grep -q "not the pinned archive: replaced" "${work}/out-3" &&
    cmp -s "${cdn}/postgres/9.9.0/postgresql-9.9.0-a.tar.gz" "${src}/9.9.0/postgresql-9.9.0-a.tar.gz"; then
    ok "the pinned archive put back"
else
    fail "replace: $(cat "${work}/out-3") uploads: $(cat "${UPLOADS}")"
fi

echo "an archive that does not match its pin is never uploaded"
rm -rf "${cdn:?}"/*
printf 'archive ONE' >"${src}/9.9.0/postgresql-9.9.0-a.tar.gz" # same size, other bytes
: >"${UPLOADS}"
rc=0
run "${list}" 4 || rc=$?
if [ "${rc}" = 2 ] && [ ! -s "${UPLOADS}" ] && grep -q "pinned .*: not uploaded" "${work}/out-4" &&
    [ ! -e "${work}/dl-4/postgresql-9.9.0-a.tar.gz" ]; then
    ok "refused (exit 2), nothing uploaded, the download removed"
else
    fail "mismatch: rc ${rc} $(cat "${work}/out-4") uploads: $(cat "${UPLOADS}")"
fi
printf 'archive one' >"${src}/9.9.0/postgresql-9.9.0-a.tar.gz"

echo "an archive GitHub does not have fails (exit 2)"
mv "${src}/9.9.0/postgresql-9.9.0-b.zip" "${work}/b.zip"
rc=0
run "${list}" 5 || rc=$?
if [ "${rc}" = 2 ] && grep -q "could not be downloaded" "${work}/out-5"; then
    ok "refused"
else
    fail "missing: rc ${rc} $(cat "${work}/out-5")"
fi
mv "${work}/b.zip" "${src}/9.9.0/postgresql-9.9.0-b.zip"

echo "a failed upload is exit 3"
rm -rf "${cdn:?}"/*
rc=0
UPLOAD_FAIL=1 run "${list}" 6 || rc=$?
if [ "${rc}" = 3 ] && grep -q "the upload to test-bucket failed" "${work}/out-6"; then
    ok "exit 3"
else
    fail "upload failure: rc ${rc} $(cat "${work}/out-6")"
fi

echo "a malformed list is refused"
printf 'postgres/9.9.0/x.tar.gz notasha 12\n' >"${work}/bad.txt"
rc=0
run "${work}/bad.txt" 7 || rc=$?
if [ "${rc}" = 2 ] && grep -q 'not a "<path> <sha256> <size>" line' "${work}/out-7"; then
    ok "exit 2"
else
    fail "malformed: rc ${rc} $(cat "${work}/out-7")"
fi

echo "the repository's list is pgbin's pins (TestTheCDNListIsThePins) and well formed"
if awk '!/^#/ && NF { if (NF != 3 || $1 !~ /^postgres\/[0-9.]+\/postgresql-/ || $2 !~ /^[0-9a-f]{64}$/ || $3 !~ /^[0-9]+$/) bad=1; n++ }
    END { exit (bad || n == 0) }' "${here}/postgres-cdn.txt"; then
    ok "postgres-cdn.txt parses"
else
    fail "postgres-cdn.txt"
fi

[ "${fails}" = 0 ] || { echo "${fails} failed"; exit 1; }
echo "all passed"
