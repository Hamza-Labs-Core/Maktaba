#!/usr/bin/env bash
# The update rehearsal's later releases of this commit (story 59.12, tests/nas/update_leg.py):
# a copy of this commit's Linux payload under another version.
#
#   reversion.sh SRC OUT VERSION            N+1: the Go binaries built again with VERSION
#   reversion.sh SRC OUT VERSION --crash    N+2, a release that cannot start: its server
#                                           exits at once when the launcher starts it
#
# SRC is a payload tools/release/linux-app/payload.sh built (build/linux-app/server); OUT is
# made from it. Its files.sha256 and payload.json are written again (the runtime installs
# only a payload whose files match its manifest). The pipeline's wheel stays the same: a
# `git describe` version, as these are, is a dev version for the pipeline either way. Run
# from the repository on the payload's own architecture (the binaries are built for it).
set -euo pipefail

SRC="${1:?usage: reversion.sh SRC OUT VERSION [--crash]}"
OUT="${2:?usage: reversion.sh SRC OUT VERSION [--crash]}"
VERSION="${3:?usage: reversion.sh SRC OUT VERSION [--crash]}"
CRASH="${4:-}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

[ -f "$SRC/payload.json" ] || { echo "reversion.sh: $SRC is not a payload" >&2; exit 2; }
rm -rf "$OUT"
cp -a "$SRC" "$OUT"
chmod -R u+w "$OUT"

case "$(uname -m)" in
    x86_64) GOARCH=amd64 ;;
    aarch64 | arm64) GOARCH=arm64 ;;
    *) echo "reversion.sh: $(uname -m) is not a payload architecture" >&2; exit 2 ;;
esac

if [ "$CRASH" = --crash ]; then
    # the image's entrypoint (`maktaba-server container`) and every command but serve run the
    # real binary; the launcher's `serve` exits 1 at once, every time: a release that cannot
    # start, which the launcher's guard goes back from
    mv "$OUT/bin/maktaba-server" "$OUT/bin/maktaba-server.real"
    cat >"$OUT/bin/maktaba-server" <<'EOF'
#!/bin/sh
# tests/nas/reversion.sh --crash: the update rehearsal's release that cannot start
for a in "$@"; do
    if [ "$a" = serve ]; then
        echo "maktaba-server (the rehearsal's bad release): exiting at once" >&2
        exit 1
    fi
done
exec "$(dirname "$0")/maktaba-server.real" "$@"
EOF
    chmod 0755 "$OUT/bin/maktaba-server"
else
    echo "reversion.sh: the Go binaries as $VERSION (linux/$GOARCH)"
    make -C "$ROOT" --no-print-directory server build-go \
        GOOS=linux GOARCH="$GOARCH" VERSION="$VERSION" BIN_SUBDIR="linux-$GOARCH/" >/dev/null
    cp "$ROOT/cmd/maktaba-server/bin/linux-$GOARCH/maktaba-server" \
       "$ROOT/api/bin/linux-$GOARCH/maktaba-api" \
       "$ROOT/streaming/bin/linux-$GOARCH/maktaba-streaming" \
       "$ROOT/cloud/bin/linux-$GOARCH/maktaba-cloudlink" "$OUT/bin/"
    (cd "$ROOT/cmd/maktaba-launcher" && GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
        go build -trimpath -ldflags "-buildid= -s -w -X main.Version=$VERSION" -o "$OUT/bin/maktaba-launcher" .)
fi
chmod -R go-w,a+rX "$OUT"

# the manifest and payload.json, as payload.sh writes them
PY="$OUT/python/bin/python3"
manifest="$("$PY" -I "$ROOT/tools/release/linux-app/payload_tool.py" manifest "$OUT")"
P_VERSION="$VERSION" P_MANIFEST="$manifest" "$PY" -I - "$OUT/payload.json" <<'EOF'
import json, os, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    doc = json.load(f)
doc["version"] = os.environ["P_VERSION"]
doc["files"] = {"manifest": "files.sha256", **json.loads(os.environ["P_MANIFEST"])}
with open(path, "w", encoding="utf-8") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
EOF
"$PY" -I "$ROOT/tools/release/linux-app/payload_tool.py" verify "$OUT"
echo "reversion.sh: $OUT is $VERSION${CRASH:+ (cannot start)}"
