#!/usr/bin/env bash
# The Linux maktaba-full package (Epic 51 story 51.17): the app's package, `maktaba`, plus
# this release's runtime.ml and tool.yt-dlp packs of one architecture, so a Full home builds
# its venv from those wheels and downloads none. It holds the release's offline file
# (`maktaba-server release-offline`) unpacked as a folder,
#
#   /usr/share/maktaba/packs/maktaba-offline-<v>-<platform>-full/
#       offline.json checksums.txt checksums.txt.sigstore.json release.json
#       components-<v>.json packs/<pack files>
#
# which the server imports at the first start of that version (serve_systempacks.go) with
# the verification of a download: the release's signature, the index, every pack.
#
# The packages, .deb and .rpm, as the app's are versioned (bundle.sh --versions):
#   maktaba-full         Depends: maktaba (>= <v>) and each data package below (= <v>);
#                        the folder's small files and what of the packs fits
#   maktaba-full-dataN   the rest of the packs' files, each package under --max-bytes
#                        (1.9 GiB: GitHub takes no release file of 2 GiB or more, and
#                        runtime.ml's Linux wheels with torch are bigger than that)
# Depends is >=, not =: the app updates itself (its updater installs the new `maktaba`
# alone, with dpkg or rpm), and an exact pin would make that install fail. The packs of the
# older version are then left alone (the import takes only its own version's) and the new
# ones come from the network with the update (51.18).
#
# Usage: full-package.sh --version V --arch x86_64|aarch64 --offline TAR --out DIR [--max-bytes N]
# Needs nfpm, tar and python3.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
die() { printf 'full-package: error: %s\n' "$*" >&2; exit 1; }

VERSION="" ARCH="" OFFLINE="" OUT="" MAX_BYTES=2040109465 # 1.9 GiB
while [ $# -gt 0 ]; do
    case "$1" in
        --version)   [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
        --arch)      [ $# -ge 2 ] || die "--arch needs a value"; ARCH="$2"; shift 2 ;;
        --offline)   [ $# -ge 2 ] || die "--offline needs a value"; OFFLINE="$2"; shift 2 ;;
        --out)       [ $# -ge 2 ] || die "--out needs a value"; OUT="$2"; shift 2 ;;
        --max-bytes) [ $# -ge 2 ] || die "--max-bytes needs a value"; MAX_BYTES="$2"; shift 2 ;;
        -h|--help)   sed -n '2,/^set -e/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
        *)           die "unknown option '$1'" ;;
    esac
done
if [ -z "$VERSION" ] || [ -z "$ARCH" ] || [ -z "$OFFLINE" ] || [ -z "$OUT" ]; then
  die "--version, --arch, --offline and --out are required"
fi
case "$ARCH" in
    x86_64) GOARCH=amd64 PLATFORM=linux-amd64 ;;
    aarch64) GOARCH=arm64 PLATFORM=linux-arm64 ;;
    *) die "--arch is x86_64 or aarch64, not '$ARCH'" ;;
esac
command -v nfpm >/dev/null 2>&1 || die "nfpm is required"
v="${VERSION#v}"
# package_versions (bundle.sh runs nothing when sourced)
# shellcheck source-path=SCRIPTDIR source=bundle.sh
. "$SCRIPT_DIR/bundle.sh"
die() { printf 'full-package: error: %s\n' "$*" >&2; exit 1; }
read -r DEB_VERSION RPM_VERSION RPM_RELEASE <<<"$(package_versions "$v")"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
NAME="maktaba-offline-$v-$PLATFORM-full"
DST="/usr/share/maktaba/packs/$NAME"
mkdir -p "$WORK/folder" "$OUT"
tar -C "$WORK/folder" -xf "$OFFLINE"
# the file is this release's, for this architecture
python3 -I - "$WORK/folder/offline.json" "$v" "$PLATFORM" <<'EOF' || die "$OFFLINE is not this release's offline file for $PLATFORM"
import json, sys
m = json.load(open(sys.argv[1]))
ok = m.get("schema") == 1 and m.get("version") == sys.argv[2] and m.get("platform") == sys.argv[3] and m.get("items")
sys.exit(0 if ok else 1)
EOF

