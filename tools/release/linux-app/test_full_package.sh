#!/usr/bin/env bash
# ok and bad always succeed: "check && ok || bad" is an if-then-else here
# shellcheck disable=SC2015
#
# Tests full-package.sh (story 51.17) on a small offline file: the packages' names, their
# versions in the .deb and .rpm forms, maktaba-full's dependencies (maktaba at least this
# version, each data package exactly), the folder the packages make together, the data
# packages' size limit, and a file of another platform refused. With --install (CI, as
# root through sudo) it also installs them with dpkg beside a stand-in `maktaba` package
# and checks what an existing install meets: the app's own update (a newer `maktaba` by
# itself, as its updater installs it) goes in with maktaba-full there, `maktaba` cannot be
# removed from under maktaba-full, and maktaba-full cannot go in without `maktaba`.
#
# Usage: test_full_package.sh [--install]   (needs nfpm, python3; dpkg-deb and rpm for the
#                                            package checks, which are skipped without them)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL=0
[ "${1:-}" != --install ] || INSTALL=1
T="$(mktemp -d)"
cleanup() {
    if [ "$INSTALL" -eq 1 ]; then
        sudo dpkg --purge maktaba-full maktaba-full-data1 maktaba-full-data2 maktaba >/dev/null 2>&1 || true
    fi
    rm -rf "$T"
}
trap cleanup EXIT
fails=0
ok() { printf '  ok    %s\n' "$*"; }
bad() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

# an offline file of 0.3.0-rc.1 for linux-amd64: the signed set, and three pack files of
# 400 bytes each (with --max-bytes 700: maktaba-full and two data packages)
mkdir -p "$T/off/packs"
printf '{"schema":1,"version":"0.3.0-rc.1","platform":"linux-amd64","items":["runtime.ml","tool.yt-dlp"]}\n' >"$T/off/offline.json"
for f in checksums.txt checksums.txt.sigstore.json release.json components-0.3.0-rc.1.json; do echo "$f" >"$T/off/$f"; done
for f in runtime.ml-0.3.0-rc.1-linux-amd64.tar.zst.part1 runtime.ml-0.3.0-rc.1-linux-amd64.tar.zst.part2 \
    tool.yt-dlp-0.3.0-rc.1-linux-amd64.tar.zst; do
    head -c 400 /dev/zero | tr '\0' 'x' >"$T/off/packs/$f"
done
(cd "$T/off" && tar -cf "$T/offline.tar" offline.json checksums.txt checksums.txt.sigstore.json release.json \
    components-0.3.0-rc.1.json packs)

echo "full-package.sh"
"$SCRIPT_DIR/full-package.sh" --version v0.3.0-rc.1 --arch x86_64 --offline "$T/offline.tar" --out "$T/out" --max-bytes 700 >/dev/null
names="$(cd "$T/out" && find . -type f | sed 's#^\./##' | sort | tr '\n' ' ')"
want="maktaba-full-0.3.0-0.1.rc.1.x86_64.rpm maktaba-full-data1-0.3.0-0.1.rc.1.x86_64.rpm maktaba-full-data1_0.3.0.rc.1_amd64.deb maktaba-full-data2-0.3.0-0.1.rc.1.x86_64.rpm maktaba-full-data2_0.3.0.rc.1_amd64.deb maktaba-full_0.3.0.rc.1_amd64.deb "
if [ "$names" = "$want" ]; then ok "names (no '~' in a file name)"; else bad "names: $names"; fi

# the packs fit one package: maktaba-full alone, depending on maktaba and nothing else
"$SCRIPT_DIR/full-package.sh" --version v0.3.0-rc.1 --arch x86_64 --offline "$T/offline.tar" --out "$T/out-one" >/dev/null
one="$(cd "$T/out-one" && find . -type f | sed 's#^\./##' | sort | tr '\n' ' ')"
[ "$one" = "maktaba-full-0.3.0-0.1.rc.1.x86_64.rpm maktaba-full_0.3.0.rc.1_amd64.deb " ] &&
    ok "one package when the packs fit" || bad "one package: $one"

if "$SCRIPT_DIR/full-package.sh" --version v0.3.0-rc.1 --arch aarch64 --offline "$T/offline.tar" --out "$T/out-arm" >/dev/null 2>&1; then
    bad "an amd64 offline file packaged for aarch64"
else
    ok "an offline file of another platform is refused"
fi
if "$SCRIPT_DIR/full-package.sh" --version v0.3.1 --arch x86_64 --offline "$T/offline.tar" --out "$T/out-v" >/dev/null 2>&1; then
    bad "a 0.3.0-rc.1 offline file packaged as 0.3.1"
else
    ok "an offline file of another version is refused"
fi
if "$SCRIPT_DIR/full-package.sh" --version v0.3.0-rc.1 --arch x86_64 --offline "$T/offline.tar" --out "$T/out-small" --max-bytes 300 >/dev/null 2>&1; then
    bad "a pack file bigger than a package was packaged"
else
    ok "a pack file bigger than a package is refused"
fi

