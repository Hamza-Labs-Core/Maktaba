# Implementation Plan — Story 20.2 Fixtures & Seed Data

> Companion to [story-20-02-fixtures-seed-data.md](story-20-02-fixtures-seed-data.md).
> Reproducible, royalty-free fixtures, total ≤ 50 MiB committed; seeded DB
> dump for 1 k videos / 10 k segments; fixture LICENSE per sample.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Committed media | Under `shared/fixtures/samples/`. AAC + h.264, short. |
| 4K HDR | Downloaded by `make fixtures` from a documented mirror; checksum-verified; not committed. |
| Seeded DB | `shared/fixtures/seeded_db.sql.zst` — Postgres-compatible dump, < 5 MB, ≤ 5 s load. |
| Probe goldens | `shared/fixtures/expected/{name}.probe.json`. |
| Transcript goldens | `shared/fixtures/expected/{name}.transcript.json`. |
| Out of scope | Capacity 50k fixture (Story 19.1); perf 1k fixture (Story 18.1). |

## 1. Project layout

```
shared/fixtures/
├── LICENSE                       # per-sample provenance
├── samples/
│   ├── arabic-lecture-60s.mp4    # 60 s, 720p, AAC
│   ├── english-clip-60s.mp4
│   ├── mixed-language-60s.mp4
│   ├── multitrack-2a-2s.mkv      # 2 audio (en+ar) + 2 sub
│   └── rtl-اسم-ملف.mp4           # RTL filename test (EC3)
├── expected/
│   ├── arabic-lecture-60s.probe.json
│   ├── arabic-lecture-60s.transcript.json
│   └── ...
├── checksums.txt                 # sha256
├── seeded_db.sql.zst             # 1k videos / 10k segments
├── 4k-hdr.manifest.json          # download-on-demand spec
├── no-audio.mp4                  # EC1
├── corrupt-moov.mp4              # EC2
└── README.md
scripts/
├── fixtures-make.sh              # download 4K HDR + verify
├── fixtures-check.sh             # size guard
└── seed-db.sh
Makefile
```

## 2. Sample manifest

```json
// shared/fixtures/4k-hdr.manifest.json
{
  "samples": [
    {
      "name": "foreman-cif.y4m",
      // FIXME(plan-20-02): pin a real upstream URL. Suggested starting point:
      //   https://media.xiph.org/video/derf/y4m/foreman_cif.y4m
      // Verify the URL still resolves before merge, fill in the sha256 from
      // `sha256sum`, and update size_bytes. If a different upstream is chosen
      // (e.g., a Blender Foundation cosmos-laundromat mirror with a stable
      // HTTPS URL), document the exact source the team has reproduced from.
      "url": "https://media.xiph.org/video/derf/y4m/foreman_cif.y4m",
      "sha256": "TODO_FILL_AFTER_VERIFY",
      "size_bytes": 0,
      "license": "Xiph.org test sequence, freely redistributable"
    }
  ]
}
```

## 3. LICENSE format

The `LICENSE` file is a sequence of stanzas separated by a blank line. Each
stanza has a header line (the file name) followed by **exactly** these
`key: value` lines (`key` is at column 0; values are trimmed of leading/
trailing whitespace; multi-line values are not allowed):

```text
shared/fixtures/LICENSE

arabic-lecture-60s.mp4
  Source: self-recorded (Maktaba project), 2026-04-12
  License: CC0
  Notes: Public domain reading of سورة الفاتحة, 60s.

english-clip-60s.mp4
  Source: LibriVox public-domain audio + colorbar overlay
  License: PD
  Notes: 60-second excerpt with colorbar overlay added.
```

Required keys per stanza: `Source`, `License`, `Notes`. Unknown keys cause
the parser to fail. The parser is implemented as the tiny grammar:

```text
file       := stanza ("\n" stanza)* "\n"?
stanza     := header (kv_line)+
header     := /^[^\s][^\n]*$/                  ; the file name in samples/
kv_line    := /^  (Source|License|Notes): .+$/ ; two-space indent, fixed keys
```

`scripts/fixtures-check.sh` parses the file using this grammar and asserts
every entry under `samples/` has a stanza, every stanza has the three
required keys, and no stanza references a missing file.

## 4. Probe goldens

Computed by:

```bash
ffprobe -v error -print_format json -show_format -show_streams \
    shared/fixtures/samples/arabic-lecture-60s.mp4 \
  > shared/fixtures/expected/arabic-lecture-60s.probe.json
```

