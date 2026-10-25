#!/usr/bin/env bash
# The Maktaba package repositories: an apt repository and a dnf/yum repository holding the
# server's .deb and .rpm packages (maktaba-server) of the stable releases. packages.yml
# brings them up to the newest stable release, from that release's own packages once they
# are checked against its signature; the tree is static, and packages.yml syncs it to the
# bucket the repositories' address serves. The Linux app is not here (Epic 39: one updater,
# the app's).
#
#   pkg-repo.sh add   ROOT FILE...   put each .deb / .rpm in the tree: a .deb as the release's
#                                    bytes, a .rpm signed with the repository key (the
#                                    release's SHA-256 kept beside it). One already there must
#                                    be the same release file: a published package never
#                                    changes
#   pkg-repo.sh prune ROOT           keep the KEEP newest versions of each package and
#                                    architecture (KEEP, default 3)
#   pkg-repo.sh index ROOT           write the indexes and sign them (signed again on every
#                                    run: apt's expire), with the public key, the source files
#                                    and a page that says how to add them
#
# ROOT holds:
#   maktaba.gpg, maktaba.asc          the repository's public key (binary for apt's signed-by,
#                                     armored for dnf's gpgkey)
#   apt/pool/main/<file>.deb          the packages
#   apt/dists/stable/                 Release, Release.gpg, InRelease (valid for VALID_DAYS)
#                                     and main/binary-<amd64|arm64>/Packages(.gz), each also
#                                     under by-hash/ (Acquire-By-Hash: a machine reading while
#                                     the repository changes finds what its InRelease names)
#   apt/maktaba.list                  the source line, for /etc/apt/sources.list.d/
#   rpm/packages/<file>.rpm           the packages, signed; <file>.rpm.sha256 the release's
#                                     own file's SHA-256
#   rpm/repodata/                     createrepo_c's indexes (gzip: EL8 reads them);
#                                     repomd.xml.asc signs them
#   rpm/maktaba.repo                  the repository file, for /etc/yum.repos.d/ (gpgcheck and
#                                     repo_gpgcheck)
#   index.html                        how to add them
#
# Env: REPO_URL (index: the public address, written into the source files and the page);
# GPG_KEY (add of a .rpm, index: the signing key's fingerprint, in GNUPGHOME);
# GPG_PASSPHRASE_FILE (optional); KEEP (prune, default 3); VALID_DAYS (index, default 14);
# FORMATS (index: "apt rpm", the default, or one).
# Needs apt-ftparchive (apt-utils), createrepo_c (createrepo-c), rpmsign (rpm), gpg, gzip,
# sha256sum, sha512sum, sort -V.
set -euo pipefail

die() { echo "pkg-repo.sh: $*" >&2; exit 1; }

cmd="${1:-}"
root="${2:-}"
if [ -z "${cmd}" ] || [ -z "${root}" ]; then die "usage: pkg-repo.sh add|prune|index ROOT [FILE...]"; fi
shift 2

# ident FILE: "<name> <arch> <version>" from a package's file name, as nfpm names them
# (name_version_arch.deb; name-version-release.arch.rpm); nothing for another name
ident() {
    local base="${1##*/}" stem nvr nv
    case "${base}" in
        *.deb)
            stem="${base%.deb}"
            case "${stem}" in *_*_*) ;; *) return 0 ;; esac
            # name, architecture, version: the version may hold no "_" either
            printf '%s %s %s\n' "${stem%%_*}" "${stem##*_}" "$(stem="${stem#*_}"; printf '%s' "${stem%_*}")"
            ;;
        *.rpm)
            stem="${base%.rpm}"
            nvr="${stem%.*}"
            nv="${nvr%-*}"
            case "${nv}" in *-*) ;; *) return 0 ;; esac
            printf '%s %s %s\n' "${nv%-*}" "${stem##*.}" "${nv##*-}-${nvr##*-}"
            ;;
    esac
}

# safe NAME: a package file name the tree takes (letters, digits, . _ + ~ -)
safe() {
    case "$1" in
        ""|.*|*[!A-Za-z0-9._+~-]*) return 1 ;;
    esac
    return 0
}

sha256() { sha256sum "$1" | awk '{print $1}'; }

# gpg_extra: what gpg needs here besides the key (batch, and the passphrase when there is one)
gpg_extra() {
    printf '%s' "--batch --no-tty"
    if [ -n "${GPG_PASSPHRASE_FILE:-}" ]; then
        printf ' %s' "--pinentry-mode loopback --passphrase-file ${GPG_PASSPHRASE_FILE}"
    fi
}

