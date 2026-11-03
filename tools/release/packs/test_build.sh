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
[ -f "$tmp/c/$pack.part1" ] && [ -f "$tmp/c/$pack.part2" ] || fail "no parts"
[ ! -e "$tmp/c/$pack" ] || fail "a split pack left the whole file"
cat "$tmp/c/$pack".part* | cmp - "$tmp/a/$pack" || fail "the parts joined are not the pack"
echo "ok: a big pack splits, and its parts join to the pack"

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