Stripped of timestamps/build info AND canonicalized for floating-point
representation via a normalizer. `ffmpeg`/`ffprobe` is pinned in fixture
metadata (`shared/fixtures/ffmpeg-version.txt` records the exact build,
e.g. `ffmpeg 6.1.2`); CI fails fast if the running tool reports a different
version when regenerating goldens.

```python
# scripts/normalize_probe.py
import json

_FLOAT_KEYS = ("duration", "start_time", "bit_rate", "r_frame_rate")
_PRECISION  = 6  # six decimal places — enough for ffprobe outputs


def _canonicalize_floats(v):
    """Canonicalize numbers: round to fixed precision, strip trailing zeros."""
    if isinstance(v, float):
        s = f"{v:.{_PRECISION}f}".rstrip("0").rstrip(".")
        return s if s else "0"
    return v


def normalize(j):
    j["format"].pop("filename", None)
    j["format"].pop("size", None)               # depends on path / FS
    for s in j.get("streams", []):
        s.pop("codec_long_name", None)          # build-string dependent
        s.pop("disposition", None)              # ordering noise
    # Canonicalize float-shaped scalars (ffprobe emits them as strings, but
    # round-tripping through json.loads can land them as floats).
    def walk(obj):
        if isinstance(obj, dict):
            return {k: walk(_canonicalize_floats(v)) for k, v in obj.items()}
        if isinstance(obj, list):
            return [walk(_canonicalize_floats(x)) for x in obj]
        return obj
    return walk(j)
```

Unit test asserts `ffprobe(sample) == expected.probe.json` byte-for-byte
after normalize, with sorted keys (`json.dumps(..., sort_keys=True)`). TC1
thus boils down to a goldens-equality test repeated 10×.

## 5. Seeded DB

The seeded DB targets the canonical schema (architecture.md §1368):
`transcript_segments(id, transcript_id, seq, start_sec, end_sec, text)`.
Rows must be inserted in FK order: `users` → `libraries` → `videos` →
`audio_tracks` → `transcripts` → `transcript_segments`.

```sql
-- generated by scripts/generate-seeded-db.go (committed output zst-compressed)
-- 1 user, 1 library, 1000 videos, 1000 audio_tracks, 1000 transcripts,
-- 10000 transcript_segments. Indexes built after COPY.

INSERT INTO users (id, email, ...) VALUES (...);
INSERT INTO libraries (id, owner_id, name, root_path, ...) VALUES (...);

-- 1000 videos under the library.
COPY videos (id, content_hash, library_id, path, duration_sec, ...) FROM stdin;
...
\.

-- One audio track per video (track 0).
COPY audio_tracks (id, video_id, track_index, codec, language, ...) FROM stdin;
...
\.

-- One transcript per audio_track (model + language tagged).
COPY transcripts (id, video_id, audio_track_id, model, language, ...) FROM stdin;
...
\.

-- 10 segments per transcript (10000 total). NOTE columns: transcript_id,
-- seq, start_sec, end_sec, text.
COPY transcript_segments (id, transcript_id, seq, start_sec, end_sec, text) FROM stdin;
...
\.

-- Build indexes after bulk load.
ANALYZE;
```

Load:

```bash
# scripts/seed-db.sh
set -euo pipefail
DB=${1?dbname}
zstd -dc shared/fixtures/seeded_db.sql.zst | psql -d "$DB" -v ON_ERROR_STOP=1
```

Benchmarked load time ≤ 5 s on M2 / 16 GB.

## 6. Size guard

```bash
#!/usr/bin/env bash
# scripts/fixtures-check.sh
set -euo pipefail

MAX_FILE_MIB=10           # bumped from 5 to accommodate multitrack-2a-2s.mkv
MAX_TOTAL_MIB=50

bad=0
total_kb=0
while IFS= read -r f; do
    kb=$(du -k "$f" | cut -f1)
    total_kb=$((total_kb + kb))
    if [ "$kb" -gt $((MAX_FILE_MIB * 1024)) ]; then
        echo "FAIL: $f ${kb}KiB > ${MAX_FILE_MIB}MiB"; bad=1
    fi
done < <(find shared/fixtures/samples shared/fixtures/expected -type f)

total_mib=$((total_kb / 1024))
if [ "$total_mib" -gt $MAX_TOTAL_MIB ]; then
    echo "FAIL: total ${total_mib}MiB > ${MAX_TOTAL_MIB}MiB"; bad=1
fi
exit $bad
```

## 7. Re-download 4K HDR