# sign_rpm FILE: signs a .rpm in place with the repository key (a header signature, SHA-256),
# which dnf checks (gpgcheck=1) against the key the repository publishes
sign_rpm() {
    rpmsign --addsign \
        --define "_gpg_name ${GPG_KEY:?GPG_KEY: the signing key}" \
        --define "_gpg_digest_algo sha256" \
        --define "_gpg_sign_cmd_extra_args $(gpg_extra)" \
        "$1" >/dev/null
}

add() {
    local f base dst want have
    [ "$#" -gt 0 ] || die "add: no package"
    for f in "$@"; do
        base="${f##*/}"
        safe "${base}" || die "add: ${base}: not a package file name"
        [ -n "$(ident "${base}")" ] || die "add: ${base}: not a name_version_arch.deb or name-version-release.arch.rpm"
        case "${base}" in
            *.deb) dst="${root}/apt/pool/main/${base}" ;;
            *.rpm) dst="${root}/rpm/packages/${base}" ;;
        esac
        mkdir -p "${dst%/*}"
        want="$(sha256 "${f}")"
        if [ -e "${dst}" ]; then
            case "${base}" in
                *.deb) have="$(sha256 "${dst}")" ;;
                *.rpm) have="$(cat "${dst}.sha256" 2>/dev/null || true)" ;;
            esac
            [ "${have}" = "${want}" ] ||
                die "add: ${base} is in the repository from another file: a published package never changes"
            echo "kept ${base} (there already)"
            continue
        fi
        cp "${f}" "${dst}.part"
        if [ "${base##*.}" = rpm ]; then
            sign_rpm "${dst}.part"
            printf '%s\n' "${want}" >"${dst}.sha256"
        fi
        mv "${dst}.part" "${dst}"
        echo "added ${base}"
    done
}

