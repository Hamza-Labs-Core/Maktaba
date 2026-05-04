# Implementation Plan — Story 9.5 Ignore Rules and Extension Filtering

> Companion to [story-09-05-ignore-rules.md](story-09-05-ignore-rules.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.1 (effective settings) and feeds Stories 9.2
> (watcher pre-filter) and 9.3 (sweep walker filter).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Single matcher | `pipeline/src/maktaba_pipeline/ignore/matcher.py::IgnoreMatcher`. One class, used by the watcher and the sweep walker; built once per library, reused per event/file. |
| Glob engine | Python `pathspec` (gitignore-flavored matching) — handles `**`, `?`, `[abc]`, leading `/`, and trailing `/`. The same patterns work cross-platform; case-sensitivity is set by the OS hint at construction time. |
| Built-in patterns | Frozen tuple in `matcher.py::BUILTIN_IGNORE_PATTERNS`. Always applied; user `ignore_globs` are *added* on top. |
| Extension filter | Separate from glob match — checked after the glob filter passes. Source: a frozen set, configurable via app-level setting `supported_video_exts` (architecture §11.4). |
| Case sensitivity | `pathspec`'s `case_sensitive` flag is set from `os.name`: `False` on `nt`/`win32`, `True` on `posix`. Documented as AC-2 expects. |
| Out of scope | The user-facing endpoint to edit `ignore_globs` (Epic 7 Story 7.3 PATCH); the file-rescan when ignore globs change (intentional non-feature per story edge case). |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │  IgnoreMatcher.build(                                        │
   │     user_globs: list[str],                                   │
   │     supported_exts: frozenset[str],                          │
   │     *, case_sensitive: bool | None = None) → IgnoreMatcher  │
   │                                                              │
   │   patterns = BUILTIN_IGNORE_PATTERNS + tuple(user_globs)     │
   │   spec = pathspec.PathSpec.from_lines(                       │
   │             'gitwildmatch', patterns)                        │
   │   ext_set = {e.lower() for e in supported_exts}              │
   │   return IgnoreMatcher(spec, ext_set, case_sensitive)        │
   └──────────────────────────────────────────────────────────────┘
                       │
                       ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  IgnoreMatcher                                               │
   │   .matches(path: str | Path) -> bool                         │
   │     → True if any ignore pattern matches; caller skips it.   │
   │                                                              │
   │   .is_supported_extension(path: str | Path) -> bool          │
   │     → True if path's suffix (lowercased) ∈ ext_set.          │
   │                                                              │
   │   .filter_to_scannable(path) -> bool                         │
   │     → not matches() and is_supported_extension().            │
   │       The single helper used by walker and watcher.          │
   └──────────────────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/ignore/__init__.py` | Re-exports `IgnoreMatcher`, `build_matcher`, `BUILTIN_IGNORE_PATTERNS`, `DEFAULT_SUPPORTED_VIDEO_EXTS`. |
| `pipeline/src/maktaba_pipeline/ignore/matcher.py` | The matcher and the constants. |
| `pipeline/tests/ignore/test_matcher_unit.py` | Unit tests per §6.1. |
| `pipeline/tests/ignore/test_matcher_arabic.py` | Unicode-aware matching tests per §6.2. |
| `pipeline/tests/ignore/fixtures/` | Path fixture lists for parametrized tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/sweep/walker.py` | Replace placeholder `ignore` arg with `IgnoreMatcher`; call `filter_to_scannable`. |
| `pipeline/src/maktaba_pipeline/watcher/library_watcher.py` | Same — switch from "ignore_globs list" to `IgnoreMatcher`. |
| `pipeline/pyproject.toml` | Add `pathspec>=0.12`. |
| `shared/schema/library_settings.schema.json` | Confirm `ignore_globs` slot — schema change already in Story 9.1 plan. |

### 2.3 Type and constant definitions

```python
# pipeline/src/maktaba_pipeline/ignore/matcher.py
from __future__ import annotations
import os
from pathlib import Path

import pathspec
from pathspec.patterns import GitWildMatchPattern

# AC-1 — built-in patterns. Order doesn't matter; PathSpec ORs them.
BUILTIN_IGNORE_PATTERNS: tuple[str, ...] = (
    "**/.*",                  # any dotfile / dotdir at any depth
    "**/*.part",              # incomplete browser/curl downloads
    "**/*.crdownload",        # Chrome partial download
    "**/*.tmp",               # generic temp marker (some FTP clients)
    "**/.maktaba/**",         # our own sidecar dir; even if not hidden
    "**/.DS_Store",
    "**/Thumbs.db",
    "**/desktop.ini",
    "**/@eaDir/**",           # Synology metadata
    "**/.AppleDouble/**",     # macOS-on-NFS sidecar
    "**/__MACOSX/**",         # zip extraction artifact
    "**/lost+found/**",       # ext4 fsck dropbox
    "**/$RECYCLE.BIN/**",     # Windows recycle bin on shared volumes
    "**/.Trash-*/**",         # Linux desktop trash dirs
    "**/Sample.*",            # decade-old release-group habit (lowercase below)
    "**/sample.*",
)

DEFAULT_SUPPORTED_VIDEO_EXTS: frozenset[str] = frozenset({
    "mp4", "mkv", "mov", "m4v", "avi", "wmv", "flv", "webm",
    "mpeg", "mpg", "ts", "m2ts", "mts", "vob", "ogv", "3gp",
})
```

```python
# pipeline/src/maktaba_pipeline/ignore/matcher.py (continued)
class IgnoreMatcher:
    __slots__ = ("_spec", "_exts", "_case_sensitive")

    def __init__(
        self,
        spec: pathspec.PathSpec,
        exts: frozenset[str],
        case_sensitive: bool,
    ) -> None: ...

    def matches(self, path: str | Path) -> bool: ...
    def is_supported_extension(self, path: str | Path) -> bool: ...
    def filter_to_scannable(self, path: str | Path) -> bool: ...

    @property
    def case_sensitive(self) -> bool: ...
```

### 2.4 Function signatures

```python
def build_matcher(
    user_globs: list[str],
    supported_exts: frozenset[str] | None = None,
    *,
    case_sensitive: bool | None = None,
) -> IgnoreMatcher:
    """Construct an IgnoreMatcher with built-ins prepended.

    `case_sensitive=None` → derived from os.name.
    `supported_exts=None` → DEFAULT_SUPPORTED_VIDEO_EXTS.
    """
```

## 3. Database

No schema additions for this story. The matcher is constructed from the
already-stored `libraries.settings.ignore_globs` array.

## 4. Code scaffolding

### 4.1 Constructor & matching

```python
# pipeline/src/maktaba_pipeline/ignore/matcher.py
import os
from pathlib import PurePosixPath, PureWindowsPath, Path

import pathspec


def _normalize_for_match(path: str | Path) -> str:
    """pathspec wants forward-slash POSIX-style strings.
    On Windows we normalize backslashes; case is handled by the spec."""
    s = str(path)
    if os.sep == "\\":
        s = s.replace("\\", "/")
    return s


class IgnoreMatcher:
    __slots__ = ("_spec", "_exts", "_case_sensitive")

    def __init__(self, spec, exts, case_sensitive):
        self._spec = spec
        self._exts = exts
        self._case_sensitive = case_sensitive

    def matches(self, path) -> bool:
        return self._spec.match_file(_normalize_for_match(path))

    def is_supported_extension(self, path) -> bool:
        suffix = Path(path).suffix.lstrip(".")
        if not suffix:
            return False
        return suffix.lower() in self._exts

    def filter_to_scannable(self, path) -> bool:
        return self.is_supported_extension(path) and not self.matches(path)

    @property
    def case_sensitive(self):
        return self._case_sensitive


def build_matcher(user_globs, supported_exts=None, *,
                  case_sensitive=None) -> IgnoreMatcher:
    if case_sensitive is None:
        case_sensitive = os.name == "posix"
    if supported_exts is None:
        supported_exts = DEFAULT_SUPPORTED_VIDEO_EXTS

    patterns = list(BUILTIN_IGNORE_PATTERNS) + list(user_globs)

    # pathspec doesn't expose a case-sensitivity knob across versions;
    # we lower-case the patterns and the path on Windows.
    if not case_sensitive:
        patterns = [p.lower() for p in patterns]
        spec = pathspec.PathSpec.from_lines("gitwildmatch", patterns)
        # Wrap matches() to lower-case the path:
        original = spec.match_file
        spec.match_file = lambda p: original(p.lower())  # type: ignore
    else:
        spec = pathspec.PathSpec.from_lines("gitwildmatch", patterns)

    return IgnoreMatcher(
        spec=spec,
        exts=frozenset(e.lower() for e in supported_exts),
        case_sensitive=case_sensitive,
    )
```

### 4.2 Integration touchpoints (already referenced from Stories 9.2 / 9.3)

```python
# pipeline/src/maktaba_pipeline/watcher/library_watcher.py
from ..ignore.matcher import build_matcher

class LibraryWatcher:
    def __init__(self, library, debouncer, move_detector,
                 ignore_matcher, *, loop=None) -> None:
        self._ignore = ignore_matcher
        ...

    async def _on_event_async(self, e):
        if not self._ignore.is_supported_extension(e.src_path) \
                or self._ignore.matches(e.src_path):
            return  # silent skip; not even a counter increment for noise
        ...
```

```python
# pipeline/src/maktaba_pipeline/sweep/walker.py
from ..ignore.matcher import IgnoreMatcher

async def walk(root, ignore: IgnoreMatcher):
    ...
    for entry in os.scandir(d):
        if ignore.matches(entry.path):
            continue
        ...
        if entry.is_file() and not ignore.is_supported_extension(entry.path):
            continue
        ...
```

## 5. Test plan

### 5.1 Unit tests (`test_matcher_unit.py`)

For each BUILTIN_IGNORE_PATTERNS entry, parametrize:

| Pattern | Positive (should match) | Negative (should not) |
|---|---|---|
| `**/.*` | `/lib/.cache`, `/lib/sub/.config/x` | `/lib/normal.mp4`, `/lib/A.B/movie.mp4` (single dot but not hidden) |
| `**/*.part` | `/lib/foo.mp4.part`, `/lib/sub/x.part` | `/lib/part.mp4` |
| `**/*.crdownload` | `/lib/foo.mp4.crdownload` | `/lib/foo.crdownload.mp4` |
| `**/.maktaba/**` | `/lib/.maktaba/db/x.json`, `/lib/sub/.maktaba/y` | `/lib/.maktaba` (dir entry alone — gitwildmatch matches via `**/.*`) |
| `**/.DS_Store` | `/lib/.DS_Store`, `/lib/sub/.DS_Store` | `/lib/DS_Store` |
| `**/Thumbs.db` | `/lib/Thumbs.db` | `/lib/thumbs.db` (case-sensitive on POSIX); on Windows the case-insensitive build matches both. |
| `**/Sample.*` and `**/sample.*` | `/lib/Sample.mp4`, `/lib/sample.mp4`, `/lib/sub/Sample.avi` | `/lib/movie.sample.mp4` |
| `**/$RECYCLE.BIN/**` | `/lib/$RECYCLE.BIN/x` | `/lib/recycle/x` |

| Test | What it pins |
|---|---|
| `test_user_glob_extends_builtins` | `user_globs=["**/raw/**"]` → `/lib/raw/x.mp4` matches; `/lib/x.mp4` does not. |
| `test_supported_extension_set` | `.mp4` accepted; `.txt` rejected; case-folded extension match. |
| `test_filter_to_scannable_compound` | `.cache/x.mp4` → False (matches ignore); `x.txt` → False (ext); `x.mp4` → True. |
| `test_case_sensitive_on_posix` | `Thumbs.db` matches; `thumbs.db` does NOT match the builtin (lowercase has no builtin). |
| `test_case_insensitive_on_windows` | Build with `case_sensitive=False`; both `Thumbs.db` and `thumbs.db` match. |
| `test_freeze_library_with_star_star` | `user_globs=["**/*"]` → every path matches; sweep is a no-op. AC edge case. |
| `test_unknown_extension_rejected_even_if_ffprobe_capable` | `.divx` not in default ext set → rejected. AC edge case (no auto-detection). |

### 5.2 Unicode tests (`test_matcher_arabic.py`)

| Test | What it pins |
|---|---|
| `test_arabic_directory_name_passes_when_not_hidden` | `/lib/مكتبة/مقطع.mp4` → scannable. |
| `test_arabic_dotfile_skipped` | `/lib/.مخفي/x.mp4` matches `**/.*` and is skipped. |
| `test_user_glob_with_arabic` | `user_globs=["**/خام/**"]` (raw) → matches `/lib/خام/x.mp4`. |
| `test_nfc_vs_nfd_normalization` | A pattern in NFC and a path in NFD normalize-equivalent — match works because we normalize both to NFC before comparing. (Implementation: `unicodedata.normalize("NFC", s)` in `_normalize_for_match` for both pattern construction and path matching.) |

### 5.3 Performance gate

| Test | Target |
|---|---|
| `test_matcher_throughput_1m_paths_per_sec` | A pre-built matcher answers `filter_to_scannable` for 1,000,000 strings in ≤ 1 s on the CI runner. The walker calls this once per directory entry, so this caps walker overhead. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Deeply nested `.maktaba/` | The pattern is `**/.maktaba/**` not `.maktaba/**`; matches at any depth. | `test_maktaba_nested` |
| `**/*` user glob | Every path matches → sweep enqueues nothing → library is frozen but not deleted. Documented in operator handbook. | `test_freeze_library_with_star_star` |
| Adding an ignore glob after files are already indexed | The matcher is rebuilt on `library.settings_changed` (Story 9.1's NOTIFY); it changes *future* event handling only. Existing rows are untouched. The user must purge to remove them. | Documented; tested via Story 9.2's `test_settings_change_reloads_watcher`. |
| New extension support (e.g., `.divx`) | The user must add it to app-level `supported_video_exts`. Out of scope for Library config; lives in the global app config. | Documented |
| Path with weird characters (`?`, `[`) | `pathspec` treats `?` and `[` as glob metacharacters; users must escape them in user globs. The built-ins don't use these characters. | Documented in operator handbook; user globs sanity is at validation time (Story 9.1 schema). |
| Symlink to `.maktaba/` outside a library | `walker` resolves symlinks, then the matcher sees a path *inside* `.maktaba/` (or wherever the symlink points). The match runs against the *resolved* path, which is what we want. | `test_symlink_to_dotmaktaba_skipped` |
| Empty user globs list | Falls back to built-ins only; no extra patterns. | `test_empty_user_globs_uses_builtins_only` |
| Pattern with leading `/` (root-anchored) | `gitwildmatch` treats it as relative to the matched path's start; in our case-paths-are-absolute model, this is rarely useful. Document recommendation: use `**/foo` not `/foo`. | Documented |

## 7. Configuration

Read from effective settings:

| Key | Default | Effect |
|---|---|---|
| `ignore_globs` | `[]` | User-supplied extension to built-ins. |
| `supported_video_exts` (app-level, not per-library in v1) | the 16 listed in §2.3 | The set of file extensions that are eligible for scanning. |

`supported_video_exts` is intentionally app-level (not per-library);
making it per-library would invite divergent behaviour across roots and
complicate the watcher fast path. Documented in the limitations
section of the architecture.

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| `pathspec` | ≥ 0.12 | Mature gitignore-style matching; pure Python; good enough perf (≥ 1 M paths/s in §5.3 measurement). |
| stdlib `unicodedata` | py3 | NFC normalization for the Arabic test fixtures. |

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/ignore/matcher.py` exposes `IgnoreMatcher`, `build_matcher`, `BUILTIN_IGNORE_PATTERNS`, `DEFAULT_SUPPORTED_VIDEO_EXTS`.
- [ ] Watcher and walker construct the matcher exactly once per library and reuse it across events/files.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: each built-in pattern's positive and negative cases pass.
- [ ] AC-2: extensions outside `supported_video_exts` are skipped.
- [ ] AC-3: user `ignore_globs` extend the built-ins; effective on the next watcher event after a settings change.

**Behaviour (cross-platform)**
- [ ] On POSIX, `Thumbs.db` ≠ `thumbs.db`.
- [ ] On Windows builds, `case_sensitive=False` makes the comparisons identical.

**Performance**
- [ ] Matcher answers `filter_to_scannable` at ≥ 1 M paths/s on the CI runner.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.5.
- [ ] `docs/operations/ignore-rules.md` documents the built-ins, the user-glob format, and the "freeze a library with `**/*`" pattern.
