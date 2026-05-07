# Implementation Plan — Story 1.2 Content-Addressable Identity (BLAKE3)

> Companion to [story-01-02-content-identity.md](story-01-02-content-identity.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Go 1.23+. Identity is computed in the scanner stage; the scanner runs as part of the Pipeline Service ([architecture.md §3.1](../../architecture.md)) but the hash primitive is a pure CPU/IO function and lives in a shared Go package so the API service can verify hashes on upload paths too. |
| Module location | `pipeline/internal/identity/` (Go package) — exposed to Python via cgo binding **only if needed**; current plan keeps Python's BLAKE3 (`blake3` PyPI package) reading the same constants from `shared/db/queries/identity.sql` so both implementations agree by construction. The Go path is the canonical one for tests. |
| Database surface | `videos.content_hash` column, `UNIQUE (library_id, content_hash)` index, `videos.metadata.additional_paths` JSONB array for in-library duplicates. Matches §1.1 of the story. |
| Out of scope | The watcher (Story 1.3), state-machine transitions out of `DISCOVERED` (Story 1.6), and the actual scanner walk loop (Story 1.1). This plan stops at "given a path, return a hash" plus the SQL that stores it. |

## 1. Architecture diagram

```
                            scanner.WalkRoot()  (Story 1.1)
                                    │
                                    ▼
                     ┌──────────────────────────────┐
                     │  identity.HashFile(path)     │ ← Story 1.2
                     │                              │
                     │   ┌──────────────────────┐   │
                     │   │  os.Stat(path)       │   │  size, mtime
                     │   └──────────────────────┘   │
                     │              │               │
                     │              ▼               │
                     │   if size <= 8 MiB           │
                     │     → stream entire file     │
                     │   else                       │
                     │     → pread head 4 MiB       │
                     │     → pread tail 4 MiB       │
                     │                              │
                     │              ▼               │
                     │   blake3.Hasher              │
                     │     .Write(head)             │
                     │     .Write(tail)             │
                     │     .Write(size_le_u64)      │
                     │     .Sum(nil)                │
                     │              │               │
                     │              ▼               │
                     │   hex.EncodeToString(...)    │
                     └──────────────────────────────┘
                                    │
                                    ▼
                       ┌──────────────────────────┐
                       │  storeOrLink(library,    │
                       │              path, hash) │
                       └──────────────────────────┘
                          │                    │
                INSERT ON CONFLICT      already exists in same lib?
                          │                    │
                          ▼                    ▼
                ┌──────────────┐   ┌──────────────────────────┐
                │ new videos   │   │ append path to           │
                │ row (DISCO)  │   │ metadata.additional_paths │
                └──────────────┘   │ INFO log: duplicate_hash │
                                   └──────────────────────────┘
```

The hash flow itself is deliberately tiny:

```
file ──► [head 4 MiB] ──┐
                        ├──► BLAKE3 ──► 32 bytes ──► hex(64 chars)
file ──► [tail 4 MiB] ──┤
                        │
                  size_u64_le ─────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/internal/identity/hasher.go` | `Hasher` struct, public `HashFile`, `HashReader` |
| `pipeline/internal/identity/hasher_test.go` | Unit + golden-vector tests |
| `pipeline/internal/identity/hasher_bench_test.go` | Benchmarks (real disk + sparse fixture) |
| `pipeline/internal/identity/io_budget_test.go` | I/O accounting test (≤ 8 MiB reads) |
| `pipeline/internal/identity/doc.go` | Package docstring + invariants |
| `shared/db/migrations/0003_videos_content_hash.sql` | Adds column + constraints (see §4) |
| `shared/db/queries/identity.sql` | sqlc input — insert/lookup queries |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/go.mod` | Add `lukechampine.com/blake3 v1.x` |
| `pipeline/internal/scanner/scan.go` (Story 1.1) | Call `identity.HashFile` on each accepted path |
| `specs/epics/01-scanner/README.md` | Tick story 1.2 once landed |

### 2.3 Function signatures (canonical)

```go
package identity

// HeadTailBytes is the per-region budget. 4 MiB head + 4 MiB tail = 8 MiB max disk read per file.
const HeadTailBytes int64 = 4 * 1024 * 1024

// Hasher captures parameters in case we ever tune them; default is sufficient.
type Hasher struct {
    HeadTail int64 // 0 → HeadTailBytes
}

// HashFile opens path, reads at most 2*HeadTail bytes, and returns the canonical content hash.
// Returns ("", err) on any IO error; never returns ("", nil).
func (h Hasher) HashFile(path string) (hex string, size int64, err error)

// HashReader is the seam tests use: any io.ReaderAt + size feeds the same algorithm.
func (h Hasher) HashReader(r io.ReaderAt, size int64) (hex string, err error)

// Default is a zero-allocation singleton with HeadTail = HeadTailBytes.
var Default = Hasher{}
```

### 2.4 Algorithm (prose)

The canonical formula — applied uniformly for every file size, no
shortcuts — is:

```
content_hash = BLAKE3( first_HT_bytes || last_HT_bytes || size_le_u64 )
```

where `HT = min(HeadTail, size)`. The two regions may overlap (or be
identical, for files smaller than HeadTail), but they are still emitted
to the hasher twice. Codifying this without short-circuiting is the only
way the hash stays stable across the size = `2*HeadTail` boundary.

Given a file of `size` bytes:

1. Open it once. `O_RDONLY`. No `O_DIRECT`; we *want* the page cache to keep tails warm for re-scans.
2. Compute `ht = min(HeadTail, size)`.
3. `pread(buf[:ht], 0)` — read head; `hasher.Write(head)`.
4. `pread(buf[:ht], size - ht)` — read tail (may overlap head; for
   `size <= HeadTail` the two reads return the same bytes);
   `hasher.Write(tail)`.
5. Append `size` as little-endian `uint64` (8 bytes) to the hasher.
6. `hex.EncodeToString(hasher.Sum(nil))` — 64 lowercase hex chars.

Step 5 is what makes a 1-byte append flip the hash even when both head and tail are unchanged.

## 3. Go code scaffolding

`pipeline/internal/identity/hasher.go`:

```go
// Package identity computes the canonical Maktaba content hash:
//
//   BLAKE3( first 4 MiB || last 4 MiB || size_le_u64 ) → 32 bytes → 64-char hex
//
// Files smaller than 2*HeadTail are hashed in full (head and tail overlap),
// which is also cheaper than seeking twice for a small file.
package identity

import (
    "encoding/binary"
    "encoding/hex"
    "errors"
    "fmt"
    "io"
    "os"

    "lukechampine.com/blake3"
)

const (
    // HeadTailBytes — 4 MiB head, 4 MiB tail. Total disk budget per large file: 8 MiB.
    HeadTailBytes int64 = 4 * 1024 * 1024
    sizeSuffixLen       = 8 // little-endian uint64
)

var ErrEmptyHash = errors.New("identity: empty hash result")

type Hasher struct {
    HeadTail int64
}

var Default = Hasher{}

func (h Hasher) headTail() int64 {
    if h.HeadTail <= 0 {
        return HeadTailBytes
    }
    return h.HeadTail
}

// HashFile is the entry point used by the scanner.
func (h Hasher) HashFile(path string) (string, int64, error) {
    f, err := os.Open(path)
    if err != nil {
        return "", 0, fmt.Errorf("identity: open %s: %w", path, err)
    }
    defer f.Close()

    fi, err := f.Stat()
    if err != nil {
        return "", 0, fmt.Errorf("identity: stat %s: %w", path, err)
    }
    if !fi.Mode().IsRegular() {
        return "", 0, fmt.Errorf("identity: %s is not a regular file", path)
    }
    size := fi.Size()

    sum, err := h.HashReader(f, size)
    return sum, size, err
}

// HashReader hashes via ReaderAt — used by tests with bytes.Reader / sparse fixtures.
//
// The canonical formula is BLAKE3( head || tail || size_le_u64 ), where
// head = first ht bytes, tail = last ht bytes, ht = min(HeadTail, size).
// For size <= HeadTail the two regions are the same byte range; we still
// write them to the hasher twice so the formula is uniform across sizes.
func (h Hasher) HashReader(r io.ReaderAt, size int64) (string, error) {
    ht := h.headTail()
    if size < ht {
        ht = size
    }
    hasher := blake3.New(32, nil)

    headBuf := make([]byte, ht)
    if _, err := io.ReadFull(io.NewSectionReader(r, 0, ht), headBuf); err != nil {
        return "", fmt.Errorf("identity: read head: %w", err)
    }
    if _, err := hasher.Write(headBuf); err != nil {
        return "", err
    }

    tailOff := size - ht
    if tailOff == 0 {
        // size <= HeadTail: head and tail are the same byte range. Honor
        // the formula by writing the buffer to the hasher a second time.
        if _, err := hasher.Write(headBuf); err != nil {
            return "", err
        }
    } else {
        tailBuf := make([]byte, ht)
        if _, err := io.ReadFull(io.NewSectionReader(r, tailOff, ht), tailBuf); err != nil {
            return "", fmt.Errorf("identity: read tail: %w", err)
        }
        if _, err := hasher.Write(tailBuf); err != nil {
            return "", err
        }
    }

    var sizeBuf [sizeSuffixLen]byte
    binary.LittleEndian.PutUint64(sizeBuf[:], uint64(size))
    if _, err := hasher.Write(sizeBuf[:]); err != nil {
        return "", err
    }

    sum := hasher.Sum(nil)
    if len(sum) == 0 {
        return "", ErrEmptyHash
    }
    return hex.EncodeToString(sum), nil
}

```

Why `io.NewSectionReader` instead of `f.ReadAt` directly? It gives us a clean `io.Reader` for `io.ReadFull`, which handles short reads from network filesystems without us reinventing the loop.

## 4. Database migrations

`shared/db/migrations/0003_videos_content_hash.sql` — applied on top of the `videos` table from §8.1 of architecture.md (migration `0001_init_libraries_and_videos.sql`).

```sql
-- +goose Up
-- +goose StatementBegin

-- 1. Drop the old global UNIQUE on content_hash (introduced in 0001) and replace
--    with the per-library UNIQUE that the story spec mandates.
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_content_hash_key;

-- 2. Per-library uniqueness — the canonical identity for a (library, content) pair.
ALTER TABLE videos
    ADD CONSTRAINT videos_library_content_hash_key
    UNIQUE (library_id, content_hash);

-- 3. Validate the hash format at the SQL boundary so corrupt rows can never
--    sneak in via a buggy worker. 64 lowercase hex chars (lukechampine.com/blake3
--    only emits lowercase, so the constraint is tight by construction).
ALTER TABLE videos
    ADD CONSTRAINT videos_content_hash_format_chk
    CHECK (content_hash ~ '^[0-9a-f]{64}$');

-- 4. Index for "find me every row in this library by content hash" — covers
--    the duplicate-detection path on insert.
CREATE INDEX IF NOT EXISTS videos_content_hash_lookup_idx
    ON videos (content_hash);

-- 5. Functional index over the additional_paths array so that
--    "which row owns this path?" is fast even after a rename round-trip.
--    The `metadata` JSONB column itself is owned by 0001
--    (plan-01-05 schema decisions); we only index the array here.
CREATE INDEX IF NOT EXISTS videos_additional_paths_gin_idx
    ON videos USING GIN ((metadata -> 'additional_paths'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_additional_paths_gin_idx;
DROP INDEX IF EXISTS videos_content_hash_lookup_idx;
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_content_hash_format_chk;
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_library_content_hash_key;
ALTER TABLE videos ADD CONSTRAINT videos_content_hash_key UNIQUE (content_hash);
-- +goose StatementEnd
```

`shared/db/queries/identity.sql` — sqlc input:

```sql
-- name: InsertOrLinkVideo :one
-- Insert a freshly-discovered file. If a row already exists with the same
-- (library_id, content_hash), append the path to additional_paths and return
-- the existing id. Caller logs `duplicate_content_hash` on link.
WITH ins AS (
    INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, state)
    VALUES ($1, $2, $3, $4, $5, $6, 'discovered')
    ON CONFLICT (library_id, content_hash) DO NOTHING
    RETURNING id, 'inserted'::text AS outcome
)
SELECT id, outcome FROM ins
UNION ALL
SELECT v.id,
       CASE WHEN v.path = $3 THEN 'noop' ELSE 'linked' END AS outcome
  FROM videos v
 WHERE v.library_id = $1
   AND v.content_hash = $2
   AND NOT EXISTS (SELECT 1 FROM ins)
 LIMIT 1;

-- name: AppendAdditionalPath :exec
UPDATE videos
   SET metadata = jsonb_set(
                    metadata,
                    '{additional_paths}',
                    COALESCE(metadata -> 'additional_paths', '[]'::jsonb)
                       || to_jsonb($2::text),
                    true)
 WHERE id = $1
   AND NOT (COALESCE(metadata -> 'additional_paths', '[]'::jsonb) @> to_jsonb($2::text));

-- name: UpdatePathOnRename :exec
-- When the canonical path moves, swap path and remove the new path from
-- additional_paths if it was there.
UPDATE videos
   SET path = $2,
       metadata = jsonb_set(
                    metadata,
                    '{additional_paths}',
                    COALESCE(metadata -> 'additional_paths', '[]'::jsonb)
                       - $2::text,
                    true),
       updated_at = now()
 WHERE id = $1;

-- name: GetVideoByLibraryAndHash :one
SELECT * FROM videos
 WHERE library_id = $1 AND content_hash = $2
 LIMIT 1;
```

The CTE in `InsertOrLinkVideo` makes the insert+lookup atomic; without it, a concurrent scanner thread could race between an `INSERT…ON CONFLICT` that returns nothing and a follow-up `SELECT`.

## 5. Performance analysis

### 5.1 Per-file cost model

For files ≥ 8 MiB:

| Operation | Cost |
|---|---|
| `open`/`fstat` | ~1 syscall, < 1 ms on warm inodes, ~5 ms cold over NFS |
| `pread(head)` | 1 seek + 4 MiB sequential read |
| `pread(tail)` | 1 seek + 4 MiB sequential read |
| BLAKE3 hash 8 MiB + 8 B | BLAKE3 single-thread on Apple-silicon ≈ 6.5 GB/s → 8 MiB takes ~1.2 ms |
| `INSERT…ON CONFLICT` | < 1 ms local, < 5 ms remote |

**Disk-bound floor.** On a 7200 RPM HDD (~12 ms avg seek, ~150 MB/s sequential):
- 2 seeks ≈ 24 ms
- 2 × 4 MiB reads ≈ 53 ms
- ≈ **80 ms wall-clock per file**

On NVMe SSD (~50 µs seek, ~3 GB/s):
- 2 seeks ≈ 0.1 ms
- 2 × 4 MiB ≈ 2.6 ms
- ≈ **3 ms wall-clock per file**

### 5.2 30 TB estimate

A 30 TB library at the project's design point (avg lecture file ~2 GB, so ~15,000 files):

| Storage | Per-file | Total wall-clock (single thread) |
|---|---|---|
| HDD | ~80 ms | 15,000 × 0.08 s ≈ **20 minutes** |
| NVMe | ~3 ms | 15,000 × 0.003 s ≈ **45 seconds** |

If the library is 1 M small (50 MiB) files instead — same 30 TB:

| Storage | Per-file | Total wall-clock (single thread) |
|---|---|---|
| HDD | ~80 ms | 1,000,000 × 0.08 s ≈ **22 hours** |
| NVMe | ~3 ms | 1,000,000 × 0.003 s ≈ **50 minutes** |

### 5.3 Parallel hashing strategy

**Bound the parallelism by the storage type, not by CPU.** BLAKE3 on a 1 MiB chunk is ~150 µs of CPU; the bottleneck is always the disk. The scanner uses a worker pool sized to:

```go
// pipeline/internal/scanner/pool.go (excerpt — lives in Story 1.1)
func recommendedWorkers(rootFS string) int {
    if isRotational(rootFS) {
        return 1                   // Random reads on HDD destroy throughput
    }
    return min(runtime.NumCPU(), 8) // Diminishing returns past ~8 on consumer NVMe
}
```

For HDD libraries, **serial** processing is the fastest correct answer; parallel hashing of a rotational disk causes the head to thrash and reduces aggregate throughput by 2–4×. This is why `recommendedWorkers` returns 1 for HDDs.

For SSDs/NVMe, an 8-worker pool with a buffered job channel (`make(chan string, 256)`) saturates the disk on most consumer drives. The scanner walks the tree in one goroutine and feeds paths in; the workers pull, hash, and `INSERT`.

**Memory budget.** Per worker: one 4 MiB buffer (`buf`) + BLAKE3 state (~2 KiB) + the SectionReader. 8 workers ≈ 32 MiB. Acceptable.

### 5.4 Re-hash skip

The scanner already keeps `(path, mtime, size)` in a per-library cache (Story 1.1). If `(mtime, size)` matches what's already on the row, hashing is skipped entirely — the most common case for periodic full sweeps. The hash is only recomputed when `mtime` changes or `size != stat.st_size` (the network-FS escape hatch in the story's edge cases).

## 6. Test plan

### 6.1 Unit tests (`hasher_test.go`)

| Test | What it pins |
|---|---|
| `TestHashGoldenVector_4MiBPattern` | Static fixture (`bytes.Repeat([]byte{0xAB}, 4*MiB)`) → hex matches a hand-computed expected value. Locks the algorithm forever. |
| `TestHashIsDeterministic` | Hash same fixture twice in the same process → equal. |
| `TestHashIsStableAcrossOpens` | Hash, close, reopen, hash → equal. Catches state leaks. |
| `TestHashSmallFileHeadTailFormula` | 1 MiB file (`size < HeadTail`): `HashReader(...)` matches `BLAKE3(content || content || size_le_u64)`. Asserts the small-file path applies the canonical head+tail formula uniformly (head and tail collapse to the same byte range, so the body is fed to the hasher twice). |
| `TestHashChangesOnSizeChange` | Append 1 byte → hex differs. (Pins the size suffix.) |
| `TestHashChangesOnByteFlip` | Flip byte at offset 100 in a 16 MiB file → hex differs. |
| `TestHashEqualHeadTailDifferentMiddleStillCollides` | Two 16 MiB files with identical head, tail, and size but different middle → hex is **identical**. *Documents the known limitation.* The story accepts this trade-off; the test exists so we notice if anyone "fixes" it without updating the spec. |
| `TestHashZeroByteFile` | Empty file → hex of `BLAKE3(size_le_u64=0)`. Compute by hand and pin. |
| `TestHashLargeSparseFile_30GB` | `truncate -s 30G` sparse fixture → produces a hash; **measures `read()` syscall count** via a wrapped `readAtCounter`; asserts ≤ 8 MiB total bytes read. |
| `TestHashFile_NotRegular` | Symlink (after `os.Lstat`/`os.Stat` resolution) — accept; pipe/device — return error containing "not a regular file". |

### 6.2 Integration tests (`pipeline/internal/scanner/scan_test.go`)

| Test | What it pins |
|---|---|
| `TestScannerLinksDuplicateInSameLibrary` | Copy a fixture to two paths in the same library root → exactly one `videos` row, second path appears in `metadata.additional_paths`, INFO log line `duplicate_content_hash` emitted. |
| `TestScannerCreatesTwoRowsAcrossLibraries` | Same bytes ingested into library A and library B → two rows, distinct `library_id`, identical `content_hash`. |
| `TestScannerRenameReusesRow` | Hash a file, move it, rescan → no new row; `videos.path` is updated; `additional_paths` does **not** grow because the old path no longer exists. |
| `TestScannerNetworkFsSizeMismatch` | Mock `fs.FileInfo` returning a size different from actual content (simulates SMB lying) → on next scan the row's `content_hash` and `size_bytes` are updated in place; no duplicate row. |

### 6.3 Benchmarks (`hasher_bench_test.go`)

```go
func BenchmarkHashFile_1GB(b *testing.B)    // tmpfs
func BenchmarkHashFile_30GB_Sparse(b *testing.B)
func BenchmarkHashSmallFile_1MiB(b *testing.B)
func BenchmarkHashParallel_8Workers(b *testing.B) // reports MiB/s aggregate
```

CI threshold: 1 GB hash on tmpfs must complete in < 10 ms (8 MiB read + ~1 ms hash + slack). If it regresses, something has gone very wrong with our IO path.

## 7. Test code scaffolding

`pipeline/internal/identity/hasher_test.go`:

```go
package identity_test

import (
    "bytes"
    "encoding/binary"
    "encoding/hex"
    "io"
    "os"
    "path/filepath"
    "sync/atomic"
    "testing"

    "lukechampine.com/blake3"

    "maktaba/pipeline/internal/identity"
)

const MiB = 1024 * 1024

func mustWriteFile(t *testing.T, path string, body []byte) {
    t.Helper()
    if err := os.WriteFile(path, body, 0o600); err != nil {
        t.Fatalf("write %s: %v", path, err)
    }
}

func TestHashIsDeterministic(t *testing.T) {
    body := bytes.Repeat([]byte{0xAB}, 16*MiB)
    p := filepath.Join(t.TempDir(), "f.bin")
    mustWriteFile(t, p, body)

    h1, _, err := identity.Default.HashFile(p)
    if err != nil {
        t.Fatal(err)
    }
    h2, _, err := identity.Default.HashFile(p)
    if err != nil {
        t.Fatal(err)
    }
    if h1 != h2 {
        t.Fatalf("non-deterministic: %s vs %s", h1, h2)
    }
    if len(h1) != 64 {
        t.Fatalf("hex length = %d, want 64", len(h1))
    }
}

func TestHashSmallFileHeadTailFormula(t *testing.T) {
    body := bytes.Repeat([]byte{0xCD}, MiB) // 1 MiB << 4 MiB HeadTail
    p := filepath.Join(t.TempDir(), "small.bin")
    mustWriteFile(t, p, body)

    got, _, err := identity.Default.HashFile(p)
    if err != nil {
        t.Fatal(err)
    }

    // Reference: BLAKE3(head || tail || size_le_u64). For files smaller
    // than HeadTail, head and tail are the SAME byte range, so the body
    // is written to the hasher twice — same shape as the large-file path.
    h := blake3.New(32, nil)
    h.Write(body)
    h.Write(body)
    var sb [8]byte
    binary.LittleEndian.PutUint64(sb[:], uint64(len(body)))
    h.Write(sb[:])
    want := hex.EncodeToString(h.Sum(nil))

    if got != want {
        t.Fatalf("small-file hash mismatch:\n got  %s\n want %s", got, want)
    }
}

func TestHashChangesOnSizeChange(t *testing.T) {
    base := bytes.Repeat([]byte{0xEE}, 12*MiB)
    dir := t.TempDir()
    a := filepath.Join(dir, "a.bin")
    b := filepath.Join(dir, "b.bin")
    mustWriteFile(t, a, base)
    mustWriteFile(t, b, append(append([]byte{}, base...), 0x00))

    ha, _, err := identity.Default.HashFile(a)
    if err != nil {
        t.Fatal(err)
    }
    hb, _, err := identity.Default.HashFile(b)
    if err != nil {
        t.Fatal(err)
    }
    if ha == hb {
        t.Fatalf("size suffix did not affect hash; both = %s", ha)
    }
}

func TestHashEqualHeadTailDifferentMiddle(t *testing.T) {
    // Documents the accepted trade-off: head+tail+size collisions are possible
    // by construction. If this test ever flips, the algorithm changed silently.
    head := bytes.Repeat([]byte{0x11}, 4*MiB)
    tail := bytes.Repeat([]byte{0x22}, 4*MiB)
    midA := bytes.Repeat([]byte{0xAA}, 4*MiB)
    midB := bytes.Repeat([]byte{0xBB}, 4*MiB)

    a := append(append([]byte{}, head...), midA...)
    a = append(a, tail...)
    b := append(append([]byte{}, head...), midB...)
    b = append(b, tail...)

    dir := t.TempDir()
    pa, pb := filepath.Join(dir, "a"), filepath.Join(dir, "b")
    mustWriteFile(t, pa, a)
    mustWriteFile(t, pb, b)

    ha, _, _ := identity.Default.HashFile(pa)
    hb, _, _ := identity.Default.HashFile(pb)
    if ha != hb {
        t.Fatalf("expected equal hashes (documented limitation); got %s vs %s", ha, hb)
    }
}

func TestHashZeroByteFile(t *testing.T) {
    p := filepath.Join(t.TempDir(), "zero")
    mustWriteFile(t, p, nil)

    got, size, err := identity.Default.HashFile(p)
    if err != nil {
        t.Fatal(err)
    }
    if size != 0 {
        t.Fatalf("size = %d, want 0", size)
    }

    h := blake3.New(32, nil)
    var sb [8]byte
    h.Write(sb[:]) // size = 0
    want := hex.EncodeToString(h.Sum(nil))
    if got != want {
        t.Fatalf("zero-byte hash:\n got  %s\n want %s", got, want)
    }
}

// readAtCounter wraps a ReaderAt and records total bytes read.
// Used to assert the I/O budget on a 30 GB sparse fixture.
type readAtCounter struct {
    r     io.ReaderAt
    bytes int64
}

func (c *readAtCounter) ReadAt(p []byte, off int64) (int, error) {
    n, err := c.r.ReadAt(p, off)
    atomic.AddInt64(&c.bytes, int64(n))
    return n, err
}

func TestHashIOBudget_30GBSparse(t *testing.T) {
    if testing.Short() {
        t.Skip("creates a 30 GB sparse file")
    }
    p := filepath.Join(t.TempDir(), "sparse.bin")
    f, err := os.Create(p)
    if err != nil {
        t.Fatal(err)
    }
    const size = int64(30) * 1024 * 1024 * 1024
    if err := f.Truncate(size); err != nil {
        t.Fatal(err)
    }
    if err := f.Close(); err != nil {
        t.Fatal(err)
    }

    f, err = os.Open(p)
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()

    counter := &readAtCounter{r: f}
    _, err = identity.Default.HashReader(counter, size)
    if err != nil {
        t.Fatal(err)
    }
    if counter.bytes > 8*MiB {
        t.Fatalf("read budget violated: %d bytes (max %d)", counter.bytes, 8*MiB)
    }
}
```

`pipeline/internal/identity/hasher_bench_test.go`:

```go
package identity_test

import (
    "bytes"
    "os"
    "path/filepath"
    "testing"

    "maktaba/pipeline/internal/identity"
)

func benchHashFileSize(b *testing.B, size int64) {
    p := filepath.Join(b.TempDir(), "bench.bin")
    f, err := os.Create(p)
    if err != nil {
        b.Fatal(err)
    }
    if _, err := f.Write(bytes.Repeat([]byte{0xA5}, int(size))); err != nil {
        b.Fatal(err)
    }
    f.Close()

    b.SetBytes(8 * MiB) // bytes actually read off disk
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _, err := identity.Default.HashFile(p)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkHashFile_1GB(b *testing.B)  { benchHashFileSize(b, 1024*MiB) }
func BenchmarkHashFile_16MiB(b *testing.B) { benchHashFileSize(b, 16*MiB) }
func BenchmarkHashFile_4MiB_SmallBranch(b *testing.B) {
    benchHashFileSize(b, 4*MiB) // exercises the head==tail overlap path
}
```

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| File `< 4 MiB` | Head and tail are the same byte range; the file content is written to the hasher twice, then the size suffix. Same formula as the large-file path. | `TestHashSmallFileHeadTailFormula` |
| Zero-byte file | Hash of `size_le_u64 = 0` only (no head/tail bytes to write). Valid 64-char hex. | `TestHashZeroByteFile` |
| File between 4 and 8 MiB | Head and tail overlap in the middle; both reads still happen, both ranges are written to the hasher. | `TestHashOverlappingHeadTail` |
| Symlink | `os.Open` follows symlinks; hashed path = target. **Symlinks themselves do not become videos rows** — the scanner's walk (Story 1.1) decides whether to follow. Identity treats whatever it gets as a regular file. | `IsRegular()` check |
| FIFO / device / socket | Rejected with `"not a regular file"` error; scanner skips and logs WARN. | `fi.Mode().IsRegular()` check |
| Sparse holes | BLAKE3 reads holes as zero bytes — accepted. Two sparse files of different total sizes never collide because of the size suffix. | `TestHashIOBudget_30GBSparse` |
| Network filesystem reports wrong size | Hash is recomputed on next scan if `stat.st_size != row.size_bytes`; the existing row is updated in place via `UpdatePathOnRename` + a size update. No duplicate row. | `TestScannerNetworkFsSizeMismatch` |
| File truncated mid-hash | `pread(tail)` reads past EOF → `io.ErrUnexpectedEOF` from `io.ReadFull` → caller retries the entire scan for that path on next sweep. We do **not** silently emit a partial-content hash. | `io.ReadFull` semantics |
| File grows mid-hash | The size we recorded in `os.Stat` no longer matches the actual file. We hash with the stat-time size; the next scan sweep will pick up the new mtime/size and re-hash. Acceptable — the hash is still self-consistent for *that* size. | Documented; not separately tested |
| Read permission denied | `os.Open` returns `fs.ErrPermission`; scanner logs WARN and skips. | Standard error path |
| File deleted between Stat and Open | `os.Open` returns `fs.ErrNotExist`; scanner logs DEBUG and skips. | Standard error path |
| Path with non-UTF-8 bytes | Hashed identically; the path string is opaque to identity. | Inherited from Go's `os` |

## 9. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `lukechampine.com/blake3` | v1.4.x | Pure-Go, zero cgo, includes SIMD for AVX2/NEON. ~2× the throughput of `github.com/zeebo/blake3` on Apple Silicon, comparable on x86. Stable for years. License: MIT. |
| `github.com/jackc/pgx/v5` | already in `go.mod` | DB driver for the `InsertOrLinkVideo` path. |
| `github.com/sqlc-dev/sqlc` | dev-only | Generates Go from `shared/db/queries/identity.sql`. |

**Considered and rejected:** `github.com/zeebo/blake3` — slower on ARM64; `github.com/cloudflare/circl/hash/blake3` — overkill (whole crypto library), not maintained as a focused implementation.

**Python side** (out of scope for this story but for the symmetry check): `blake3==0.4.x` from PyPI uses the same Rust core as the reference implementation; verified to produce byte-identical output for the same `(head, tail, size_le_u64)` input in `tests/cross_lang_hash_test.py` (added as part of Story 1.1 integration).

## 10. Acceptance checklist

Before this story is marked done:

**Code**
- [ ] `pipeline/internal/identity/hasher.go` exposes `Hasher`, `Default`, `HashFile`, `HashReader`, `HeadTailBytes`.
- [ ] `pipeline/internal/identity/hasher_test.go` covers all unit tests in §6.1 and they pass.
- [ ] `pipeline/internal/identity/hasher_bench_test.go` benchmarks in §6.3 run; 1 GB tmpfs hash < 10 ms in CI.
- [ ] `lukechampine.com/blake3` added to `pipeline/go.mod`; `go.sum` updated.

**Database**
- [ ] `shared/db/migrations/0003_videos_content_hash.sql` applies cleanly on a fresh `0001_init_libraries_and_videos.sql` schema.
- [ ] `goose down` reverts cleanly; tested in CI.
- [ ] `videos_library_content_hash_key` exists; the old global `videos_content_hash_key` is gone.
- [ ] `videos_content_hash_format_chk` rejects non-64-char and uppercase hex inputs (verified by SQL test).
- [ ] `videos_additional_paths_gin_idx` is used by the planner for the `metadata->'additional_paths' ? '/some/path'` lookup (`EXPLAIN` output checked into a fixture).
- [ ] `shared/db/queries/identity.sql` generates `InsertOrLinkVideo`, `AppendAdditionalPath`, `UpdatePathOnRename`, `GetVideoByLibraryAndHash` via sqlc.

**Behaviour (story acceptance criteria)**
- [ ] AC #1: Hash output exactly matches `hex(BLAKE3(head || tail || size_le_u64))` for an arbitrary fixture, verified by golden vector.
- [ ] AC #2: Two identical files in the same library → one row, INFO log `duplicate_content_hash`, second path in `additional_paths`.
- [ ] AC #3: Two identical files in different libraries → two rows, both with the same `content_hash`, distinct `library_id`.
- [ ] AC #4: 30 GB sparse fixture hashed reads ≤ 8 MiB total (asserted by `readAtCounter`).
- [ ] All test cases listed in the story (`test_hash_*`) have a corresponding Go test that passes.

**Performance**
- [ ] `BenchmarkHashFile_1GB` reports ≥ 800 MiB/s effective throughput on the standard CI runner (NVMe).
- [ ] Scanner worker pool sizing (`recommendedWorkers`) returns 1 on rotational, `min(NumCPU, 8)` otherwise.
- [ ] Memory per worker measured with `runtime.ReadMemStats` ≤ 8 MiB steady-state.

**Docs**
- [ ] `specs/epics/01-scanner/README.md` ticks story 1.2.
- [ ] `pipeline/internal/identity/doc.go` documents the algorithm and its known head+tail-collision limitation, with a link back to this plan.
- [ ] Cross-language parity test (Python `blake3` vs Go `blake3`) on a shared fixture is in `pipeline/tests/cross_lang_hash_test.py` and runs in CI.

**Operational**
- [ ] INFO log line `duplicate_content_hash library_id=… hash=… new_path=… existing_path=…` is structured (matches the JSON shape used elsewhere in `pipeline/`).
- [ ] No new metric is required for v1; the existing `scanner_files_processed_total{outcome="inserted|linked|skipped"}` (Story 1.1) gets the new `linked` label value.
