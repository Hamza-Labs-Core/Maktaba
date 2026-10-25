#!/usr/bin/env bash
# Checks pkg-repo.sh against fake server packages, with a throwaway signing key, in a
# temporary directory (nothing is published):
#   - the tree takes a release's packages and refuses one whose file changed; a .rpm goes in
#     signed with the repository key (its release SHA-256 beside it), and the same release
#     file again is kept;
#   - it keeps the three newest versions of each package (0.3.10 above 0.3.9);
#   - its indexes are signed by the key it publishes; apt's carry Valid-Until and by-hash
#     copies, so apt reads the repository while it is being published (the new indexes up, the
#     old InRelease still served), and refuses a changed index;
#   - dnf (Rocky Linux 8 and 9, Fedora 42, over HTTP, when docker is here) installs the newest
#     release through both signatures and refuses a tampered repository: an unsigned package
#     under re-made indexes, the old index signature left.
# REQUIRE=apt,rpm,dnf (CI) fails instead of skipping what this machine cannot run.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
pids=()
cleanup() {
    local p
    for p in "${pids[@]+"${pids[@]}"}"; do kill "${p}" 2>/dev/null || true; done
    rm -rf "${work}"
}
trap cleanup EXIT
repo="${work}/repo"
export GNUPGHOME="${work}/gnupg"
mkdir -p "${GNUPGHOME}" "${work}/pkgs"
chmod 0700 "${GNUPGHOME}"

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
skip() {
    case ",${REQUIRE:-}," in
        *",$1,"*) fail "$2 (required here)" ;;
        *) printf '  skip  %s\n' "$2" ;;
    esac
}
has() { command -v "$1" >/dev/null 2>&1; }
pkgrepo() { bash "${here}/pkg-repo.sh" "$@"; }
sha() { sha256sum "$1" | awk '{print $1}'; }

