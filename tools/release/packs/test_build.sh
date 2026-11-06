#!/usr/bin/env bash
# Story 51.15 T09: build.sh on a tiny test catalog builds the same .tar.zst twice, its
# files.sha256 matches what unpacks, a big one splits into parts, and a recipe that puts a
# link out of its folder fails the build.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$here/../../.." && pwd)"
tmp="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/packs-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# the real catalog's head (phases, reasons, features) and one pack component
awk '/^\[\[component\]\]/ { exit } { print }' "$REPO_ROOT/shared/components/catalog.toml" >"$tmp/catalog.toml"
cat >>"$tmp/catalog.toml" <<'TOML'
[[component]]
id         = "tool.fake"
kind       = "asset"
group      = "tools"
title_key  = "components.tool.fake.title"
desc_key   = "components.tool.fake.desc"
source     = "pack:tool.fake"
in_payload = false
platforms  = ["linux-amd64"]
since      = "0.3.66"
TOML

mkdir -p "$tmp/recipes"
cat >"$tmp/recipes/tool.fake.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
stage="$1"
mkdir -p "$stage/bin" "$stage/share/fake"
printf '#!/bin/sh\necho fake %s %s\n' "$2" "$3" >"$stage/bin/fake"
chmod 0755 "$stage/bin/fake"
# a few hundred KiB zstd cannot shrink much (a small part size splits it), the same
# bytes every run
python3 -c 'import random,sys; r=random.Random(51); sys.stdout.buffer.write(bytes(r.getrandbits(8) for _ in range(400000)))' >"$stage/share/fake/data.bin"
echo "hello" >"$stage/share/fake/readme.txt"
ln -s ../../bin/fake "$stage/share/fake/run"
SH

export SOURCE_DATE_EPOCH=1762160000
build() { bash "$here/build.sh" --version 0.4.2 --platform linux-amd64 --catalog "$tmp/catalog.toml" --recipes "$tmp/recipes" --out "$1" "${@:2}"; }

build "$tmp/a" >"$tmp/a.log" 2>&1 || { cat "$tmp/a.log"; fail "first build"; }
build "$tmp/b" >"$tmp/b.log" 2>&1 || { cat "$tmp/b.log"; fail "second build"; }
pack=tool.fake-0.4.2-linux-amd64.tar.zst
[ -f "$tmp/a/$pack" ] || fail "no $pack"
cmp "$tmp/a/$pack" "$tmp/b/$pack" || fail "two builds of one commit differ"
grep -q "$pack: tool.fake 0.4.2 linux-amd64" "$tmp/a.log" || { cat "$tmp/a.log"; fail "the pack was not verified"; }
echo "ok: the same bytes twice, and it unpacks against its files.sha256"

# split: parts, joined the same as the whole
build "$tmp/c" --part-size 100000 >"$tmp/c.log" 2>&1 || { cat "$tmp/c.log"; fail "split build"; }
if [ ! -f "$tmp/c/$pack.part1" ] || [ ! -f "$tmp/c/$pack.part2" ]; then fail "no parts"; fi
[ ! -e "$tmp/c/$pack" ] || fail "a split pack left the whole file"
cat "$tmp/c/$pack".part* | cmp - "$tmp/a/$pack" || fail "the parts joined are not the pack"
echo "ok: a big pack splits, and its parts join to the pack"

# --only (story 51.18, the update rehearsal's one pack): that pack alone; an id the
# platform has no pack of fails the build
build "$tmp/e" --only tool.fake >"$tmp/e.log" 2>&1 || { cat "$tmp/e.log"; fail "--only build"; }
cmp "$tmp/e/$pack" "$tmp/a/$pack" || fail "--only built other bytes"
if build "$tmp/f" --only tool.nope >"$tmp/f.log" 2>&1; then fail "--only of no pack built"; fi
grep -q "tool.nope is not one of linux-amd64's packs" "$tmp/f.log" || { cat "$tmp/f.log"; fail "--only not refused"; }
echo "ok: --only builds that pack alone, and refuses an id with no pack"

# a recipe whose link leaves the folder fails the build
cat >"$tmp/recipes/tool.fake.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo hi >"$1/a.txt"
ln -s ../../../../etc/passwd "$1/escape"
SH
if build "$tmp/d" >"$tmp/d.log" 2>&1; then fail "a link out of the pack was built"; fi
grep -q "leaves the folder" "$tmp/d.log" || { cat "$tmp/d.log"; fail "not refused for the link"; }
echo "ok: a link out of the folder is refused"

