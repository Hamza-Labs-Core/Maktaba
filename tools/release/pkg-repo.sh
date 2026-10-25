#!/usr/bin/env bash
# The Maktaba package repositories: an apt repository and a dnf/yum repository holding the
# .deb and .rpm packages of the stable releases (the server's, maktaba-server, and the Linux
# app's, maktaba). packages.yml updates them after each stable release is published, from
# the release's own (verified) packages; the tree is static, and packages.yml syncs it to
# the bucket the repository's address serves.
#
#   pkg-repo.sh add   ROOT FILE...   put each .deb / .rpm in the tree (one already there must
#                                    be the same bytes: a published package never changes)
#   pkg-repo.sh prune ROOT           keep the KEEP newest versions of each package and
#                                    architecture (KEEP, default 3)
#   pkg-repo.sh index ROOT           write the indexes and sign them, with the public key,
#                                    the source files and a page that says how to add them
#
# ROOT holds:
#   maktaba.gpg, maktaba.asc          the repository's public key (binary for apt's signed-by,
#                                     armored for dnf's gpgkey)
#   apt/pool/main/<file>.deb          the packages
#   apt/dists/stable/                 Release, InRelease, Release.gpg and
#                                     main/binary-<amd64|arm64>/Packages(.gz)
#   apt/maktaba.list                  the source line, for /etc/apt/sources.list.d/
#   rpm/packages/<file>.rpm           the packages
#   rpm/repodata/                     createrepo_c's indexes; repomd.xml.asc signs them
#   rpm/maktaba.repo                  the repository file, for /etc/yum.repos.d/
#   index.html                        how to add them
#
# The indexes are signed (apt's InRelease and Release.gpg, dnf's repomd.xml.asc) and list
# every package's SHA-256, so apt and dnf check each package through the signed index. The
# packages themselves stay the release's bytes: signing them again would change the files
# the release's own signature covers.
#
# Env: REPO_URL (index: the public address, written into the source files and the page);
# GPG_KEY (index: the signing key's fingerprint, in GNUPGHOME); GPG_PASSPHRASE_FILE
# (optional); KEEP (prune, default 3); FORMATS (index: "apt rpm", the default, or one).
# Needs apt-ftparchive (apt-utils), createrepo_c (createrepo-c), gpg, gzip, sort -V.
set -euo pipefail

die() { echo "pkg-repo.sh: $*" >&2; exit 1; }

cmd="${1:-}"
root="${2:-}"
if [ -z "${cmd}" ] || [ -z "${root}" ]; then die "usage: pkg-repo.sh add|prune|index ROOT [FILE...]"; fi
shift 2

# ident FILE: "<name> <arch> <version>" from a package's file name, as nfpm and tauri name
# them (name_version_arch.deb; name-version-release.arch.rpm); nothing for another name
ident() {
    local base="${1##*/}" stem nvr nv
    case "${base}" in
        *.deb)
            stem="${base%.deb}"
            case "${stem}" in *_*_*) ;; *) return 0 ;; esac
            # name, version, architecture: the version may hold no "_" either
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

add() {
    local f base dst
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
        if [ -e "${dst}" ]; then
            cmp -s "${f}" "${dst}" ||
                die "add: ${base} is in the repository with other bytes: a published package never changes"
            echo "kept ${base} (there already)"
            continue
        fi
        cp "${f}" "${dst}.part"
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
                    rm -f "${f}"
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

index_apt() {
    local apt="${root}/apt" dist="${root}/apt/dists/stable" arch release
    mkdir -p "${apt}/pool/main"
    rm -rf "${dist}"
    for arch in amd64 arm64; do
        mkdir -p "${dist}/main/binary-${arch}"
        (cd "${apt}" && apt-ftparchive --arch "${arch}" packages pool/main) >"${dist}/main/binary-${arch}/Packages"
        gzip -9 -n -c "${dist}/main/binary-${arch}/Packages" >"${dist}/main/binary-${arch}/Packages.gz"
    done
    # written beside the tree first: apt-ftparchive hashes every file under dists/stable
    release="$(mktemp)"
    (cd "${apt}" && apt-ftparchive \
        -o APT::FTPArchive::Release::Origin=Maktaba \
        -o APT::FTPArchive::Release::Label=Maktaba \
        -o APT::FTPArchive::Release::Suite=stable \
        -o APT::FTPArchive::Release::Codename=stable \
        -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
        -o APT::FTPArchive::Release::Components=main \
        -o APT::FTPArchive::Release::Description="Maktaba: the server (maktaba-server) and the Linux app (maktaba)" \
        release dists/stable) >"${release}"
    mv "${release}" "${dist}/Release"
    chmod 0644 "${dist}/Release"
    sign --clearsign --output "${dist}/InRelease" "${dist}/Release"
    sign --detach-sign --armor --output "${dist}/Release.gpg" "${dist}/Release"
    printf 'deb [signed-by=/usr/share/keyrings/maktaba.gpg] %s/apt stable main\n' "${REPO_URL}" >"${apt}/maktaba.list"
}

index_rpm() {
    local rpm="${root}/rpm"
    mkdir -p "${rpm}/packages"
    createrepo_c --quiet --update --checksum sha256 "${rpm}"
    sign --detach-sign --armor --output "${rpm}/repodata/repomd.xml.asc" "${rpm}/repodata/repomd.xml"
    # The indexes are signed and name each package's SHA-256 (repo_gpgcheck); the packages
    # are the release's own bytes, unsigned by this key (gpgcheck=0).
    cat >"${rpm}/maktaba.repo" <<EOF
[maktaba]
name=Maktaba
baseurl=${REPO_URL}/rpm
enabled=1
gpgcheck=0
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
<p>The stable releases of the Maktaba server (<code>maktaba-server</code>) and the Linux app
(<code>maktaba</code>), for x86_64 and aarch64. Each installed server updates itself after
that, in its quiet window; the repository keeps the package's own files current.</p>
<h2>Debian, Ubuntu</h2>
<pre>curl -fsSL ${REPO_URL}/maktaba.gpg | sudo tee /usr/share/keyrings/maktaba.gpg &gt;/dev/null
curl -fsSL ${REPO_URL}/apt/maktaba.list | sudo tee /etc/apt/sources.list.d/maktaba.list
sudo apt update
sudo apt install maktaba          # the Linux app (or maktaba-server: the server alone)</pre>
<h2>Fedora, RHEL</h2>
<pre>sudo curl -fsSL -o /etc/yum.repos.d/maktaba.repo ${REPO_URL}/rpm/maktaba.repo
sudo dnf install maktaba          # the Linux app (or maktaba-server: the server alone)</pre>
</body></html>
EOF
}

index() {
    local formats="${FORMATS:-apt rpm}" f
    : "${REPO_URL:?REPO_URL: the public address of the repository}"
    case "${REPO_URL}" in */) die "index: REPO_URL ends with /" ;; esac
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
