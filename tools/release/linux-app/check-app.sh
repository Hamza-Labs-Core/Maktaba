#!/usr/bin/env bash
# Checks the Linux app's bundles (Epic 39 story 39.10): for each AppImage, .deb or .rpm,
# the shell is there and the server payload is in usr/share/maktaba/server, and the
# payload installs as the server runs it: `maktaba-server runtime install` from the
# unpacked bundle into a scratch data directory checks every file against files.sha256
# (and makes again the links a bundler copied as files). check.sh then runs on that
# runtime copy (Postgres, Python, the venv plan, no ffmpeg in it and the system's taken),
# once, on the first bundle. No bundle may carry ffmpeg or its libraries anywhere (the
# AppImage's GStreamer plugins included).
#
# The .deb's Depends and the .rpm's Requires name the libraries the shell links and the
# distribution's ffmpeg (the .rpm's is RPM Fusion's on Fedora and RHEL: their ffmpeg-free
# provides no "ffmpeg"), and both packages carry package-remove.sh as their removal script
# (prerm / %preun).
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
            # the package itself, not a library of it
            case ", $deps," in *", ffmpeg,"* | *", ffmpeg ("*) ok "Depends: ffmpeg" ;; *) fail "Depends lacks ffmpeg ($deps)" ;; esac
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
                for want in 'libwebkit2gtk-4.1.so.0()(64bit)' 'libgtk-3.so.0()(64bit)' 'libayatana-appindicator3.so.1()(64bit)' ffmpeg; do
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
    # No ffmpeg anywhere in the bundle: not in the payload, not among what the AppImage
    # brings along (its GStreamer plugins: gst-libav links libavcodec). The list goes to a
    # file (find | head under pipefail ended the script on SIGPIPE). Exact names: libavc1394, a
    # FireWire library the AppImage's GStreamer plugins bring, is no ffmpeg.
    find "$root" \( -iname 'ffmpeg' -o -iname 'ffprobe' -o -name 'libavcodec.so*' -o -name 'libavformat.so*' \
        -o -name 'libavfilter.so*' -o -name 'libavutil.so*' -o -name 'libavdevice.so*' -o -name 'libavresample.so*' \
        -o -name 'libpostproc.so*' -o -name 'libswscale.so*' -o -name 'libswresample.so*' -o -name 'libx264.so*' \
        -o -name 'libgstlibav*.so' \) \
        -printf '%P\n' >"$SCRATCH/ffmpeg-files" 2>/dev/null || true
    if [ ! -s "$SCRATCH/ffmpeg-files" ]; then
        ok "no ffmpeg in the bundle (the distribution's is used)"
    else
        fail "$name carries ffmpeg (the distribution's is used): $(sed -n '1,10p' "$SCRATCH/ffmpeg-files" | tr '\n' ' ')"
    fi
    # The payload's modes. A .deb or .rpm is installed as root and each user's setup copies
    # the tree: every file readable by others, every folder open to them, every executable
    # runnable by them (this extraction is ours, so u+x would prove nothing). An AppImage is
    # its user's own file, mounted or extracted by that user (--appimage-extract makes the
    # folders 700 itself): its executables need only run for that user. The lists go to a
    # file, with find's own errors (a folder it cannot read): a pipe into head ended the
    # script on SIGPIPE under pipefail.
    case "$name" in
        *.AppImage)
            runnable=-u+x who="its user"
            ;;
        *)
            runnable=-o+x who="any user"
            find "$payload" \( \( -type f ! -perm -o+r \) -o \( -type d ! -perm -o+rx \) \) -printf '%m %p\n' >"$SCRATCH/modes" 2>&1 || true
            if [ ! -s "$SCRATCH/modes" ]; then
                ok "every file readable by any user"
            else
                fail "$name: $(wc -l <"$SCRATCH/modes") files or folders other users cannot read (mode, path): $(sed -n '1,10p' "$SCRATCH/modes" | tr '\n' ';')"
                continue
            fi
            ;;
    esac
    find "$payload/bin" "$payload/postgres/bin" -maxdepth 1 -type f ! -perm "$runnable" -printf '%m %p\n' >"$SCRATCH/modes" 2>&1 || true
    if [ ! -s "$SCRATCH/modes" ]; then
        ok "executables runnable by $who"
    else
        fail "$name dropped the executable bit (mode, path): $(sed -n '1,10p' "$SCRATCH/modes" | tr '\n' ';')"
        continue
    fi
    server="$payload/bin/maktaba-server"
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
