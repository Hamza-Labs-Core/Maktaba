#!/usr/bin/env bash
# Checks pkg-repo.sh against fake packages, with a throwaway signing key, in a temporary
# directory (nothing is published): the tree takes a release's packages and refuses one
# whose bytes changed, keeps the three newest versions of each package (0.3.10 above 0.3.9),
# and its indexes are signed by the key it publishes. apt (when apt-get is here) installs
# from it as a machine does, through the signed index, and refuses a changed index; dnf
# (in Rocky Linux 9 and Fedora containers, when docker is here) the same. REQUIRE=rpm,dnf
# (CI) fails instead of skipping what this machine cannot run.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT
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

# the throwaway key, with a passphrase (the workflow's key may have one)
printf 'test-pass' >"${work}/pass"
gpg --batch --pinentry-mode loopback --passphrase-file "${work}/pass" \
    --quick-gen-key "Maktaba packages (test) <packages@invalid>" rsa3072 sign 1d 2>/dev/null
GPG_KEY="$(gpg --batch --with-colons --list-secret-keys 2>/dev/null | awk -F: '/^fpr/ {print $10; exit}')"
export GPG_KEY GPG_PASSPHRASE_FILE="${work}/pass"

# --- the fake packages: the server's and the app's, as the release names them ---------
mkdeb() { # mkdeb PACKAGE VERSION ARCH FILE
    local d="${work}/deb-$1-$2-$3"
    mkdir -p "${d}/DEBIAN" "${d}/usr/share/doc/$1"
    printf 'Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: test <t@invalid>\nDescription: test\n' \
        "$1" "$2" "$3" >"${d}/DEBIAN/control"
    printf '%s %s\n' "$1" "$2" >"${d}/usr/share/doc/$1/marker"
    dpkg-deb --root-owner-group -b "${d}" "${work}/pkgs/$4" >/dev/null
}
mkrpm() { # mkrpm NAME VERSION ARCH(amd64|arm64) FILE
    local d="${work}/rpm-$1-$2-$3"
    mkdir -p "${d}/out"
    printf '%s %s\n' "$1" "$2" >"${d}/marker"
    cat >"${d}/nfpm.yaml" <<EOF
name: $1
arch: $3
platform: linux
version: $2
release: "1"
maintainer: test <t@invalid>
description: test
license: MIT
contents:
  - src: ${d}/marker
    dst: /usr/share/doc/$1/marker
EOF
    nfpm package -f "${d}/nfpm.yaml" -p rpm -t "${d}/out" >/dev/null
    mv "${d}"/out/*.rpm "${work}/pkgs/$4"
}

rpms=false
if has nfpm && has createrepo_c; then rpms=true; else skip rpm "the .rpm side (needs nfpm and createrepo_c)"; fi
has dpkg-deb || { echo "pkg-repo-test.sh: needs dpkg-deb" >&2; exit 1; }
has apt-ftparchive || { echo "pkg-repo-test.sh: needs apt-ftparchive (apt-utils)" >&2; exit 1; }

versions="0.3.5 0.3.6 0.3.9 0.3.10"
for v in ${versions}; do
    mkdeb maktaba-server "${v}-1" amd64 "maktaba-server_${v}-1_amd64.deb"
    mkdeb maktaba-server "${v}-1" arm64 "maktaba-server_${v}-1_arm64.deb"
    mkdeb maktaba "${v}" amd64 "Maktaba_${v}_amd64.deb"
    if [ "${rpms}" = true ]; then
        mkrpm maktaba-server "${v}" amd64 "maktaba-server-${v}-1.x86_64.rpm"
        mkrpm maktaba-server "${v}" arm64 "maktaba-server-${v}-1.aarch64.rpm"
        mkrpm Maktaba "${v}" amd64 "Maktaba-${v}-1.x86_64.rpm"
    fi
done

echo "the tree"
for v in ${versions}; do
    pkgrepo add "${repo}" "${work}"/pkgs/*"${v}"[-_]* >/dev/null
done
out="$(pkgrepo add "${repo}" "${work}/pkgs/maktaba-server_0.3.6-1_amd64.deb")"
if grep -q kept <<<"${out}"; then ok "a package added again is kept (the same bytes)"; else fail "added again: ${out}"; fi
cp "${work}/pkgs/maktaba-server_0.3.6-1_amd64.deb" "${work}/changed.deb"
printf 'x' >>"${work}/changed.deb"
mkdir -p "${work}/changed"
mv "${work}/changed.deb" "${work}/changed/maktaba-server_0.3.6-1_amd64.deb"
if pkgrepo add "${repo}" "${work}/changed/maktaba-server_0.3.6-1_amd64.deb" >"${work}/log" 2>&1; then
    fail "a package whose bytes changed was taken"
elif grep -q "never changes" "${work}/log"; then
    ok "a package whose bytes changed is refused"
else
    fail "refused, but not for the bytes: $(cat "${work}/log")"
fi
if pkgrepo add "${repo}" "${work}/pass" >/dev/null 2>&1; then fail "a file that is no package was taken"; else ok "a file that is no package is refused"; fi

KEEP=3 pkgrepo prune "${repo}" >/dev/null
left="$(cd "${repo}/apt/pool/main" && ls)"
if ! grep -q "_0.3.5" <<<"${left}" && grep -q "maktaba-server_0.3.10-1_amd64.deb" <<<"${left}" &&
    [ "$(grep -c '^maktaba-server_.*_amd64.deb$' <<<"${left}")" = 3 ] &&
    [ "$(grep -c '^Maktaba_.*_amd64.deb$' <<<"${left}")" = 3 ]; then
    ok "prune keeps the three newest versions of each package and architecture (0.3.10 above 0.3.9)"
else
    fail "prune left: ${left}"
fi
if [ "${rpms}" = true ]; then
    left="$(cd "${repo}/rpm/packages" && ls)"
    if ! grep -q -- "-0.3.5-" <<<"${left}" && [ "$(grep -c '^maktaba-server-.*x86_64.rpm$' <<<"${left}")" = 3 ]; then
        ok "prune keeps the three newest .rpm versions"
    else
        fail "prune left (.rpm): ${left}"
    fi
fi

echo "the indexes"
formats="apt"
[ "${rpms}" = true ] && formats="apt rpm"
FORMATS="${formats}" REPO_URL="file:///repo" pkgrepo index "${repo}" >/dev/null
first="$(cat "${repo}/apt/dists/stable/main/binary-amd64/Packages")"
FORMATS="${formats}" REPO_URL="file:///repo" pkgrepo index "${repo}" >/dev/null
if [ "${first}" = "$(cat "${repo}/apt/dists/stable/main/binary-amd64/Packages")" ]; then
    ok "indexing again writes the same package index"
else
    fail "a second index differs"
fi
if gpgv --keyring "${repo}/maktaba.gpg" "${repo}/apt/dists/stable/InRelease" >/dev/null 2>&1 &&
    gpgv --keyring "${repo}/maktaba.gpg" "${repo}/apt/dists/stable/Release.gpg" "${repo}/apt/dists/stable/Release" >/dev/null 2>&1; then
    ok "InRelease and Release.gpg are signed by the published key"
else
    fail "the apt index's signature does not check against maktaba.gpg"
fi
if grep -q "BEGIN PGP PRIVATE" "${repo}/maktaba.asc" || gpg --batch --list-packets "${repo}/maktaba.gpg" 2>/dev/null | grep -q "secret key packet"; then
    fail "the published key holds the secret key"
else
    ok "only the public key is published"
fi
for a in amd64 arm64; do
    if grep -q "^Architecture: ${a}$" "${repo}/apt/dists/stable/main/binary-${a}/Packages" &&
        ! grep -q "^Architecture: " <(grep -v "^Architecture: ${a}$" "${repo}/apt/dists/stable/main/binary-${a}/Packages"); then
        ok "binary-${a} lists the ${a} packages alone"
    else
        fail "binary-${a}: $(grep '^Architecture' "${repo}/apt/dists/stable/main/binary-${a}/Packages" | sort | uniq -c)"
    fi
done
if grep -qx "deb \[signed-by=/usr/share/keyrings/maktaba.gpg\] file:///repo/apt stable main" "${repo}/apt/maktaba.list"; then
    ok "maktaba.list names the key and the address"
else
    fail "maktaba.list: $(cat "${repo}/apt/maktaba.list")"
fi
if [ "${rpms}" = true ]; then
    if gpg --batch --verify "${repo}/rpm/repodata/repomd.xml.asc" "${repo}/rpm/repodata/repomd.xml" >/dev/null 2>&1; then
        ok "repomd.xml is signed"
    else
        fail "repomd.xml.asc does not check"
    fi
    if grep -q "^repo_gpgcheck=1$" "${repo}/rpm/maktaba.repo" && grep -q "^gpgkey=file:///repo/maktaba.asc$" "${repo}/rpm/maktaba.repo"; then
        ok "maktaba.repo checks the signed index with the published key"
    else
        fail "maktaba.repo: $(cat "${repo}/rpm/maktaba.repo")"
    fi
fi

echo "apt"
if has apt-get; then
    a="${work}/apt"
    mkdir -p "${a}/lists/partial" "${a}/cache/archives/partial" "${a}/etc/preferences.d" "${a}/etc/trusted.gpg.d"
    : >"${a}/status"
    printf 'deb [signed-by=%s] file://%s/apt stable main\n' "${repo}/maktaba.gpg" "${repo}" >"${a}/sources.list"
    aptopts=(-o "Dir::Etc::SourceList=${a}/sources.list" -o "Dir::Etc::SourceParts=${a}/none"
        -o "Dir::Etc::Preferences=${a}/none" -o "Dir::Etc::PreferencesParts=${a}/etc/preferences.d"
        -o "Dir::Etc::Trusted=${a}/none" -o "Dir::Etc::TrustedParts=${a}/etc/trusted.gpg.d"
        -o "Dir::State::Lists=${a}/lists" -o "Dir::State::status=${a}/status" -o "Dir::Cache=${a}/cache"
        -o "APT::Architecture=amd64" -o "APT::Architectures=amd64" -o "Debug::NoLocking=1" -o "APT::Sandbox::User=$(id -un)")
    if apt-get "${aptopts[@]}" update >"${work}/log" 2>&1; then
        ok "apt reads the repository through its signed index"
    else
        fail "apt-get update: $(tail -5 "${work}/log")"
    fi
    cand="$(apt-cache "${aptopts[@]}" policy maktaba-server 2>/dev/null | awk '/Candidate:/ {print $2}')"
    if [ "${cand}" = "0.3.10-1" ]; then ok "apt's candidate is the newest release (0.3.10-1)"; else fail "candidate: ${cand:-none}"; fi
    n="$(apt-cache "${aptopts[@]}" madison maktaba-server 2>/dev/null | wc -l)"
    if [ "${n}" = 3 ]; then ok "apt sees the three versions kept"; else fail "apt sees ${n} versions"; fi
    if (cd "${work}" && apt-get "${aptopts[@]}" download maktaba >/dev/null 2>&1) && ls "${work}"/[Mm]aktaba_0.3.10_amd64.deb >/dev/null 2>&1; then
        ok "apt fetches the app's package and checks its hash"
    else
        fail "apt-get download maktaba"
    fi
    # a changed index (Packages no longer the one InRelease signed) is refused
    printf '\n' >>"${repo}/apt/dists/stable/main/binary-amd64/Packages"
    gzip -9 -n -c "${repo}/apt/dists/stable/main/binary-amd64/Packages" >"${repo}/apt/dists/stable/main/binary-amd64/Packages.gz"
    rm -rf "${a}/lists"
    mkdir -p "${a}/lists/partial"
    if apt-get "${aptopts[@]}" update >"${work}/log" 2>&1 && ! grep -q "^E:" "${work}/log"; then
        fail "apt took an index its signature does not cover"
    else
        ok "apt refuses an index its signature does not cover"
    fi
    FORMATS="${formats}" REPO_URL="file:///repo" pkgrepo index "${repo}" >/dev/null
else
    skip apt "apt itself (no apt-get here)"
fi

echo "dnf"
if [ "${rpms}" = true ] && has docker && docker info >/dev/null 2>&1; then
    for image in rockylinux:9 fedora:42; do
        # the repository file as published (file:///repo), the index as signed, then changed
        if docker run --rm -v "${repo}:/repo:ro" "${image}" bash -c '
            set -e
            cp /repo/rpm/maktaba.repo /etc/yum.repos.d/maktaba.repo
            dnf -q -y --disablerepo="*" --enablerepo=maktaba makecache
            # the container architecture only: the repository carries the other one too
            n="$(dnf -q --disablerepo="*" --enablerepo=maktaba list --showduplicates --available maktaba-server 2>/dev/null | grep -c "^maktaba-server\.$(uname -m) ")"
            [ "$n" = 3 ] || { echo "dnf sees $n versions for $(uname -m)" >&2; exit 1; }
            dnf -q -y --disablerepo="*" --enablerepo=maktaba install maktaba-server >/dev/null
            rpm -q maktaba-server | grep -q "0.3.10-1" || { echo "installed $(rpm -q maktaba-server)" >&2; exit 1; }
            cp -r /repo /tmp/repo
            sed -i "s/<revision>/<revision>1/" /tmp/repo/rpm/repodata/repomd.xml
            sed "s|file:///repo|file:///tmp/repo|" /repo/rpm/maktaba.repo >/etc/yum.repos.d/maktaba.repo
            dnf -q clean all >/dev/null
            if dnf -q -y --disablerepo="*" --enablerepo=maktaba makecache >/dev/null 2>&1; then
                echo "dnf took a changed repomd.xml" >&2; exit 1
            fi' >"${work}/log" 2>&1; then
            ok "${image}: dnf installs the newest release through the signed index and refuses a changed one"
        else
            fail "${image}: $(tail -5 "${work}/log")"
        fi
    done
else
    skip dnf "dnf itself (needs the .rpm side and docker)"
fi

echo
if [ "${fails}" -gt 0 ]; then
    echo "pkg-repo-test.sh: ${fails} failed"
    exit 1
fi
echo "pkg-repo-test.sh: all passed"