DIR=usr/share/maktaba/packs/maktaba-offline-0.3.0-rc.1-linux-amd64-full
if command -v dpkg-deb >/dev/null 2>&1; then
    full="$T/out/maktaba-full_0.3.0.rc.1_amd64.deb"
    [ "$(dpkg-deb -f "$full" Package)" = maktaba-full ] && ok ".deb Package" || bad ".deb Package: $(dpkg-deb -f "$full" Package)"
    [ "$(dpkg-deb -f "$full" Version)" = 0.3.0~rc.1 ] && ok ".deb Version 0.3.0~rc.1" || bad ".deb Version: $(dpkg-deb -f "$full" Version)"
    deps="$(dpkg-deb -f "$full" Depends)"
    case "$deps" in
        *"maktaba (>= 0.3.0~rc.1)"*"maktaba-full-data1 (= 0.3.0~rc.1)"*"maktaba-full-data2 (= 0.3.0~rc.1)"*) ok ".deb Depends: $deps" ;;
        *) bad ".deb Depends: $deps" ;;
    esac
    one_deps="$(dpkg-deb -f "$T/out-one/maktaba-full_0.3.0.rc.1_amd64.deb" Depends)"
    [ "$one_deps" = "maktaba (>= 0.3.0~rc.1)" ] && ok "a lone maktaba-full depends on maktaba only" ||
        bad "a lone maktaba-full Depends: $one_deps"
    [ -z "$(dpkg-deb -f "$T/out/maktaba-full-data1_0.3.0.rc.1_amd64.deb" Depends)" ] && ok "a data package depends on nothing" ||
        bad "data1 Depends: $(dpkg-deb -f "$T/out/maktaba-full-data1_0.3.0.rc.1_amd64.deb" Depends)"
    # together, the packages make the offline file's folder, file for file
    got="$(for d in "$T"/out/*.deb; do dpkg-deb -c "$d"; done | awk '$1 !~ /^d/ {print $NF}' | sed 's#^\./##' | grep "^$DIR/" | sed "s#^$DIR/##" | sort | tr '\n' ' ')"
    exp="$(cd "$T/off" && find . -type f | sed 's#^\./##' | sort | tr '\n' ' ')"
    [ "$got" = "$exp" ] && ok "the .debs make the folder" || bad "the .debs hold: $got, want: $exp"
else
    echo "  skip  .deb checks (no dpkg-deb)"
fi
if command -v rpm >/dev/null 2>&1; then
    rfull="$T/out/maktaba-full-0.3.0-0.1.rc.1.x86_64.rpm"
    [ "$(rpm -qp --queryformat '%{NAME} %{VERSION}-%{RELEASE}' "$rfull" 2>/dev/null)" = "maktaba-full 0.3.0-0.1.rc.1" ] &&
        ok ".rpm name and version" || bad ".rpm: $(rpm -qp --queryformat '%{NAME} %{VERSION}-%{RELEASE}' "$rfull" 2>/dev/null)"
    reqs="$(rpm -qp --requires "$rfull" 2>/dev/null | tr '\n' ',')"
    case "$reqs" in
        *"maktaba >= 0.3.0"*"maktaba-full-data1 = 0.3.0-0.1.rc.1"*) ok ".rpm Requires" ;;
        *) bad ".rpm Requires: $reqs" ;;
    esac
    rpm -qlp "$rfull" 2>/dev/null | grep -q "^/$DIR/offline.json$" && ok ".rpm holds offline.json" || bad ".rpm files: $(rpm -qlp "$rfull" 2>/dev/null)"
else
    echo "  skip  .rpm checks (no rpm)"
fi

if [ "$INSTALL" -eq 1 ]; then
    echo "install (dpkg)"
    # a stand-in for the app's package: what dpkg sees of it is its name and version
    stub() {
        local v="$1" out="$2"
        mkdir -p "$T/stub"
        printf 'maktaba %s\n' "$v" >"$T/stub/VERSION"
        cat >"$T/stub-$v.yaml" <<EOF
name: maktaba
arch: amd64
platform: linux
version: "$v"
version_schema: none
maintainer: test <test@example.com>
description: the app's package, a stand-in
contents:
  - src: $T/stub/VERSION
    dst: /usr/share/maktaba-test-stub/VERSION
EOF
        nfpm package -f "$T/stub-$v.yaml" -p deb -t "$out" >/dev/null
    }
    mkdir -p "$T/stubs"
    stub 0.3.0~rc.1 "$T/stubs"
    stub 0.3.0 "$T/stubs"
    if sudo dpkg -i "$T"/out/maktaba-full*_amd64.deb >/dev/null 2>&1; then
        bad "maktaba-full went in without maktaba"
    else
        ok "maktaba-full does not go in without maktaba"
    fi
    sudo dpkg --purge maktaba-full maktaba-full-data1 maktaba-full-data2 >/dev/null 2>&1 || true
    sudo dpkg -i "$T/stubs/maktaba_0.3.0~rc.1_amd64.deb" "$T"/out/maktaba-full*_amd64.deb >/dev/null &&
        ok "maktaba and maktaba-full install" || bad "maktaba and maktaba-full do not install"
    got="$(cd "/$DIR" && find . -type f | sed 's#^\./##' | sort | tr '\n' ' ')"
    [ "$got" = "$exp" ] && ok "/$DIR holds the offline file" || bad "/$DIR holds: $got"
    sudo dpkg -i "$T/stubs/maktaba_0.3.0_amd64.deb" >/dev/null && ok "the app's own update (a newer maktaba alone) goes in" ||
        bad "a newer maktaba does not go in beside maktaba-full"
    if sudo dpkg -r maktaba >/dev/null 2>&1; then
        bad "maktaba was removed from under maktaba-full"
    else
        ok "maktaba is not removed from under maktaba-full"
    fi
fi

[ "$fails" -eq 0 ] || { echo "test_full_package: $fails failed"; exit 1; }
echo "test_full_package: all passed"
