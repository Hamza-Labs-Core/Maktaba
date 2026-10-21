#!/usr/bin/env bash
# install.sh verification fails closed (review of PR #102): a release whose
# checksums.txt, Sigstore bundle or archive entry is missing is refused, the
# archive name is matched exactly, a duplicate entry is refused, and a bad tag
# never reaches a URL. Runs install.sh's functions against a local "release"
# (fetch is replaced by a copy); no network, nothing installed.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
export work
trap 'rm -rf "${work}"' EXIT

# shellcheck source=SCRIPTDIR/../install.sh
MAKTABA_INSTALL_LIB=1 . "${here}/../install.sh"
set +e  # each case runs in a subshell and its status is checked

fetch() { # fetch <url> <dest>: serve ${work}/release/<basename url>
    local src
    src="${work}/release/$(basename "$1")"
    [ -f "${src}" ] || return 22
    cp "${src}" "$2"
}

sha() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}

archive="maktaba-server-1.5.0-linux-amd64.tar.gz"
fresh_release() {
    rm -rf "${work}/release" "${work}/dl"
    mkdir -p "${work}/release" "${work}/dl"
    printf 'archive bytes\n' > "${work}/release/${archive}"
    cp "${work}/release/${archive}" "${work}/dl/${archive}"
    printf '%s  ./%s\n' "$(sha "${work}/release/${archive}")" "${archive}" > "${work}/release/checksums.txt"
    printf '{}\n' > "${work}/release/checksums.txt.sigstore.json"
}

fails=0
expect() { # expect <ok|fail> <name> <command...>
    local want="$1" name="$2" got
    shift 2
    if ( "$@" ) >/dev/null 2>&1; then got=ok; else got=fail; fi
    if [ "${got}" = "${want}" ]; then
        echo "ok   ${name}"
    else
        echo "FAIL ${name}: wanted ${want}, got ${got}"
        fails=$((fails + 1))
    fi
}

run_verify() { verify_release "${work}/dl" "https://example.invalid/r" v1.5.0 "${archive}"; }

# Without cosign on PATH, only the SHA-256 can be checked (warned): PATH minus
# every directory holding a cosign.
nocosign_path=""
IFS=: read -r -a path_dirs <<< "${PATH}"
for d in "${path_dirs[@]}"; do
    [ -x "${d}/cosign" ] || [ -x "${d}/cosign.exe" ] && continue
    nocosign_path="${nocosign_path:+${nocosign_path}:}${d}"
done
without_cosign() { PATH="${nocosign_path}" "$@"; }

fresh_release
expect ok   "complete release verifies (no cosign: checksum only)" without_cosign run_verify

fresh_release; rm "${work}/release/checksums.txt"
expect fail "missing checksums.txt is refused" without_cosign run_verify

fresh_release; rm "${work}/release/checksums.txt.sigstore.json"
expect fail "missing Sigstore bundle is refused" without_cosign run_verify

fresh_release; printf '%s  ./other.tar.gz\n' "$(sha "${work}/release/${archive}")" > "${work}/release/checksums.txt"
expect fail "archive not listed is refused" without_cosign run_verify

fresh_release; printf 'tampered\n' > "${work}/dl/${archive}"
expect fail "tampered archive is refused" without_cosign run_verify

fresh_release
# The old grep treated the name as a regexp: '.' matched any character.
printf '%s  ./maktaba-server-1x5x0-linux-amd64xtarxgz\n' "$(sha "${work}/release/${archive}")" > "${work}/release/checksums.txt"
expect fail "look-alike name (regexp dots) does not match" without_cosign run_verify

fresh_release
printf '%s  %s\n' "$(printf 'x' | { sha256sum 2>/dev/null || shasum -a 256; } | awk '{print $1}')" "${archive}" >> "${work}/release/checksums.txt"
expect fail "archive listed twice is refused" without_cosign run_verify

fresh_release; tr -d '\r' < "${work}/release/checksums.txt" | sed 's/$/\r/' > "${work}/c" && mv "${work}/c" "${work}/release/checksums.txt"
expect ok   "CRLF checksums.txt still verifies" without_cosign run_verify

fresh_release
expect fail "MAKTABA_REQUIRE_COSIGN=1 without cosign is refused" \
    env MAKTABA_REQUIRE_COSIGN=1 PATH="${nocosign_path}" bash -c "MAKTABA_INSTALL_LIB=1 . '${here}/../install.sh'; $(declare -f fetch); verify_release '${work}/dl' https://example.invalid/r v1.5.0 '${archive}'"

# A cosign that rejects the bundle is fatal.
mkdir -p "${work}/badcosign"
printf '#!/bin/sh\nexit 1\n' > "${work}/badcosign/cosign"
chmod +x "${work}/badcosign/cosign"
fresh_release
expect fail "cosign rejecting the signature is refused" env PATH="${work}/badcosign:${nocosign_path}" \
    bash -c "MAKTABA_INSTALL_LIB=1 . '${here}/../install.sh'; $(declare -f fetch); verify_release '${work}/dl' https://example.invalid/r v1.5.0 '${archive}'"

expect ok   "valid tag" valid_tag v1.5.0
expect ok   "valid prerelease tag" valid_tag v1.5.0-rc.1
for bad in 'latest' '1.5.0' 'v1.5' 'v1.5.0/../../x' 'v1.5.0;id' 'v1.5.0 x'; do
    expect fail "bad tag '${bad}' refused" valid_tag "${bad}"
done

# --app (Epic 39): the Linux payload's asset name per CPU
expect ok   "payload name for amd64" test "$(payload_archive 1.5.0 amd64)" = maktaba-linux-payload-1.5.0-x86_64.tar.gz
expect ok   "payload name for arm64" test "$(payload_archive 1.5.0 arm64)" = maktaba-linux-payload-1.5.0-aarch64.tar.gz
expect fail "no payload for another CPU" payload_archive 1.5.0 riscv64
fresh_release
pa="$(payload_archive 1.5.0 amd64)"
printf 'payload bytes
' > "${work}/release/${pa}"
cp "${work}/release/${pa}" "${work}/dl/${pa}"
printf '%s  ./%s
' "$(sha "${work}/release/${pa}")" "${pa}" >> "${work}/release/checksums.txt"
expect ok   "the payload verifies against checksums.txt" without_cosign verify_release "${work}/dl" https://example.invalid/r v1.5.0 "${pa}"

if [ "${fails}" -ne 0 ]; then
    echo "${fails} case(s) failed" >&2
    exit 1
fi
echo "install.sh verification: all cases passed"