prune() {
    local keep="${KEEP:-3}" dir f id groups group n
    case "${keep}" in ""|*[!0-9]*|0) die "prune: KEEP must be a positive number" ;; esac
    for dir in "${root}/apt/pool/main" "${root}/rpm/packages"; do
        [ -d "${dir}" ] || continue
        # "<name> <arch>" groups, then each group's files newest first
        groups="$(for f in "${dir}"/*.deb "${dir}"/*.rpm; do
            [ -f "${f}" ] || continue
            id="$(ident "${f}")"
            [ -n "${id}" ] && printf '%s\n' "${id% *}"
        done | sort -u)"
        [ -n "${groups}" ] || continue
        while read -r group; do
            n=0
            while read -r _ f; do
                n=$((n + 1))
                if [ "${n}" -gt "${keep}" ]; then
                    rm -f "${f}" "${f}.sha256"
                    echo "pruned ${f##*/}"
                fi
            done < <(for f in "${dir}"/*.deb "${dir}"/*.rpm; do
                [ -f "${f}" ] || continue
                id="$(ident "${f}")"
                [ "${id% *}" = "${group}" ] && printf '%s %s\n' "${id##* }" "${f}"
            done | sort -V -r -k1,1)
        done <<<"${groups}"
    done
}

# sign GPG-ARGS...: the repository key signs (a passphrase from GPG_PASSPHRASE_FILE)
sign() {
    local args=(--batch --yes --local-user "${GPG_KEY:?GPG_KEY: the signing key}" --digest-algo SHA512)
    if [ -n "${GPG_PASSPHRASE_FILE:-}" ]; then
        args+=(--pinentry-mode loopback --passphrase-file "${GPG_PASSPHRASE_FILE}")
    fi
    gpg "${args[@]}" "$@"
}

# by_hash DIR: each index file of DIR also under DIR/by-hash/<SHA256|SHA512>/<digest>; the
# copies of the last five runs stay (a machine that read an earlier InRelease finds them)
by_hash() {
    local dir="$1" f kind sum
    for f in "${dir}"/Packages "${dir}"/Packages.gz; do
        for kind in SHA256 SHA512; do
            mkdir -p "${dir}/by-hash/${kind}"
            case "${kind}" in
                SHA256) sum="$(sha256sum "${f}" | awk '{print $1}')" ;;
                SHA512) sum="$(sha512sum "${f}" | awk '{print $1}')" ;;
            esac
            [ -f "${dir}/by-hash/${kind}/${sum}" ] || cp "${f}" "${dir}/by-hash/${kind}/${sum}"
            touch "${dir}/by-hash/${kind}/${sum}"
        done
    done
    for kind in SHA256 SHA512; do
        # shellcheck disable=SC2012 # digests: no odd names
        ls -1t "${dir}/by-hash/${kind}" | tail -n +11 | while read -r f; do rm -f "${dir}/by-hash/${kind}/${f}"; done
    done
}

index_apt() {
    local apt="${root}/apt" dist="${root}/apt/dists/stable" arch release
    mkdir -p "${apt}/pool/main" "${dist}"
    # the indexes are written again; by-hash/ keeps the earlier ones
    rm -f "${dist}/Release" "${dist}/Release.gpg" "${dist}/InRelease"
    for arch in amd64 arm64; do
        mkdir -p "${dist}/main/binary-${arch}"
        (cd "${apt}" && apt-ftparchive --arch "${arch}" packages pool/main) >"${dist}/main/binary-${arch}/Packages"
        gzip -9 -n -c "${dist}/main/binary-${arch}/Packages" >"${dist}/main/binary-${arch}/Packages.gz"
        by_hash "${dist}/main/binary-${arch}"
    done
    # written beside the tree first: apt-ftparchive lists every index under dists/stable (the
    # by-hash copies, named by their digests, match none of its patterns)
    release="$(mktemp)"
    (cd "${apt}" && apt-ftparchive \
        -o APT::FTPArchive::MD5=false -o APT::FTPArchive::SHA1=false \
        -o APT::FTPArchive::Release::Origin=Maktaba \
        -o APT::FTPArchive::Release::Label=Maktaba \
        -o APT::FTPArchive::Release::Suite=stable \
        -o APT::FTPArchive::Release::Codename=stable \
        -o APT::FTPArchive::Release::ValidTime="$((${VALID_DAYS:-14} * 86400))" \
        -o APT::FTPArchive::Release::Acquire-By-Hash=yes \
        -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
        -o APT::FTPArchive::Release::Components=main \
        -o APT::FTPArchive::Release::Description="Maktaba: the server (maktaba-server)" \
        release dists/stable) >"${release}"
    mv "${release}" "${dist}/Release"
    chmod 0644 "${dist}/Release"
    sign --detach-sign --armor --output "${dist}/Release.gpg" "${dist}/Release"
    sign --clearsign --output "${dist}/InRelease" "${dist}/Release"
    printf 'deb [signed-by=/usr/share/keyrings/maktaba.gpg] %s/apt stable main\n' "${REPO_URL}" >"${apt}/maktaba.list"
}

index_rpm() {
    local rpm="${root}/rpm"
    mkdir -p "${rpm}/packages"
    createrepo_c --quiet --update --checksum sha256 --general-compress-type gz "${rpm}"
    sign --detach-sign --armor --output "${rpm}/repodata/repomd.xml.asc" "${rpm}/repodata/repomd.xml"
    # Both checks: each package's own signature (gpgcheck) and the index's (repo_gpgcheck).
    cat >"${rpm}/maktaba.repo" <<EOF
[maktaba]
name=Maktaba
baseurl=${REPO_URL}/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=${REPO_URL}/maktaba.asc
EOF
}

index_page() {
    cat >"${root}/index.html" <<EOF
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Maktaba packages</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body>
<h1>Maktaba packages</h1>
<p>The Maktaba server (<code>maktaba-server</code>) of each stable release, for x86_64 and
aarch64. An installed server updates itself after that, in its quiet window; the repository
keeps the package's own files (the launcher) current. The same packages are attached to each
release on GitHub. The Linux app updates itself: take it from the release.</p>
<h2>Debian, Ubuntu</h2>
<pre>curl -fsSL ${REPO_URL}/maktaba.gpg | sudo tee /usr/share/keyrings/maktaba.gpg &gt;/dev/null
curl -fsSL ${REPO_URL}/apt/maktaba.list | sudo tee /etc/apt/sources.list.d/maktaba.list
sudo apt update
sudo apt install maktaba-server</pre>
<h2>Fedora, RHEL</h2>
<pre>sudo curl -fsSL -o /etc/yum.repos.d/maktaba.repo ${REPO_URL}/rpm/maktaba.repo
sudo dnf install maktaba-server</pre>
</body></html>
EOF
}

index() {
    local formats="${FORMATS:-apt rpm}" f
    : "${REPO_URL:?REPO_URL: the public address of the repository}"
    case "${REPO_URL}" in */) die "index: REPO_URL ends with /" ;; esac
    case "${VALID_DAYS:-14}" in ""|*[!0-9]*|0) die "index: VALID_DAYS must be a positive number" ;; esac
    mkdir -p "${root}"
    for f in ${formats}; do
        case "${f}" in
            apt) index_apt ;;
            rpm) index_rpm ;;
            *) die "index: unknown format ${f} (apt, rpm)" ;;
        esac
    done
    gpg --batch --yes --export "${GPG_KEY:?GPG_KEY: the signing key}" >"${root}/maktaba.gpg"
    gpg --batch --yes --armor --export "${GPG_KEY}" >"${root}/maktaba.asc"
    [ -s "${root}/maktaba.gpg" ] || die "index: no public key for ${GPG_KEY}"
    index_page
}

case "${cmd}" in
    add) add "$@" ;;
    prune) prune ;;
    index) index ;;
    *) die "unknown command ${cmd} (add, prune, index)" ;;
esac
