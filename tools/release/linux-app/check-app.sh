#!/usr/bin/env bash
# Checks the Linux app's bundles (Epic 39 story 39.10): for each AppImage, .deb or .rpm,
# the shell is there and the server payload is in usr/share/maktaba/server, and the
# payload installs as the server runs it: `maktaba-server runtime install` from the
# unpacked bundle into a scratch data directory checks every file against files.sha256
# (and makes again the links a bundler copied as files). check.sh then runs on that
# runtime copy (Postgres, ffmpeg, Python, the venv plan), once, on the first bundle.
#
# The .deb's Depends and the .rpm's Requires name the libraries the shell links, and both
# packages carry package-remove.sh as their removal script (prerm / %preun).
#
# Usage: check-app.sh BUNDLE...   (*.AppImage, *.deb, *.rpm)
# Needs: bash, coreutils, dpkg-deb for a .deb; rpm (its headers) and bsdtar (or rpm2cpio
# and cpio) for an .rpm.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ $# -gt 0 ] || { sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 2; }

fails=0
ok()   { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/linux-app-bundles.XXXXXX")"
trap 'rm -rf "$SCRATCH"' EXIT
checked=0

for bundle in "$@"; do
    name="$(basename "$bundle")"
    echo "bundle: $name ($(du -h "$bundle" | awk '{print $1}'))"
    dir="$SCRATCH/$name.d"
    mkdir -p "$dir"
    abs="$(cd "$(dirname "$bundle")" && pwd)/$name"
    case "$bundle" in
        *.AppImage)
            # --appimage-extract needs no FUSE; it unpacks into ./squashfs-root
            if ! out="$(cd "$dir" && "$abs" --appimage-extract 2>&1 >/dev/null)"; then
                fail "unpack $name: $out"
                continue
            fi
            root="$dir/squashfs-root"
            ;;
        *.deb)
            if ! out="$(dpkg-deb -x "$abs" "$dir" 2>&1)"; then
                fail "unpack $name: $out"
                continue
            fi
            root="$dir"
            deps="$(dpkg-deb -f "$bundle" Depends)"
            for want in libwebkit2gtk-4.1 libgtk-3 libayatana-appindicator3; do
                case "$deps" in *"$want"*) ok "Depends: $want" ;; *) fail "Depends lacks $want ($deps)" ;; esac
            done
            if dpkg-deb -I "$abs" prerm 2>/dev/null | cmp -s - "$SCRIPT_DIR/package-remove.sh"; then
                ok "prerm: package-remove.sh"
            else
                fail "the .deb's prerm is not package-remove.sh"
            fi
            ;;
        *.rpm)
            # the header (Requires, scriptlets) needs rpm; libarchive unpacks the payload whatever
            # its compression, rpm2cpio else
            if ! command -v rpm >/dev/null 2>&1; then
                fail "$name: rpm is needed to read the .rpm's Requires and scriptlets"
            else
                reqs="$(rpm -qp --requires "$abs" 2>/dev/null)"
                for want in 'libwebkit2gtk-4.1.so.0()(64bit)' 'libgtk-3.so.0()(64bit)' 'libayatana-appindicator3.so.1()(64bit)'; do
                    if printf '%s\n' "$reqs" | grep -qxF "$want"; then ok "Requires: $want"; else fail "Requires lacks $want ($(printf '%s' "$reqs" | tr '\n' ' '))"; fi
                done
                # rpm prints the scriptlet's body after its header line: it must hold the
                # script's disable line (a literal, $ and all)
                scripts="$(rpm -qp --scripts "$abs" 2>/dev/null)"
                # shellcheck disable=SC2016
                if printf '%s\n' "$scripts" | grep -q '^preuninstall scriptlet' &&
                    printf '%s\n' "$scripts" | sed -n '/^preuninstall scriptlet/,/^[a-z]* scriptlet/p' |
                    grep -qF 'systemctl --user --machine="$user@" disable --now "$unit"'; then
                    ok "%preun: package-remove.sh"
                else
                    fail "the .rpm's %preun is not package-remove.sh"
                fi
            fi
            if command -v bsdtar >/dev/null 2>&1; then
                out="$(bsdtar -xf "$abs" -C "$dir" 2>&1)" || { fail "unpack $name: $out"; continue; }
            else
                out="$(cd "$dir" && rpm2cpio "$abs" | cpio -idm --quiet 2>&1)" || { fail "unpack $name: $out"; continue; }
            fi
            root="$dir"
            ;;
        *)
            fail "$name: not an AppImage, .deb or .rpm"
            continue
            ;;
    esac
    shell="$(find "$root/usr/bin" -maxdepth 1 -type f -iname 'maktaba' -print -quit 2>/dev/null || true)"
    if [ -n "$shell" ]; then ok "the app: ${shell#"$root"/}"; else fail "no usr/bin/maktaba in $name"; fi
    payload="$root/usr/share/maktaba/server"
    if [ ! -f "$payload/payload.json" ]; then
        fail "no server payload in usr/share/maktaba/server"
        continue
    fi
    if find "$root/usr/lib" -path '*/server/payload.json' 2>/dev/null | grep -q .; then
        fail "a payload among the resources in usr/lib (linuxdeploy rewrites ELF files there)"
    fi
    # the server binary as the app runs it: a copy with its mode when the package lost it
    server="$SCRATCH/maktaba-server"
    cp "$payload/bin/maktaba-server" "$server"
    chmod 0755 "$server"
    data="$SCRATCH/$name.data"
    if out="$(XDG_DATA_HOME="$data" "$server" runtime install --from "$payload" 2>&1)"; then
        ok "runtime install: $(printf '%s\n' "$out" | tail -1)"
    else
        fail "runtime install from $name: $(printf '%s\n' "$out" | tail -3)"
        continue
    fi
    copy="$(find "$data/maktaba/runtime" -mindepth 1 -maxdepth 1 -type d ! -name '.*' -print -quit)"
    if [ "$checked" -eq 0 ]; then
        checked=1
        if "$SCRIPT_DIR/check.sh" --payload "$copy"; then ok "check.sh on the runtime copy"; else fail "check.sh on the runtime copy of $name"; fi
    fi
done

if [ "$fails" -ne 0 ]; then
    echo "$fails check(s) failed"
    exit 1
fi
echo "all bundles passed"
