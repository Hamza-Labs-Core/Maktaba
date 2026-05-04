# Plan 9.5 — Ignore rules and supported-extension filtering — implementation

> Implementation plan for [story-09-05-ignore-rules.md](story-09-05-ignore-rules.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: produces the `IgnoreMatcher` consumed by
> the watcher in [Plan 9.2](plan-09-02-filesystem-watcher.md) (event-time
> filter, before debounce) and the sweep in
> [Plan 9.3](plan-09-03-periodic-sweep.md) (walk-time prune of dirnames
> + file filter); reads `ignore_globs` from the resolved settings owned
> by [Plan 9.1](plan-09-01-library-config-schema.md); does **not**
> retroactively purge already-indexed videos (story AC) — the user must
> explicitly delete them. The matcher is also reusable from the API
> service (Go) for the dry-run "what would this glob exclude?" endpoint
> in Story 9.6, but the canonical implementation is in Python; the Go
> side calls the same regex via a shared spec doc.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Use `pathspec.PathSpec` (gitignore syntax)** as the underlying glob engine, not `fnmatch` and not bare regex. The matcher accepts gitignore-flavor patterns, including `**`, `!negation`, leading `/` for root-anchor, and trailing `/` for directory-only match. | Story AC-1 ("`**/.maktaba/**`, …"); industry-standard semantics. | `fnmatch` does not handle `**` correctly (it treats `**` like `*` — matches a single segment, not zero-or-more segments). Hand-rolled regex is brittle. `pathspec` is a small, well-tested dependency that matches what users already know. |
| D2 | **The matcher exposes one method, `matches(path: str) -> bool`**, where True means "skip this path". Both files and directories pass through the same method; directory matches use a trailing-slash convention internally. The sweep prunes `dirnames` by synthesizing `dir + '/'` before calling `matches`. | Plan 9.2 `_Handler.on_*` and Plan 9.3 `iter_files` both call the same predicate. | A single predicate keeps watcher and sweep aligned; if they used different matchers, an ignored-on-watcher / not-ignored-on-sweep gap would silently break user expectations. |
| D3 | **Built-in patterns are baked into `BUILTIN_IGNORES`** and always applied; user `ignore_globs` are appended *after* and can negate built-ins via `!**/.DS_Store` (gitignore semantics). Built-ins: `**/.*`, `**/*.part`, `**/*.crdownload`, `**/.maktaba/**`, `**/.DS_Store`, `**/Thumbs.db`. | Story AC-1 verbatim. | A user might want to negate (e.g., a library where `.dotfiles/` is intentional). Gitignore-style negation is the cleanest UX; without it, we'd need two settings keys. |
| D4 | **Supported-extension filter is a separate stage** (`SupportedExtensionFilter`), not folded into the ignore matcher. The matcher answers "should I skip", the extension filter answers "is this a video file". Both must pass for a path to be processed. | Story AC-2 separates the two concerns; the test cases differentiate "ignored" from "unsupported". | Folding them confuses the audit story: if a file is skipped, was it ignored or unsupported? Separate filters → separate metrics + clearer logs. |
| D5 | **Case sensitivity matches the OS.** Linux + macOS are case-sensitive; Windows is case-insensitive. We detect via `sys.platform` once at module import and configure the underlying matcher accordingly. | Story test case: "case-insensitive match on Windows; case-sensitive on Linux/macOS." | Matches the user's filesystem's actual semantics. APFS on macOS is case-insensitive *by default* but the *kernel* exposes case-sensitive APIs to `os.stat`; we follow the kernel-API behavior, which matches `pathspec` defaults. |
| D6 | **Settings live-reload via NOTIFY (Plan 9.1).** The watcher / sweep instances hold a *reference* to an `IgnoreMatcher`; on `library.settings_changed` the supervisor builds a new matcher and assigns it (atomic Python ref swap). No restart. | Plan 9.1 D6 + cross-cut. | Reusing the matcher object across reloads would require mutating its internal `PathSpec`, which `pathspec` doesn't support cleanly. Building a new instance is O(N) in the pattern count (cheap) and lock-free. |
| D7 | **`ignore_globs` is NOT retroactive.** Adding a glob does not purge already-indexed videos that match. The story AC says exactly this; we surface a warning in the PATCH response when the new globs would have matched ≥ 1 existing video, and link to the manual purge endpoint (Story 9.15). | Story test case: "changing `ignore_globs` after files are already indexed does not retroactively remove them — the user must purge." | Auto-purge is destructive and could surprise the user. The warning makes the implication visible; the user can act. |
| D8 | **Supported extensions are app-level, not per-library**, in v1. Stored in `app_settings.supported_video_exts` (TEXT[]); defaults to the 16-extension set in the story. A future Story 9.X may add per-library overrides. | Story AC-2 + edge case "user must add it to `supported_video_exts` in app settings". | Per-library extension overrides are useful but introduce a new validation surface; the deferred path is documented. |
| D9 | **All paths are normalized before matching.** We call `os.path.normpath` and convert backslashes to forward slashes before passing to `pathspec`. This ensures Windows callers and POSIX callers see the same matching semantics for the same logical path. | Cross-platform consistency. | Without normalization, `**/raw/**` on Windows would not match `C:\data\raw\v.mp4` because the matcher sees backslashes. |
| D10 | **`IgnoreMatcher.from_settings(settings, *, app_settings=None)`** is the canonical builder. It pulls `ignore_globs` from settings, prepends `BUILTIN_IGNORES`, and returns an immutable matcher. The `app_settings` argument is accepted but only `supported_video_exts` is read from it (passed through to a sibling `SupportedExtensionFilter`). | Construction simplicity. | One builder, one settings shape — the watcher and sweep both call the builder identically. |

If D1 is rejected (use `fnmatch`): the `**/.maktaba/**` pattern silently
fails to match `/data/lib/sub/.maktaba/inner/x` and the user gets a
sidecar dir indexed by mistake. This is a correctness bug that would be
hard to debug.

If D5 is rejected (always case-sensitive): Windows users get spurious
mismatches between watcher events (which preserve OS case) and sweep
walks (which canonicalize to whatever path we typed in settings). The
default test suite already covers this in the Windows CI matrix.

---

## 1. Architecture diagram — matcher consumers

```
   PATCH /api/libraries/{id}                   PATCH validates ignore_globs
            │                                  using a shared regex spec
            ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ libraries.settings.ignore_globs (JSONB array of strings)    │
   │ libraries.settings_version (BIGINT)         (Plan 9.1)      │
   └────────────────────────────┬────────────────────────────────┘
                                │ LISTEN library.settings_changed
                                ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ pipeline IgnoreMatcher.from_settings(settings)              │
   │   patterns = BUILTIN_IGNORES + settings.ignore_globs        │
   │   spec = pathspec.PathSpec.from_lines('gitwildmatch',       │
   │             patterns)                                       │
   │   return IgnoreMatcher(spec)                                │
   └─────────────────────────────────────────────────────────────┘
                                │
            ┌───────────────────┼───────────────────┐
            ▼                   ▼                   ▼
       LibraryWatcher      SweepRunner        SupportedExtensionFilter
       (Plan 9.2)          (Plan 9.3)         (this plan)
            │                   │                   │
            ▼                   ▼                   ▼
       _Handler.on_* :   iter_files :           ext lookup against
       if matcher        for dir in dirnames:   supported_video_exts
       .matches(path):   if matcher.matches      (per app_settings)
       drop event        (dir+'/'): prune
                         for f in filenames:
                           if matcher.matches(f) or
                              not ext_filter.allows(f): skip


   ┌─────────────────────────────────────────────────────────────┐
   │ apps/api/internal/library/ignore_validate.go                │
   │                                                             │
   │   Validate the user's globs at PATCH time:                  │
   │     - non-empty strings                                     │
   │     - parses cleanly under gitwildmatch                     │
   │     - pre-flight count of matched existing videos (D7)      │
   │       SELECT COUNT(*) FROM videos WHERE library_id=$1       │
   │         AND path ~~ ANY($2::text[])                         │
   │     -> returns warnings array if count > 0                  │
   └─────────────────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 SQL — app-level supported extensions (D8)

```sql
-- shared/db/migrations/00XX_app_settings_supported_exts.sql
BEGIN;

-- app_settings is a singleton row; if it doesn't exist, create the table.
CREATE TABLE IF NOT EXISTS app_settings (
    id                    SMALLINT PRIMARY KEY DEFAULT 1
                                   CHECK (id = 1),    -- singleton
    supported_video_exts  TEXT[] NOT NULL DEFAULT ARRAY[
        'mp4','mkv','mov','m4v','avi','wmv','flv','webm','mpeg','mpg',
        'ts','m2ts','mts','vob','ogv','3gp']::text[],
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the singleton row.
INSERT INTO app_settings (id) VALUES (1)
ON CONFLICT (id) DO NOTHING;

COMMIT;
```

### 2.2 Python package layout

```
pipeline/src/maktaba_pipeline/ignore/
├── __init__.py                  # public surface: IgnoreMatcher, SupportedExtensionFilter
├── matcher.py                   # IgnoreMatcher (D1, D2, D3, D5, D9)
├── ext_filter.py                # SupportedExtensionFilter (D4, D8)
├── builtin.py                   # BUILTIN_IGNORES, default supported_video_exts
├── factory.py                   # from_settings builder (D10)
├── errors.py                    # InvalidPattern
└── tests/
    ├── conftest.py
    ├── test_builtin_patterns.py
    ├── test_user_globs.py
    ├── test_negation.py
    ├── test_double_star.py
    ├── test_supported_exts.py
    ├── test_case_sensitivity.py
    ├── test_factory_from_settings.py
    ├── test_normpath_cross_platform.py
    └── test_freeze_pattern.py
apps/api/internal/library/
├── ignore_validate.go           # PATCH-side validation (preflight count)
└── ignore_validate_test.go
```

### 2.3 `builtin.py` — built-in patterns + ext defaults (D3, D8)

```python
"""Hard-coded built-in ignore patterns and the default video-extension list.

These are the floor: user `ignore_globs` are appended on top and may
negate any built-in via `!pattern` (gitignore semantics)."""
from __future__ import annotations

BUILTIN_IGNORES: tuple[str, ...] = (
    "**/.*",              # any hidden file or directory at any depth
    "**/*.part",          # browser/Wget partial downloads
    "**/*.crdownload",    # Chrome partial downloads
    "**/.maktaba/**",     # our sidecar dir, at any depth
    "**/.DS_Store",       # macOS finder metadata
    "**/Thumbs.db",       # Windows thumbnail cache
)

DEFAULT_VIDEO_EXTS: frozenset[str] = frozenset({
    "mp4", "mkv", "mov", "m4v", "avi", "wmv", "flv", "webm", "mpeg",
    "mpg", "ts", "m2ts", "mts", "vob", "ogv", "3gp",
})
```

### 2.4 `matcher.py` — IgnoreMatcher (D1, D2, D5, D9)

```python
"""IgnoreMatcher: gitwildmatch-style path filter, hot-swappable per library."""
from __future__ import annotations
import os, sys
from typing import Iterable

import pathspec

from .errors import InvalidPattern


class IgnoreMatcher:
    """Immutable; build one with from_settings (factory.py) per settings_version."""

    __slots__ = ("_spec", "_case_insensitive", "_patterns")

    def __init__(self, *, patterns: Iterable[str],
                 case_insensitive: bool | None = None):
        if case_insensitive is None:
            case_insensitive = sys.platform == "win32"           # D5
        self._case_insensitive = case_insensitive
        self._patterns = tuple(patterns)
        try:
            self._spec = pathspec.PathSpec.from_lines(
                "gitwildmatch",
                (p.lower() if case_insensitive else p
                 for p in self._patterns),
            )
        except Exception as e:
            raise InvalidPattern(str(e)) from e

    def matches(self, path: str) -> bool:
        """Return True iff `path` matches at least one (non-negated) pattern."""
        norm = self._normalize(path)
        return self._spec.match_file(norm)

    def _normalize(self, path: str) -> str:
        # D9: slash-normalize; lowercase if case-insensitive.
        norm = path.replace("\\", "/")
        norm = os.path.normpath(norm).replace("\\", "/")
        if self._case_insensitive:
            norm = norm.lower()
        return norm

    @property
    def patterns(self) -> tuple[str, ...]:
        return self._patterns
```

### 2.5 `ext_filter.py` — SupportedExtensionFilter (D4, D8)

```python
"""SupportedExtensionFilter: independent of IgnoreMatcher (D4)."""
from __future__ import annotations
import os
from typing import Iterable

from .builtin import DEFAULT_VIDEO_EXTS


class SupportedExtensionFilter:
    __slots__ = ("_exts",)

    def __init__(self, exts: Iterable[str] | None = None):
        if exts is None:
            self._exts = DEFAULT_VIDEO_EXTS
        else:
            self._exts = frozenset(e.lower().lstrip(".") for e in exts if e)

    def allows(self, path: str) -> bool:
        ext = os.path.splitext(path)[1].lower().lstrip(".")
        return ext in self._exts

    @property
    def extensions(self) -> frozenset[str]:
        return self._exts
```

### 2.6 `factory.py` — `from_settings` builder (D10)

```python
"""Build an IgnoreMatcher from a resolved library-settings dict."""
from __future__ import annotations
from typing import Iterable

from .builtin import BUILTIN_IGNORES
from .matcher import IgnoreMatcher
from .ext_filter import SupportedExtensionFilter


def build_matcher(settings: dict | None,
                  *, case_insensitive: bool | None = None) -> IgnoreMatcher:
    user = ((settings or {}).get("ignore_globs") or [])
    if not isinstance(user, list):
        user = []
    cleaned = [p for p in user if isinstance(p, str) and p.strip()]
    patterns: list[str] = list(BUILTIN_IGNORES) + cleaned
    return IgnoreMatcher(patterns=patterns, case_insensitive=case_insensitive)


def build_ext_filter(app_settings: dict | None) -> SupportedExtensionFilter:
    if app_settings and app_settings.get("supported_video_exts"):
        return SupportedExtensionFilter(app_settings["supported_video_exts"])
    return SupportedExtensionFilter()


# Convenience for callers (used by Plan 9.2 / Plan 9.3 supervisors).
def from_settings(settings: dict | None,
                  app_settings: dict | None = None,
                  *, case_insensitive: bool | None = None
                  ) -> tuple[IgnoreMatcher, SupportedExtensionFilter]:
    return (build_matcher(settings, case_insensitive=case_insensitive),
            build_ext_filter(app_settings))
```

We also expose `IgnoreMatcher.from_settings = staticmethod(build_matcher)`
in `matcher.py` so the watcher's `swap_matcher` call site reads naturally:

```python
# matcher.py, end of file
from .factory import build_matcher as _build
IgnoreMatcher.from_settings = staticmethod(_build)            # type: ignore[attr-defined]
```

### 2.7 `errors.py`

```python
class InvalidPattern(Exception):
    """ignore_globs contained an unparseable pattern."""
```

### 2.8 Go-side validation (PATCH preflight, D7)

```go
// apps/api/internal/library/ignore_validate.go
package library

import (
    "context"
    "fmt"
    "regexp"

    "github.com/jackc/pgx/v5/pgxpool"
)

// ValidateIgnoreGlobs is called from the settings validator (Plan 9.1)
// when the patch touches ignore_globs. It (a) sanity-checks the patterns
// and (b) returns a warning if any matches an already-indexed video.
//
// We do NOT run the full gitwildmatch engine in Go — we delegate to a
// shared regex spec; the canonical engine is the Python pathspec library.
// For preflight, we use Postgres LIKE with a coarse translation; the user
// gets a "warning, may match N videos" message but the actual filtering
// at runtime still uses pathspec on the Pipeline side.
func ValidateIgnoreGlobs(
    ctx context.Context, db *pgxpool.Pool,
    libraryID string, globs []string,
) ([]Warning, error) {
    if len(globs) == 0 {
        return nil, nil
    }
    var warnings []Warning
    var asLike []string
    for i, g := range globs {
        if g == "" {
            return nil, fmt.Errorf("ignore_globs[%d] is empty", i)
        }
        if !validGlobShape(g) {
            return nil, fmt.Errorf("ignore_globs[%d]: %q has bad pattern", i, g)
        }
        asLike = append(asLike, globToLike(g))
    }
    var n int64
    err := db.QueryRow(ctx, `
        SELECT COUNT(*) FROM videos
         WHERE library_id = $1 AND path ~~ ANY($2::text[])
    `, libraryID, asLike).Scan(&n)
    if err != nil {
        return nil, err
    }
    if n > 0 {
        warnings = append(warnings, Warning{
            Path:    "/ignore_globs",
            Message: fmt.Sprintf("matches %d already-indexed videos; "+
                "they will NOT be auto-purged. Use Story 9.15 to delete.", n),
        })
    }
    return warnings, nil
}

var globShape = regexp.MustCompile(`^[a-zA-Z0-9._/!*?\-\[\]]+$`)

func validGlobShape(g string) bool {
    return globShape.MatchString(g) && len(g) <= 256
}

// globToLike converts a coarse subset of gitwildmatch to SQL LIKE for
// the preflight count. It deliberately under-approximates: a "yes" from
// LIKE means "definitely matches"; the runtime pathspec is the source
// of truth.
func globToLike(g string) string {
    out := make([]byte, 0, len(g)+2)
    for i := 0; i < len(g); i++ {
        c := g[i]
        switch c {
        case '*':
            // gitwildmatch '*' = any segment chars; LIKE '%' = any chars
            out = append(out, '%')
        case '?':
            out = append(out, '_')
        case '%', '\\', '_':
            out = append(out, '\\', c)
        default:
            out = append(out, c)
        }
    }
    return string(out)
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/00XX_app_settings_supported_exts.sql` | `app_settings` table + singleton row | `test_app_settings_singleton` |
| 2 | `pipeline/src/maktaba_pipeline/ignore/__init__.py` | re-exports | n/a |
| 3 | `pipeline/src/maktaba_pipeline/ignore/errors.py` | `InvalidPattern` | n/a |
| 4 | `pipeline/src/maktaba_pipeline/ignore/builtin.py` | `BUILTIN_IGNORES`, `DEFAULT_VIDEO_EXTS` | n/a |
| 5 | `pipeline/src/maktaba_pipeline/ignore/matcher.py` | `IgnoreMatcher` | `test_builtin_patterns`, `test_double_star`, `test_negation`, `test_normpath_cross_platform`, `test_case_sensitivity` |
| 6 | `pipeline/src/maktaba_pipeline/ignore/ext_filter.py` | `SupportedExtensionFilter` | `test_supported_exts` |
| 7 | `pipeline/src/maktaba_pipeline/ignore/factory.py` | `build_matcher`, `build_ext_filter`, `from_settings` | `test_factory_from_settings` |
| 8 | `apps/api/internal/library/ignore_validate.go` | `ValidateIgnoreGlobs`, `globToLike`, `validGlobShape` | `TestValidateIgnoreGlobs*` |
| 9 | `apps/api/internal/library/ignore_validate_test.go` | (tests) | (the tests themselves) |
| 10 | `pipeline/pyproject.toml` (extend) | add `pathspec>=0.12.0` | dependency lock |

---

## 4. Test cases

### 4.1 `test_builtin_patterns_match_dotfiles_and_sidecars` — AC-1

```python
@pytest.mark.parametrize("path", [
    "/data/lib/.hidden",
    "/data/lib/sub/.hidden",
    "/data/lib/file.part",
    "/data/lib/sub/dl.crdownload",
    "/data/lib/.maktaba/x",
    "/data/lib/sub/.maktaba/inner/y",
    "/data/lib/.DS_Store",
    "/data/lib/Thumbs.db",
])
def test_builtin_pattern_matches(path):
    m = IgnoreMatcher.from_settings({})
    assert m.matches(path) is True


@pytest.mark.parametrize("path", [
    "/data/lib/video.mp4",
    "/data/lib/sub/clip.mkv",
    "/data/lib/maktaba_inside_name.mp4",   # not the .maktaba dir
])
def test_builtin_pattern_does_not_match_normal_paths(path):
    m = IgnoreMatcher.from_settings({})
    assert m.matches(path) is False
```

### 4.2 `test_double_star_matches_at_any_depth` — story edge case (E1)

```python
def test_maktaba_pattern_matches_at_any_depth():
    m = IgnoreMatcher.from_settings({})
    assert m.matches("/data/lib/.maktaba/cache/x")
    assert m.matches("/data/lib/sub/sub2/.maktaba/cache/y")
    # The story explicitly notes: pattern is **/.maktaba/**, NOT .maktaba/**.
```

### 4.3 `test_user_globs_apply` — AC-3

```python
def test_user_globs_extend_builtins():
    m = IgnoreMatcher.from_settings(
        {"ignore_globs": ["**/raw/**", "**/*.tmp.mp4"]})
    assert m.matches("/data/lib/raw/v.mp4")
    assert m.matches("/data/lib/raw/sub/v.mp4")
    assert m.matches("/data/lib/work.tmp.mp4")
    assert not m.matches("/data/lib/normal.mp4")


def test_user_globs_can_negate_builtins():
    m = IgnoreMatcher.from_settings(
        {"ignore_globs": ["!**/.dotfile-i-want.mp4"]})
    # Built-in **/.* normally matches; the user's negation un-ignores this one.
    assert m.matches("/data/lib/.other") is True
    assert m.matches("/data/lib/.dotfile-i-want.mp4") is False
```

### 4.4 `test_freeze_library_with_starstar_glob` — story edge case

```python
def test_user_glob_starstar_freezes_library():
    """User adds **/* — every scan becomes a no-op."""
    m = IgnoreMatcher.from_settings({"ignore_globs": ["**/*"]})
    assert m.matches("/data/lib/v.mp4")
    assert m.matches("/data/lib/sub/x.mkv")
```

### 4.5 `test_case_sensitivity_per_platform` — AC-2 / story test case

```python
def test_case_sensitive_match_on_linux(monkeypatch):
    monkeypatch.setattr("sys.platform", "linux")
    m = IgnoreMatcher(patterns=["**/RAW/**"])
    assert m.matches("/data/lib/RAW/v.mp4")
    assert not m.matches("/data/lib/raw/v.mp4")


def test_case_insensitive_on_windows(monkeypatch):
    monkeypatch.setattr("sys.platform", "win32")
    m = IgnoreMatcher(patterns=["**/RAW/**"])
    assert m.matches("/data/lib/RAW/v.mp4")
    assert m.matches("/data/lib/raw/v.mp4")
```

### 4.6 `test_normpath_handles_backslashes` — D9

```python
def test_backslash_paths_are_normalized():
    m = IgnoreMatcher(patterns=["**/raw/**"])
    assert m.matches(r"C:\data\raw\v.mp4")
    assert m.matches("C:/data/raw/v.mp4")
    assert m.matches("/data/raw/v.mp4")
```

### 4.7 `test_supported_exts` — AC-2

```python
def test_default_extensions():
    f = SupportedExtensionFilter()
    assert f.allows("v.mp4")
    assert f.allows("V.MKV")             # case-insensitive ext lookup
    assert not f.allows("notes.txt")
    assert not f.allows("v.flac")        # audio not video


def test_app_setting_extensions_override():
    f = SupportedExtensionFilter(["mp4", "mkv"])
    assert f.allows("v.mp4")
    assert not f.allows("v.mov")         # mov not in custom list
```

### 4.8 `test_factory_from_settings` — D10

```python
def test_from_settings_combines_builtins_and_user():
    m, f = from_settings(
        settings={"ignore_globs": ["**/raw/**"]},
        app_settings={"supported_video_exts": ["mp4"]})
    assert m.matches("/data/lib/.hidden")             # built-in
    assert m.matches("/data/lib/raw/v.mp4")            # user
    assert f.allows("v.mp4")
    assert not f.allows("v.mkv")


def test_from_settings_defaults_when_keys_missing():
    m, f = from_settings(settings={}, app_settings={})
    assert m.matches("/data/lib/.DS_Store")
    assert f.allows("v.webm")
```

### 4.9 `test_no_retroactive_purge` — AC of D7

```python
async def test_adding_glob_does_not_delete_existing_videos(db, http_client):
    """PATCHing ignore_globs does not modify videos rows."""
    seed_videos(db, library_id=LIB_ID, paths=["/data/lib/raw/v.mp4"])
    n_before = await db.fetchval(
        "SELECT COUNT(*) FROM videos WHERE library_id=$1", LIB_ID)
    resp = await http_client.patch(f"/api/libraries/{LIB_ID}",
        json={"ignore_globs": ["**/raw/**"]})
    assert resp.status_code == 200
    body = resp.json()
    # Warning surfaces the count.
    assert any("matches 1 already-indexed" in w["message"]
               for w in body.get("warnings", []))
    n_after = await db.fetchval(
        "SELECT COUNT(*) FROM videos WHERE library_id=$1", LIB_ID)
    assert n_after == n_before              # not retroactive
```

### 4.10 `TestValidateIgnoreGlobs` — Go side

```go
func TestValidateIgnoreGlobsRejectsEmpty(t *testing.T) {
    db := freshDB(t)
    _, err := ValidateIgnoreGlobs(ctx, db, libID, []string{""})
    if err == nil {
        t.Fatalf("expected error for empty glob")
    }
}

func TestValidateIgnoreGlobsWarnsOnPreflightHit(t *testing.T) {
    db := freshDB(t)
    seedVideo(t, db, libID, "/data/lib/raw/v.mp4")
    warnings, err := ValidateIgnoreGlobs(ctx, db, libID, []string{"**/raw/**"})
    if err != nil {
        t.Fatal(err)
    }
    if len(warnings) != 1 {
        t.Fatalf("expected one warning, got %d", len(warnings))
    }
    if !strings.Contains(warnings[0].Message, "matches") {
        t.Fatalf("unexpected warning: %+v", warnings[0])
    }
}

func TestGlobToLikeApproximates(t *testing.T) {
    cases := []struct{ in, want string }{
        {"**/raw/**", "%/raw/%"},        // % is the LIKE any-chars
        {"*.tmp.mp4", "%.tmp.mp4"},
        {"a_b", "a\\_b"},                 // _ is a LIKE wildcard, escape
    }
    for _, c := range cases {
        if got := globToLike(c.in); got != c.want {
            t.Errorf("globToLike(%q) = %q; want %q", c.in, got, c.want)
        }
    }
}
```

### 4.11 `test_app_settings_singleton`

```python
async def test_app_settings_table_is_singleton(empty_db):
    await apply_migration(empty_db, "00XX_app_settings_supported_exts.sql")
    n = await empty_db.fetchval("SELECT COUNT(*) FROM app_settings")
    assert n == 1
    with pytest.raises(asyncpg.exceptions.CheckViolationError):
        await empty_db.execute("INSERT INTO app_settings (id) VALUES (2)")
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handling |
|-----|-----------|----------|
| E1  | **`.maktaba/` at deep nesting.** Pattern `**/.maktaba/**` matches at any depth (D2 via gitwildmatch). | `test_maktaba_pattern_matches_at_any_depth`. |
| E2  | **User `**/*` glob** — every scan becomes a no-op; documented as "freeze the library." | `test_user_glob_starstar_freezes_library`. |
| E3  | **Unsupported extension that ffprobe could decode** (e.g., `.divx`). User must add it to `app_settings.supported_video_exts`; no auto-detection. UI surfaces the setting in Story 7.X (Epic 7); this plan only owns the storage. | D8 documented. |
| E4  | **`ignore_globs` not retroactive.** PATCH validation runs a preflight count and warns. The user must use Story 9.15 to purge. | D7 + `test_adding_glob_does_not_delete_existing_videos`. |
| E5  | **Live-reload of patterns mid-sweep.** The sweep holds a reference to the matcher at start; a NOTIFY during the sweep does not affect the in-flight walk (the matcher is immutable, and the sweep pinned its reference). The next sweep tick uses the new matcher. The watcher's reference *is* swapped on NOTIFY (Plan 9.2 D8) so subsequent events use the new matcher; in-flight debounced entries are graduated under the new matcher because graduation does not re-check ignores (only the on-event filter does). This is the documented "rules can change between sweep ticks" behavior. | Documented; in-flight semantics are intentional. |
| E6  | **A negation pattern alone** (`!**/keep.mp4`). Without a corresponding ignore, negation is a no-op. We accept it — gitignore semantics specify this. | `test_user_globs_can_negate_builtins`. |
| E7  | **Whitespace-only or empty pattern in `ignore_globs`.** `build_matcher` filters them out (`if isinstance(p, str) and p.strip()`). | `factory.build_matcher`. |
| E8  | **Pattern with `..` segments.** `os.path.normpath` resolves `..` before matching, so `**/raw/..` becomes `**` — likely unintended. We document this in the API reference; users should not use `..` in globs. | Documented behavior. |
| E9  | **Unicode in pattern or path.** `pathspec` and `os.path.normpath` handle UTF-8; the Pipeline runs with `LANG=C.UTF-8`. | Inherits from runtime. |
| E10 | **Non-ASCII filename on Windows where case-insensitive lowercasing is locale-dependent.** Python's `str.lower()` uses Unicode case-folding; this is correct for Latin scripts and acceptable for non-Latin (Cyrillic, Greek) where case has well-defined semantics. Arabic, CJK, and similar scripts have no case; lowercasing is a no-op. Edge case is documented; no special handling. | Documented. |
| E11 | **Many user globs (50+).** `pathspec` builds an internal regex per pattern; matching is O(N_patterns) per path. With 50 patterns and 100k files in a sweep, that's 5M regex matches — sub-second on a modern CPU. Documented operator soft cap of 100 patterns per library. | Documented. |
| E12 | **Pattern collision with built-ins** (e.g., user adds `**/.foo` which overlaps `**/.*`). gitwildmatch deduplication is N/A; both fire and the result is "match" — same as if either alone fired. No correctness impact. | Documented. |

---

## 6. Acceptance checklist

- [ ] **A1** Built-in patterns silently skip: `**/.*`, `**/*.part`, `**/*.crdownload`, `**/.maktaba/**`, `**/.DS_Store`, `**/Thumbs.db`. (`test_builtin_pattern_matches`, `test_builtin_pattern_does_not_match_normal_paths`)
- [ ] **A2** Supported extensions enqueue for probe; others skip. Default set = the 16 extensions in story-09-05 AC-2. (`test_default_extensions`, `test_app_setting_extensions_override`)
- [ ] **A3** User `ignore_globs` extend (and may negate) built-ins via gitwildmatch syntax. Both watcher (Plan 9.2) and sweep (Plan 9.3) consume the same `IgnoreMatcher` instance per library. (`test_user_globs_extend_builtins`, `test_user_globs_can_negate_builtins`)
- [ ] **A4** Per-platform case sensitivity: case-sensitive on Linux/macOS, case-insensitive on Windows. (`test_case_sensitive_match_on_linux`, `test_case_insensitive_on_windows`)
- [ ] **A5** Adding `ignore_globs` after files are indexed does NOT retroactively purge them; the PATCH response carries a warning when the new glob would have matched ≥ 1 existing video. (`test_adding_glob_does_not_delete_existing_videos`, `TestValidateIgnoreGlobsWarnsOnPreflightHit`)
- [ ] **A6** `.maktaba/` is matched at any depth (`**/.maktaba/**` not `.maktaba/**`). (`test_maktaba_pattern_matches_at_any_depth`)
- [ ] **A7** `**/*` as a user glob freezes the library (every scan becomes a no-op). (`test_user_glob_starstar_freezes_library`)
- [ ] **A8** `IgnoreMatcher.from_settings(settings)` and `SupportedExtensionFilter` together provide the only filter surface; neither the watcher nor the sweep duplicates pattern logic locally. (Static lint: `watcher/` and `sweep/` import only from `ignore/`.)
- [ ] **A9** Live-reload via `library.settings_changed` swaps the matcher in place without restarting the Observer (Plan 9.2 cross). (Cross-tested in Plan 9.2 `test_supervisor_swap_matcher_on_notify`.)
- [ ] **A10** Path normalization makes Windows backslashes and POSIX slashes match the same way. (`test_backslash_paths_are_normalized`)
- [ ] **A11** App-level supported extensions stored in `app_settings` singleton row. (`test_app_settings_table_is_singleton`)

---

## 7. Performance budget

| Operation | Cost | Notes |
|-----------|------|-------|
| `IgnoreMatcher` construction | < 1 ms for ~10 patterns | `pathspec.PathSpec.from_lines` builds N regexes. |
| `IgnoreMatcher.matches(path)` | ~5 µs typical, ~50 µs worst case | O(N_patterns) regex matches; early exit on first match. |
| `SupportedExtensionFilter.allows(path)` | ~1 µs | One `splitext` + frozenset lookup. |
| Watcher event filter (per event, Plan 9.2) | ~6 µs | One `matches` + one `allows`. |
| Sweep walk filter (per file, Plan 9.3) | ~6 µs | Same. 100k files × 6 µs = 600 ms — fits the 30 s sweep budget. |
| PATCH preflight (`ValidateIgnoreGlobs`) | ~5 ms | One COUNT(*) query with `path ~~ ANY($)` — uses the existing `videos_path` index. |
| Live-reload (matcher swap) | < 5 ms end-to-end | Build new matcher + atomic ref swap; no in-flight events lost. |

The matcher hot path is regex-bound. With the default 6 built-in patterns
+ a handful of user globs, per-call cost is well under 10 µs and never
shows up in profiling.