# runtime.ollama's recipe (story 51.16, a Core Mac's model server): the pinned archive
# checked against its sha256, ollama and llama-server kept as they are (MLX left out),
# with their licences; an archive that is not the pinned one fails the recipe. A fake
# archive and pins stand in for Ollama's release.
ol="$tmp/ollama"
mkdir -p "$ol/src/mlx_metal_v3" "$ol/root/tools/release/mac-app"
for b in ollama llama-server; do printf '#!/bin/sh\necho %s\n' "$b" >"$ol/src/$b"; done
echo "MIT" >"$ol/src/LICENSE"
echo "mlx" >"$ol/src/MLX-LICENSE"
echo "big" >"$ol/src/mlx_metal_v3/lib.dylib"
tar -czf "$ol/ollama-darwin.tgz" -C "$ol/src" .
if command -v sha256sum >/dev/null; then sum="$(sha256sum "$ol/ollama-darwin.tgz" | cut -d' ' -f1)"; else sum="$(shasum -a 256 "$ol/ollama-darwin.tgz" | cut -d' ' -f1)"; fi
pins() { printf 'OLLAMA_VERSION=0.40.2\nOLLAMA_URL=file://%s\nOLLAMA_SHA256=%s\n' "$ol/ollama-darwin.tgz" "$1" >"$ol/root/tools/release/mac-app/pins.env"; }
pins "$sum"
recipe() { WORK="$ol/work-$1" REPO_ROOT="$ol/root" bash "$here/recipes/runtime.ollama.sh" "$ol/stage-$1" "$2" 0.4.2; }
recipe a darwin-arm64 >"$ol/a.log" 2>&1 || { cat "$ol/a.log"; fail "runtime.ollama's recipe"; }
for f in ollama/ollama ollama/llama-server licenses/ollama/LICENSE licenses/ollama.txt; do
    [ -f "$ol/stage-a/$f" ] || fail "runtime.ollama's pack has no $f"
done
[ -x "$ol/stage-a/ollama/ollama" ] || fail "runtime.ollama's ollama is not executable"
cmp "$ol/src/ollama" "$ol/stage-a/ollama/ollama" || fail "runtime.ollama's ollama is not Ollama's"
if [ -e "$ol/stage-a/licenses/ollama/MLX-LICENSE" ] || find "$ol/stage-a" -name 'mlx*' | grep -q .; then
    fail "runtime.ollama's pack carries the MLX engine"
fi
if recipe b linux-amd64 >"$ol/b.log" 2>&1; then fail "runtime.ollama built for linux-amd64"; fi
pins "0000000000000000000000000000000000000000000000000000000000000000"
if recipe c darwin-arm64 >"$ol/c.log" 2>&1; then fail "runtime.ollama took an archive that is not the pinned one"; fi
grep -q "pinned 0000" "$ol/c.log" || { cat "$ol/c.log"; fail "runtime.ollama's sha256 refusal"; }
echo "ok: runtime.ollama's recipe gathers the pinned Ollama, and refuses another"

# Epic 52 (story 52.16): a language pack's recipe gathers the pinned dictionary files and the
# pack's NOTICE, and refuses a file whose sha256 is not the pin
lg="$tmp/lang"
mkdir -p "$lg/src/fr_FR" "$lg/root/pipeline/src/maktaba_pipeline/language_packs/fr"
printf 'SET UTF-8\n' >"$lg/src/fr_FR/fr.aff"
printf '1\nbonjour\n' >"$lg/src/fr_FR/fr.dic"
sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }
langsrc() {
    cat >"$lg/sources.toml" <<TOML
commit = "fixture"
base = "file://$lg/src"
[pack."lang.hunspell.fr"]
files = [
  { path = "fr_FR/fr.aff", name = "fr.aff", sha256 = "$1", size = 10 },
  { path = "fr_FR/fr.dic", name = "fr.dic", sha256 = "$(sum "$lg/src/fr_FR/fr.dic")", size = 10 },
]
TOML
}
lrecipe() {
    mkdir -p "$lg/work-$1"
    LANG_SOURCES="$lg/sources.toml" PACK_ID=lang.hunspell.fr WORK="$lg/work-$1" REPO_ROOT="$REPO_ROOT" \
        bash "$here/recipes/lang.hunspell.sh" "$lg/stage-$1" linux-amd64 0.4.2
}
langsrc "$(sum "$lg/src/fr_FR/fr.aff")"
lrecipe a >"$lg/a.log" 2>&1 || { cat "$lg/a.log"; fail "lang.hunspell.fr's recipe"; }
for f in fr.aff fr.dic NOTICE; do [ -f "$lg/stage-a/$f" ] || fail "lang.hunspell.fr's pack has no $f"; done
cmp "$lg/src/fr_FR/fr.dic" "$lg/stage-a/fr.dic" || fail "lang.hunspell.fr's fr.dic is not the pinned one"
grep -q "MPL-2.0" "$lg/stage-a/NOTICE" || fail "lang.hunspell.fr's NOTICE has no licence"
langsrc "0000000000000000000000000000000000000000000000000000000000000000"
if lrecipe b >"$lg/b.log" 2>&1; then fail "lang.hunspell.fr took a file that is not the pinned one"; fi
grep -q "pinned 0000" "$lg/b.log" || { cat "$lg/b.log"; fail "lang.hunspell.fr's sha256 refusal"; }
echo "ok: a language pack's recipe gathers the pinned files, and refuses another"
