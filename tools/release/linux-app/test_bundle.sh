#!/usr/bin/env bash
# Tests bundle.sh's package versions and its .deb version rewrite: a pre-release's .deb and
# .rpm versions, that dpkg and rpm order them before the release (and rc.2 after rc.1), and
# that the rewritten .deb keeps its files, their owner and its maintainer scripts.
#
# Usage: test_bundle.sh   (needs dpkg-deb and dpkg; the rpm order only when rpm is there)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR source=bundle.sh
source "$SCRIPT_DIR/bundle.sh"

failures=0
fail() { echo "FAIL: $*" >&2; failures=$((failures + 1)); }

# the versions: .deb, .rpm version, .rpm release
for spec in "0.3.0|0.3.0 0.3.0 1" \
    "v0.3.0|0.3.0 0.3.0 1" \
    "0.3.0-rc.1|0.3.0~rc.1 0.3.0 0.1.rc.1" \
    "v0.0.0-ci.42|0.0.0~ci.42 0.0.0 0.1.ci.42" \
    "0.3.0-rc-1|0.3.0~rc.1 0.3.0 0.1.rc.1" \
    "0.3.0-rc.1+b7|0.3.0~rc.1+b7 0.3.0 0.1.rc.1+b7" \
    "0.3.0+b7|0.3.0+b7 0.3.0+b7 1"; do
    in="${spec%%|*}" want="${spec#*|}"
    got="$(package_versions "$in")"
    [ "$got" = "$want" ] || fail "package_versions $in: $got, want $want"
done
for bad in "" "0.3" "0.3.0.1" "dev" "v0.3.0-" "0.3.0-rc.1-5-g1a2b3c4-dirty x"; do
    if (package_versions "$bad") >/dev/null 2>&1; then
        fail "package_versions took \"$bad\""
    fi
done

# the same from the command line (build-app.sh asks it whether a version is semver)
[ "$(bash "$SCRIPT_DIR/bundle.sh" --versions v0.3.0-rc.1)" = "0.3.0~rc.1 0.3.0 0.1.rc.1" ] || fail "bundle.sh --versions"
if bash "$SCRIPT_DIR/bundle.sh" --versions dev >/dev/null 2>&1; then
    fail "bundle.sh --versions took dev"
fi

# the order: each before the next
deb_of() { local v; v="$(package_versions "$1")"; echo "${v%% *}"; }
rpm_of() { local v; v="$(package_versions "$1")"; v="${v#* }"; echo "${v% *}-${v#* }"; }
order=(0.2.9 0.3.0-beta.1 0.3.0-rc.1 0.3.0-rc.2 0.3.0-rc.10 0.3.0 0.3.1-rc.1 0.3.1)
for ((i = 0; i + 1 < ${#order[@]}; i++)); do
    a="${order[i]}" b="${order[i + 1]}"
    dpkg --compare-versions "$(deb_of "$a")" lt "$(deb_of "$b")" ||
        fail "dpkg: $(deb_of "$a") ($a) is not before $(deb_of "$b") ($b)"
    if command -v rpm >/dev/null 2>&1; then
        cmp="$(rpm --eval "%{lua: print(rpm.vercmp('$(rpm_of "$a")', '$(rpm_of "$b")'))}")"
        [ "$cmp" = -1 ] || fail "rpm: $(rpm_of "$a") ($a) is not before $(rpm_of "$b") ($b): $cmp"
    fi
done
command -v rpm >/dev/null 2>&1 || echo "(no rpm here: its order not checked)"

# the .deb's version rewritten: its files, their owner, its scripts kept
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/pkg/DEBIAN" "$work/pkg/usr/bin" "$work/pkg/usr/share/maktaba/server"
printf 'Package: maktaba\nVersion: 0.3.0-rc.1\nArchitecture: amd64\nMaintainer: x <x@example.com>\nDescription: test\n' \
    >"$work/pkg/DEBIAN/control"
printf '#!/bin/sh\necho removing\n' >"$work/pkg/DEBIAN/prerm"
chmod 0755 "$work/pkg/DEBIAN/prerm"
printf 'app\n' >"$work/pkg/usr/bin/maktaba"
chmod 0755 "$work/pkg/usr/bin/maktaba"
printf 'payload\n' >"$work/pkg/usr/share/maktaba/server/payload.json"
dpkg-deb --root-owner-group -b "$work/pkg" "$work/Maktaba_0.3.0-rc.1_amd64.deb" >/dev/null
set_deb_version "$work/Maktaba_0.3.0-rc.1_amd64.deb" "0.3.0~rc.1"
deb="$work/Maktaba_0.3.0-rc.1_amd64.deb"
[ "$(dpkg-deb -f "$deb" Version)" = "0.3.0~rc.1" ] || fail "rewritten version: $(dpkg-deb -f "$deb" Version)"
[ "$(dpkg-deb -f "$deb" Package)" = maktaba ] || fail "rewritten package name: $(dpkg-deb -f "$deb" Package)"
listing="$(dpkg-deb -c "$deb")"
for f in ./usr/bin/maktaba ./usr/share/maktaba/server/payload.json; do
    line="$(grep " $f\$" <<<"$listing" || true)"
    [ -n "$line" ] || fail "rewritten .deb lost $f"
    [[ "$line" == *" root/root "* ]] || fail "$f not root's: $line"
done
[[ "$(grep ' ./usr/bin/maktaba$' <<<"$listing")" == -rwxr-xr-x* ]] || fail "the binary lost its mode"
dpkg-deb -I "$deb" prerm | grep -q 'echo removing' || fail "the prerm is gone"

if [ "$failures" -gt 0 ]; then
    echo "$failures failure(s)" >&2
    exit 1
fi
echo "test_bundle.sh: ok"