```bash
#!/usr/bin/env bash
# scripts/fixtures-make.sh
set -euo pipefail
MANIFEST=shared/fixtures/4k-hdr.manifest.json
DEST=shared/fixtures/4k-hdr/

mkdir -p "$DEST"
for entry in $(jq -c '.samples[]' "$MANIFEST"); do
    name=$(jq -r '.name'   <<<"$entry")
    url=$(jq  -r '.url'    <<<"$entry")
    want=$(jq -r '.sha256' <<<"$entry")
    out="$DEST/$name"
    for try in 1 2 3; do
        if [ ! -f "$out" ] || [ "$(sha256sum "$out" | awk '{print $1}')" != "$want" ]; then
            curl -fsSL "$url" -o "$out.tmp" && mv "$out.tmp" "$out"
        fi
        got=$(sha256sum "$out" | awk '{print $1}')
        if [ "$got" = "$want" ]; then break; fi
        echo "checksum mismatch on $name (got $got want $want), redownloading…"
        rm -f "$out"
    done
    got=$(sha256sum "$out" | awk '{print $1}')
    if [ "$got" != "$want" ]; then
        echo "FAIL: persistent checksum mismatch for $name"; exit 1
    fi
done
```

## 8. Edge case fixtures

### EC1 — no-audio.mp4
Generated:

```bash
ffmpeg -f lavfi -i color=size=320x240:duration=10:rate=15 -c:v libx264 -an no-audio.mp4
```

Test asserts `Pipeline.transcribe` returns `state=SKIPPED_NO_AUDIO` (Epic 3).

### EC2 — corrupt-moov.mp4
Generated by walking the ISO BMFF atom tree (size, fourcc) at the top
level and corrupting the `moov` atom's size/type bytes. Substring matching
on `b"moov"` is wrong — `moov` can appear inside `udta` strings, sample
tables, or arbitrary chunk payloads — so we parse atom headers from
offset 0:

```python
import struct

def find_top_level_atom(data: bytes, fourcc: bytes) -> int:
    """Return the start offset of `fourcc` at the top level, or -1."""
    off = 0
    while off + 8 <= len(data):
        size = struct.unpack(">I", data[off:off+4])[0]
        atype = data[off+4:off+8]
        if size == 1:                  # 64-bit extended size
            size = struct.unpack(">Q", data[off+8:off+16])[0]
        if size < 8 or off + size > len(data):
            return -1                  # malformed input; bail out
        if atype == fourcc:
            return off
        off += size
    return -1


data = bytearray(open("good.mp4", "rb").read())
moov = find_top_level_atom(bytes(data), b"moov")
assert moov >= 0, "no top-level moov atom in input"
# Corrupt the size+type header (bytes [moov, moov+8)) so any parser sees
# a malformed atom instead of valid moov contents.
data[moov:moov+8] = b"\x00" * 8
open("corrupt-moov.mp4", "wb").write(data)
```

Probe test asserts `ProbeError` classification, no panic.

### EC3 — RTL filename
`shared/fixtures/samples/rtl-اسم-ملف.mp4`. Test scans it, asserts stored `path` (NFC-normalized), search by Arabic text in segments returns the file.

## 9. Test cases

### TC1 — Determinism
`shared/fixtures/expected/<name>.probe.json` regenerated and diffed 10 times in a loop. Assert byte-stable.

### TC2 — Size guard
`make fixtures-check` exits non-zero when a 10+ MiB file is added under `samples/`.

### TC3 — Re-download
Mock the URL to serve wrong checksum once then correct content. Verify recovery; verify persistent failure aborts after 3 tries.

## 10. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 no audio | story | `no-audio.mp4` fixture; pipeline skip path. |
| EC2 corrupt moov | story | `corrupt-moov.mp4`; probe classified error. |
| EC3 RTL filename | story | `rtl-اسم-ملف.mp4`; NFC-normalised in DB. |
| Probe diff noise from build info | impl | Normalizer strips. |
| Test runs offline | impl | `make fixtures` is opt-in; samples needed for tests are committed. |

## 11. Make targets

```makefile
.PHONY: fixtures fixtures-check fixtures-clean
fixtures:
	scripts/fixtures-make.sh
fixtures-check:
	scripts/fixtures-check.sh
fixtures-clean:
	rm -rf shared/fixtures/4k-hdr/
```

## 12. Dependencies

- Story 20.1 (test tiers).
- Epic 1 scanner, Epic 2/3 probe + transcribe (consume fixtures).
- Epic 5 search (Arabic text fixtures in seeded DB).