# the files, small ones first (into maktaba-full), then the packs' files by name, each
# package filled up to MAX_BYTES; one line per file: <package index> <size> <path>
(cd "$WORK/folder" && find . -type f | sed 's#^\./##' | sort) >"$WORK/files"
python3 -I - "$WORK/folder" "$MAX_BYTES" "$WORK/files" >"$WORK/plan" <<'EOF'
import os, sys
root, limit = sys.argv[1], int(sys.argv[2])
files = [l.strip() for l in open(sys.argv[3]) if l.strip()]
small = [f for f in files if not f.startswith("packs/")]
packs = [f for f in files if f.startswith("packs/")]
pkg, used = 0, 0
for f in small + packs:
    size = os.path.getsize(os.path.join(root, f))
    if size > limit:
        sys.exit(f"{f} is {size} bytes, more than a package holds ({limit})")
    if used + size > limit and used > 0:
        pkg, used = pkg + 1, 0
    used += size
    print(pkg, size, f)
EOF
count=$(($(awk '{print $1}' "$WORK/plan" | sort -n | tail -1) + 1))

pkgname() { if [ "$1" -eq 0 ]; then echo maktaba-full; else echo "maktaba-full-data$1"; fi; }

# config FORMAT INDEX: the nfpm config of package INDEX for FORMAT (deb, rpm)
config() {
    local format="$1" i="$2" name
    name="$(pkgname "$i")"
    {
        echo "name: $name"
        echo "arch: $GOARCH"
        echo "platform: linux"
        echo "version_schema: none"
        if [ "$format" = deb ]; then
            echo "version: \"$DEB_VERSION\""
        else
            echo "version: \"$RPM_VERSION\""
            echo "release: \"$RPM_RELEASE\""
        fi
        echo "section: video"
        echo "priority: optional"
        echo "maintainer: Hamza Labs <maktaba@hamzalabs.dev>"
        echo "vendor: Hamza Labs"
        echo "homepage: https://maktaba.tv"
        echo "license: MIT"
        if [ "$i" -eq 0 ]; then
            echo "description: |"
            echo "  Maktaba with everything: the app (the maktaba package) and this release's"
            echo "  transcription, search-by-meaning and media tool packs for $PLATFORM, imported"
            echo "  at the server's first start, so the Full bundle downloads no wheels."
            echo "depends:"
            if [ "$format" = deb ]; then
                echo "  - maktaba (>= $DEB_VERSION)"
            else
                echo "  - maktaba >= $RPM_VERSION"
            fi
            for ((j = 1; j < count; j++)); do
                if [ "$format" = deb ]; then
                    echo "  - $(pkgname "$j") (= $DEB_VERSION)"
                else
                    echo "  - $(pkgname "$j") = $RPM_VERSION-$RPM_RELEASE"
                fi
            done
        else
            echo "description: |"
            echo "  Part $i of maktaba-full's packs for $PLATFORM (installed with maktaba-full)."
        fi
        echo "contents:"
        awk -v i="$i" '$1 == i {print $3}' "$WORK/plan" | while read -r f; do
            echo "  - src: $WORK/folder/$f"
            echo "    dst: $DST/$f"
            echo "    file_info:"
            echo "      mode: 0644"
        done
    } >"$WORK/$format-$i.yaml"
}

for ((i = 0; i < count; i++)); do
    for format in deb rpm; do
        config "$format" "$i"
        nfpm package -f "$WORK/$format-$i.yaml" -p "$format" -t "$OUT/" >/dev/null
    done
done
# "~" in a file name becomes "." on a GitHub release: the names use "." from the start
for f in "$OUT"/*~*; do
    [ -e "$f" ] || continue
    mv "$f" "${f//\~/.}"
done
ls -l "$OUT"
echo "full-package: $count package(s) of $NAME (deb $DEB_VERSION, rpm $RPM_VERSION-$RPM_RELEASE)"
