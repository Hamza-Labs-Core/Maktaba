# Story 9.5 — Ignore rules and extension filtering

Per §3.1: hidden files, partial downloads, sidecar dirs, and unsupported
extensions are skipped. User-configurable `ignore_globs` extends this.

**AC-1 — Built-in ignores.**
- **Given** any scan,
- **When** a path matches `**/.*`, `**/*.part`, `**/*.crdownload`,
  `**/.maktaba/**`, `**/.DS_Store`, `**/Thumbs.db`,
- **Then** it is skipped silently.

**AC-2 — Supported extensions.**
- **Given** a file whose extension is in `supported_video_exts` (default:
  mp4, mkv, mov, m4v, avi, wmv, flv, webm, mpeg, mpg, ts, m2ts, mts,
  vob, ogv, 3gp),
- **When** scanned,
- **Then** it is enqueued for probe.
- **Given** an extension not in the set, **Then** it's skipped.

**AC-3 — User globs.**
- **Given** a library with `ignore_globs: ["**/raw/**", "**/*.tmp.mp4"]`,
- **When** a matching file is encountered,
- **Then** it is skipped. `ignore_globs` is also applied to the watcher
  (live events are filtered before debounce).

**Test cases:**
- Unit: each built-in pattern with a positive + negative case.
- Unit: case-insensitive match on Windows; case-sensitive on Linux/macOS.
- Integration: changing `ignore_globs` after files are already indexed
  does not retroactively remove them — the user must purge.

**Edge cases:**
- `.maktaba/` is the sidecar root and must be ignored even at deep
  nesting (`/library/sub/.maktaba/...`). The pattern uses `**/.maktaba/**`,
  not `.maktaba/**`.
- A user adds `**/*` to `ignore_globs` — every scan becomes a no-op;
  documented as a way to "freeze" a library without deleting it.
- An unknown video extension that ffprobe could actually decode — the
  user must add it to `supported_video_exts` in app settings; no
  auto-detection.
