#!/usr/bin/env bash
# The isolated runner's checks (story 54.16 §1: C7's ClamAV scan, C8 the sandbox run, C10 the
# reproducible build), on a package and its source archive, with NO network (the caller
# cuts egress before this runs) and no secret. Writes the report JSON to $3.
#
#   run.sh <package.mkplugin> <source archive> <report.json>
#
# Run by store-check-runner.yml (the CI fallback) and by the container image beside it.
# Needs: mkp (plugind/crates/mkp), zstd, python3, clamscan (optional: C7 is then skipped).
set -euo pipefail
pkg="$1"; src="$2"; out="$3"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
here="$(cd "$(dirname "$0")" && pwd)"

json() { python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"; }

# the package, unpacked by the safe extractor (no links, no paths out, regular files only)
mkdir -p "$work/pkg" "$work/src"
zstd -dc "$pkg" | python3 "$here/safe_extract.py" "$work/pkg"
if ! zstd -dc "$src" 2>/dev/null | python3 "$here/safe_extract.py" "$work/src"; then
  # a source archive that is not zstd: a plain tar or a gzip one
  python3 "$here/safe_extract.py" "$work/src" <"$src" || true
fi

# C7: ClamAV on both
if command -v clamscan >/dev/null; then
  if clamscan --no-summary --infected -r "$work/pkg" "$work/src" >"$work/clam.txt" 2>&1; then
    c7='{"result":"pass","detail":"ClamAV: no hit"}'
  elif [ $? -eq 1 ]; then
    c7="{\"result\":\"block\",\"code\":\"malware\",\"detail\":$(json "$(head -c 2000 "$work/clam.txt")")}"
  else
    c7='{"result":"skip","detail":"ClamAV failed to run"}'
  fi
else
  c7='{"result":"skip","detail":"ClamAV is not installed here"}'
fi

# C8: every stage on the plugin's own fixtures (fixtures/*.json in the source archive) and
# on generated events, in the harness with strict limits
python3 "$here/generate_fixtures.py" "$work/generated.json"
c8="$(python3 "$here/sandbox_run.py" "$work/pkg" "$work/src/fixtures" "$work/generated.json")"

# C10: a Rust source builds to the same component (reproducible), otherwise noted
c10='{"result":"skip","detail":"not a reproducible toolchain (Rust or TinyGo)"}'
if [ -f "$work/src/Cargo.toml" ]; then
  if (cd "$work/src" && cargo build --offline --locked --release --target wasm32-wasip2 >"$work/build.txt" 2>&1); then
    built="$(find "$work/src/target/wasm32-wasip2/release" -maxdepth 1 -name '*.wasm' | head -1)"
    if [ -n "$built" ] && [ "$(sha256sum <"$built" | cut -c1-64)" = "$(sha256sum <"$work/pkg/plugin.wasm" | cut -c1-64)" ]; then
      c10='{"result":"pass","reproducible":true,"detail":"the source builds to the same plugin.wasm"}'
    else
      c10='{"result":"warn","reproducible":false,"detail":"the source builds to another plugin.wasm"}'
    fi
  else
    c10="{\"result\":\"warn\",\"reproducible\":false,\"detail\":$(json "$(tail -c 2000 "$work/build.txt")")}"
  fi
fi

printf '{"c7":%s,"c8":%s,"c10":%s}\n' "$c7" "$c8" "$c10" >"$out"
