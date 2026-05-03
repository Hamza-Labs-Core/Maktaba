# Story 9.16 — Multi-root and overlap detection

A library can have N roots (§5). Roots may not overlap with another
library's roots; within a library, multiple roots are independent.

**AC-1 — N roots in one library.**
- **Given** a library with `roots: ["/mnt/a", "/mnt/b"]`,
- **When** scanned,
- **Then** both trees are walked; results merge into the library's
  catalog. No per-root subdivision in the API.

**AC-2 — Overlap rejection at create/update.**
- **Given** library A with `roots: ["/mnt/media"]`,
- **When** library B is created with `roots: ["/mnt/media/sub"]`,
- **Then** 422 `type: library-roots-overlap` (Epic 7 Story 7.3 AC-2
  edge case).

**AC-3 — Overlap detection rule.**
- Two paths overlap if one is a prefix of the other after path
  canonicalization (resolve symlinks, trailing slashes, `..`).

**AC-4 — Periodic remount-overlap warning.**
- **Given** a host where new mounts are added at runtime that, after
  resolution, make two previously non-overlapping roots resolve to the
  same physical path,
- **When** the periodic sweep (Story 9.3) runs,
- **Then** the sweep emits a `WARN` log
  `library-roots-runtime-overlap` and writes an audit row; further
  sweep work continues but the operator is advised to fix the mount
  layout.

**Test cases:**
- Unit: canonicalization fixtures cover symlink, `..`, trailing slash.
- Integration: `["/a", "/a/b"]` in one library is rejected (a single
  library may not nest its own roots either).
- Integration: simulate a runtime symlink change that creates overlap
  → next sweep emits the warning and audit row.

**Edge cases:**
- A symlink that, after resolution, makes two non-overlapping declared
  roots resolve to the same physical path — caught by canonicalization
  at create time and by the AC-4 sweep check at runtime.