# the address dnf reads the repository at (an HTTP server on this machine, below)
free_port() { python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
port=8765
has python3 && port="$(free_port)"
export REPO_URL="http://127.0.0.1:${port}"

# the throwaway key, with a passphrase (the workflow's key may have one); RSA, which EL8's
# rpm reads
printf 'test-pass' >"${work}/pass"
gpg --batch --pinentry-mode loopback --passphrase-file "${work}/pass" \
    --quick-gen-key "Maktaba packages (test) <packages@invalid>" rsa3072 sign 1d 2>/dev/null
GPG_KEY="$(gpg --batch --with-colons --list-secret-keys 2>/dev/null | awk -F: '/^fpr/ {print $10; exit}')"
export GPG_KEY GPG_PASSPHRASE_FILE="${work}/pass"

# --- the fake packages, as the release names them ----------------------------------------
mkdeb() { # mkdeb VERSION ARCH
    local d="${work}/deb-$1-$2"
    mkdir -p "${d}/DEBIAN" "${d}/usr/share/doc/maktaba-server"
    printf 'Package: maktaba-server\nVersion: %s\nArchitecture: %s\nMaintainer: test <t@invalid>\nDescription: test\n' \
        "$1-1" "$2" >"${d}/DEBIAN/control"
    printf 'maktaba-server %s\n' "$1" >"${d}/usr/share/doc/maktaba-server/marker"
    dpkg-deb --root-owner-group -b "${d}" "${work}/pkgs/maktaba-server_$1-1_$2.deb" >/dev/null
}
mkrpm() { # mkrpm VERSION ARCH(amd64|arm64) OUT-DIR [MARKER]
    local d="${work}/rpm-$1-$2-${4:-release}" f
    mkdir -p "${d}/out"
    printf 'maktaba-server %s %s\n' "$1" "${4:-release}" >"${d}/marker"
    cat >"${d}/nfpm.yaml" <<EOF
name: maktaba-server
arch: $2
platform: linux
version: $1
release: "1"
maintainer: test <t@invalid>
description: test
license: MIT
contents:
  - src: ${d}/marker
    dst: /usr/share/doc/maktaba-server/marker
EOF
    nfpm package -f "${d}/nfpm.yaml" -p rpm -t "${d}/out" >/dev/null
    for f in "${d}"/out/*.rpm; do mv "${f}" "$3/"; done
}

rpms=false
if has nfpm && has createrepo_c && has rpmsign && has rpmkeys; then
    rpms=true
else
    skip rpm "the .rpm side (needs nfpm, createrepo_c and rpm's rpmsign)"
fi
has dpkg-deb || { echo "pkg-repo-test.sh: needs dpkg-deb" >&2; exit 1; }
has apt-ftparchive || { echo "pkg-repo-test.sh: needs apt-ftparchive (apt-utils)" >&2; exit 1; }

versions="0.3.5 0.3.6 0.3.9 0.3.10"
for v in ${versions}; do
    mkdeb "${v}" amd64
    mkdeb "${v}" arm64
    if [ "${rpms}" = true ]; then
        mkrpm "${v}" amd64 "${work}/pkgs"
        mkrpm "${v}" arm64 "${work}/pkgs"
    fi
done

echo "the tree"
for v in ${versions}; do
    pkgrepo add "${repo}" "${work}"/pkgs/*"${v}"-1[._]* >/dev/null
done
out="$(pkgrepo add "${repo}" "${work}/pkgs/maktaba-server_0.3.6-1_amd64.deb")"
if grep -q kept <<<"${out}"; then ok "a .deb added again is kept (the same file)"; else fail "added again: ${out}"; fi
mkdir -p "${work}/changed"
cp "${work}/pkgs/maktaba-server_0.3.6-1_amd64.deb" "${work}/changed/"
printf 'x' >>"${work}/changed/maktaba-server_0.3.6-1_amd64.deb"
if pkgrepo add "${repo}" "${work}/changed/maktaba-server_0.3.6-1_amd64.deb" >"${work}/log" 2>&1; then
    fail "a package whose file changed was taken"
elif grep -q "never changes" "${work}/log"; then
    ok "a package whose file changed is refused"
else
    fail "refused, but not for the file: $(cat "${work}/log")"
fi
if pkgrepo add "${repo}" "${work}/pass" >/dev/null 2>&1; then fail "a file that is no package was taken"; else ok "a file that is no package is refused"; fi
if [ "${rpms}" = true ]; then
    r="maktaba-server-0.3.10-1.x86_64.rpm"
    if ! cmp -s "${work}/pkgs/${r}" "${repo}/rpm/packages/${r}" &&
        [ "$(cat "${repo}/rpm/packages/${r}.sha256")" = "$(sha "${work}/pkgs/${r}")" ]; then
        ok "a .rpm goes in signed, the release file's SHA-256 beside it"
    else
        fail "the .rpm in the tree is not the signed release file"
    fi
    out="$(pkgrepo add "${repo}" "${work}/pkgs/${r}")"
    if grep -q kept <<<"${out}"; then ok "the same release .rpm again is kept (its signed copy stays)"; else fail ".rpm added again: ${out}"; fi
    cp "${work}/pkgs/${r}" "${work}/changed/"
    printf 'x' >>"${work}/changed/${r}"
    if pkgrepo add "${repo}" "${work}/changed/${r}" >/dev/null 2>&1; then fail "a .rpm whose file changed was taken"; else ok "a .rpm whose file changed is refused"; fi
fi

KEEP=3 pkgrepo prune "${repo}" >/dev/null
left="$(cd "${repo}/apt/pool/main" && ls)"
if ! grep -q "_0.3.5" <<<"${left}" && grep -q "maktaba-server_0.3.10-1_amd64.deb" <<<"${left}" &&
    [ "$(grep -c '^maktaba-server_.*_amd64.deb$' <<<"${left}")" = 3 ] &&
    [ "$(grep -c '^maktaba-server_.*_arm64.deb$' <<<"${left}")" = 3 ]; then
    ok "prune keeps the three newest versions of each package and architecture (0.3.10 above 0.3.9)"
else
    fail "prune left: ${left}"
fi
if [ "${rpms}" = true ]; then
    left="$(cd "${repo}/rpm/packages" && ls)"
    if ! grep -q -- "-0.3.5-" <<<"${left}" && [ "$(grep -c '^maktaba-server-.*x86_64.rpm$' <<<"${left}")" = 3 ]; then
        ok "prune keeps the three newest .rpm versions, and their SHA-256 files alone"
    else
        fail "prune left (.rpm): ${left}"
    fi
fi

echo "the indexes"
formats="apt"
[ "${rpms}" = true ] && formats="apt rpm"
export FORMATS="${formats}"
pkgrepo index "${repo}" >/dev/null
dist="${repo}/apt/dists/stable"
first="$(cat "${dist}/main/binary-amd64/Packages")"
first_sum="$(sha "${dist}/main/binary-amd64/Packages.gz")"
pkgrepo index "${repo}" >/dev/null
if [ "${first}" = "$(cat "${dist}/main/binary-amd64/Packages")" ]; then
    ok "indexing again writes the same package index"
else
    fail "a second index differs"
fi
if gpgv --keyring "${repo}/maktaba.gpg" "${dist}/InRelease" >/dev/null 2>&1 &&
    gpgv --keyring "${repo}/maktaba.gpg" "${dist}/Release.gpg" "${dist}/Release" >/dev/null 2>&1; then
    ok "InRelease and Release.gpg are signed by the published key"
else
    fail "the apt index's signature does not check against maktaba.gpg"
fi
until="$(sed -n 's/^Valid-Until: //p' "${dist}/Release")"
if [ -n "${until}" ] && [ "$(date -u -d "${until}" +%s)" -gt "$(date -u -d '+13 days' +%s)" ] &&
    [ "$(date -u -d "${until}" +%s)" -lt "$(date -u -d '+15 days' +%s)" ]; then
    ok "Release is valid for 14 days (Valid-Until: ${until}), signed again on every run"
else
    fail "Valid-Until: '${until}'"
fi
if grep -qx "Acquire-By-Hash: yes" "${dist}/Release" && ! grep -q "^MD5Sum:" "${dist}/Release" &&
    [ -f "${dist}/main/binary-amd64/by-hash/SHA256/$(sha "${dist}/main/binary-amd64/Packages.gz")" ] &&
    [ -f "${dist}/main/binary-amd64/by-hash/SHA256/${first_sum}" ] &&
    ! grep -q "by-hash" "${dist}/Release"; then
    ok "the indexes are under by-hash/ too (Acquire-By-Hash), the earlier runs' copies kept"
else
    fail "by-hash: $(grep -c . "${dist}/Release") lines; $(find "${dist}" -path '*by-hash*' -type f | wc -l) copies"
fi
if grep -q "BEGIN PGP PRIVATE" "${repo}/maktaba.asc" || gpg --batch --list-packets "${repo}/maktaba.gpg" 2>/dev/null | grep -q "secret key packet"; then
    fail "the published key holds the secret key"
else
    ok "only the public key is published"
fi
for a in amd64 arm64; do
    if grep -q "^Architecture: ${a}$" "${dist}/main/binary-${a}/Packages" &&
        ! grep -q "^Architecture: " <(grep -v "^Architecture: ${a}$" "${dist}/main/binary-${a}/Packages"); then
        ok "binary-${a} lists the ${a} packages alone"
    else
        fail "binary-${a}: $(grep '^Architecture' "${dist}/main/binary-${a}/Packages" | sort | uniq -c)"
    fi
done
if grep -qx "deb \[signed-by=/usr/share/keyrings/maktaba.gpg\] ${REPO_URL}/apt stable main" "${repo}/apt/maktaba.list" &&
    ! grep -q "apt install maktaba$\|dnf install maktaba$" "${repo}/index.html"; then
    ok "maktaba.list names the key and the address; the page offers the server alone"
else
    fail "maktaba.list: $(cat "${repo}/apt/maktaba.list")"
fi
if [ "${rpms}" = true ]; then
    if gpg --batch --verify "${repo}/rpm/repodata/repomd.xml.asc" "${repo}/rpm/repodata/repomd.xml" >/dev/null 2>&1; then
        ok "repomd.xml is signed"
    else
        fail "repomd.xml.asc does not check"
    fi
    if grep -q "^gpgcheck=1$" "${repo}/rpm/maktaba.repo" && grep -q "^repo_gpgcheck=1$" "${repo}/rpm/maktaba.repo" &&
        grep -q "^gpgkey=${REPO_URL}/maktaba.asc$" "${repo}/rpm/maktaba.repo"; then
        ok "maktaba.repo checks both signatures with the published key"
    else
        fail "maktaba.repo: $(cat "${repo}/rpm/maktaba.repo")"
    fi
    mkdir -p "${work}/rpmdb"
    rpmkeys --dbpath "${work}/rpmdb" --import "${repo}/maktaba.asc"
    bad=""
    for f in "${repo}"/rpm/packages/*.rpm; do
        rpmkeys --dbpath "${work}/rpmdb" --checksig "${f}" 2>&1 | grep -qi "signatures OK\|pgp.*OK" || bad="${bad} ${f##*/}"
    done
    if [ -z "${bad}" ]; then ok "every .rpm checks against the published key"; else fail "unsigned:${bad}"; fi
fi

echo "apt"
if has apt-get; then
    a="${work}/apt"
    served="${work}/served"
    mkdir -p "${a}/etc/preferences.d" "${a}/etc/trusted.gpg.d" "${a}/cache/archives/partial"
    : >"${a}/status"
    printf 'deb [signed-by=%s] file://%s/apt stable main\n' "${repo}/maktaba.gpg" "${served}" >"${a}/sources.list"
    aptopts=(-o "Dir::Etc::SourceList=${a}/sources.list" -o "Dir::Etc::SourceParts=${a}/none"
        -o "Dir::Etc::Preferences=${a}/none" -o "Dir::Etc::PreferencesParts=${a}/etc/preferences.d"
        -o "Dir::Etc::Trusted=${a}/none" -o "Dir::Etc::TrustedParts=${a}/etc/trusted.gpg.d"
        -o "Dir::State::Lists=${a}/lists" -o "Dir::State::status=${a}/status" -o "Dir::Cache=${a}/cache"
        -o "APT::Architecture=amd64" -o "APT::Architectures=amd64" -o "Debug::NoLocking=1" -o "APT::Sandbox::User=$(id -un)")
    fresh() { rm -rf "${a}/lists"; mkdir -p "${a}/lists/partial"; }
    update() { apt-get "${aptopts[@]}" update >"${work}/log" 2>&1 && ! grep -q "^E:" "${work}/log"; }
    cand() { apt-cache "${aptopts[@]}" policy maktaba-server 2>/dev/null | awk '/Candidate:/ {print $2}'; }
    cp -r "${repo}" "${served}"
    fresh
    if update; then ok "apt reads the repository through its signed index"; else fail "apt-get update: $(tail -5 "${work}/log")"; fi
    if [ "$(cand)" = "0.3.10-1" ]; then ok "apt's candidate is the newest release (0.3.10-1)"; else fail "candidate: $(cand)"; fi
    n="$(apt-cache "${aptopts[@]}" madison maktaba-server 2>/dev/null | wc -l)"
    if [ "${n}" = 3 ]; then ok "apt sees the three versions kept"; else fail "apt sees ${n} versions"; fi
    if (cd "${work}" && apt-get "${aptopts[@]}" download maktaba-server >/dev/null 2>&1) &&
        ls "${work}"/maktaba-server_0.3.10-1_amd64.deb >/dev/null 2>&1; then
        ok "apt fetches the package and checks its hash"
    else
        fail "apt-get download maktaba-server"
    fi
    # The next release while it is being published: its packages and indexes are up, the old
    # InRelease is still served (packages.yml uploads InRelease last). A machine that took the
    # old InRelease reads the indexes it names from by-hash/, then the new release.
    mkdeb 0.3.11 amd64
    pkgrepo add "${repo}" "${work}/pkgs/maktaba-server_0.3.11-1_amd64.deb" >/dev/null
    pkgrepo index "${repo}" >/dev/null
    # shellcheck disable=SC2016 # the inner shell's own arguments
    (cd "${repo}" && find apt -type f ! -name InRelease ! -name Release ! -name Release.gpg -print0 |
        xargs -0 -I{} sh -c 'mkdir -p "$(dirname "$2/$1")" && cp "$1" "$2/$1"' _ {} "${served}")
    fresh
    if update && [ "$(cand)" = "0.3.10-1" ]; then
        ok "while the next release is published (indexes up, InRelease not yet), apt still reads the repository"
    else
        fail "mid-publish: $(tail -3 "${work}/log"); candidate $(cand)"
    fi
    cp "${dist}/InRelease" "${dist}/Release" "${dist}/Release.gpg" "${served}/apt/dists/stable/"
    fresh
    if update && [ "$(cand)" = "0.3.11-1" ]; then ok "then its InRelease, and apt takes it"; else fail "after the publish: $(cand)"; fi
    # an index its signature does not cover is refused (every form of it changed: apt would
    # take another compression's, or another digest's copy)
    # shellcheck disable=SC2016 # the inner shell's own argument
    find "${served}/apt/dists/stable/main/binary-amd64" -type f -print0 | xargs -0 -I{} sh -c 'printf x >>"$1"' _ {}
    fresh
    if update; then fail "apt took an index its signature does not cover"; else ok "apt refuses an index its signature does not cover"; fi
else
    skip apt "apt itself (no apt-get here)"
fi

echo "dnf"
if [ "${rpms}" = true ] && has docker && docker info >/dev/null 2>&1 && has python3; then
    # the repository as published, and a tampered copy: the newest x86_64 package swapped for
    # an unsigned one, the indexes made again to match it, the old index signature left
    tampered="${work}/tampered"
    cp -r "${repo}" "${tampered}"
    mkdir -p "${work}/attacker"
    mkrpm 0.3.10 amd64 "${work}/attacker" attacker
    cp "${work}/attacker/maktaba-server-0.3.10-1.x86_64.rpm" "${tampered}/rpm/packages/"
    createrepo_c --quiet --update --checksum sha256 --general-compress-type gz "${tampered}/rpm"
    cp "${repo}/rpm/repodata/repomd.xml.asc" "${tampered}/rpm/repodata/repomd.xml.asc"
    tport="$(free_port)"
    (cd "${repo}" && exec python3 -m http.server "${port}" --bind 127.0.0.1 >/dev/null 2>&1) &
    pids+=("$!")
    (cd "${tampered}" && exec python3 -m http.server "${tport}" --bind 127.0.0.1 >/dev/null 2>&1) &
    pids+=("$!")
    sleep 1
    sed "s|127.0.0.1:${port}|127.0.0.1:${tport}|" "${repo}/rpm/maktaba.repo" >"${work}/tampered.repo"
    for image in rockylinux:8 rockylinux:9 fedora:42; do
        if docker run --rm --network host -v "${repo}/rpm/maktaba.repo:/repos/good.repo:ro" \
            -v "${work}/tampered.repo:/repos/tampered.repo:ro" "${image}" bash -c '
            set -u
            dnf_=(dnf -y --disablerepo="*" --enablerepo=maktaba)
            dnf4=false
            dnf --version 2>/dev/null | head -1 | grep -q "^4\." && dnf4=true
            echo "== $(dnf --version 2>/dev/null | head -1)"
            cp /repos/good.repo /etc/yum.repos.d/maktaba.repo
            "${dnf_[@]}" --refresh makecache || { echo "makecache of the repository failed"; exit 1; }
            n="$("${dnf_[@]}" -q list --showduplicates --available maktaba-server 2>/dev/null | grep -c "^maktaba-server\.$(uname -m) ")"
            [ "$n" = 3 ] || { echo "dnf sees $n versions for $(uname -m)"; exit 1; }
            "${dnf_[@]}" install maktaba-server || { echo "install failed"; exit 1; }
            rpm -q maktaba-server | grep -q "0.3.10-1" || { echo "installed $(rpm -q maktaba-server)"; exit 1; }
            grep -q release /usr/share/doc/maktaba-server/marker || { echo "not the release package"; exit 1; }
            "${dnf_[@]}" -q remove maktaba-server >/dev/null
            echo "== the tampered repository"
            dnf clean all >/dev/null 2>&1
            cp /repos/tampered.repo /etc/yum.repos.d/maktaba.repo
            "${dnf_[@]}" --refresh makecache; mc=$?
            echo "== makecache: exit $mc"
            if [ "$mc" = 0 ]; then
                "${dnf_[@]}" install maktaba-server; in=$?
                echo "== install: exit $in"
            fi
            if rpm -q maktaba-server >/dev/null 2>&1; then
                echo "the tampered package was installed: $(cat /usr/share/doc/maktaba-server/marker)"; exit 1
            fi
            if [ "$dnf4" = true ] && [ "$mc" = 0 ]; then
                echo "dnf 4 took the index its signature does not cover"; exit 1
            fi
            echo "== refused"' >"${work}/log" 2>&1; then
            ok "${image}: dnf installs the newest release through both signatures and refuses a tampered repository"
            sed -n 's/^== /      /p' "${work}/log"
        else
            fail "${image}:"
            sed 's/^/      /' "${work}/log" | tail -40
        fi
    done
else
    skip dnf "dnf itself (needs the .rpm side, docker and python3)"
fi

echo
if [ "${fails}" -gt 0 ]; then
    echo "pkg-repo-test.sh: ${fails} failed"
    exit 1
fi
echo "pkg-repo-test.sh: all passed"
