"""Story 9.5 — built-in + user ignore rules and supported-extension filtering.

This is the canonical decision: *should the scanner enqueue this file?*
Both the bootstrap walker (``scanner.walker``) and the live watcher
(``watcher.dispatch``) consult the same predicate so the two paths can
never disagree.

The split with :mod:`scanner.walker`'s built-in defaults is intentional:
the walker only knows about its own directory pruning rules. This module
adds the *library-scoped* knobs (per-config ``ignore_globs``) and the
explicit AC-1 vocabulary (`*.part`, `.maktaba`, `.DS_Store`, `Thumbs.db`)
expressed in glob form so they survive walker refactors.

Case sensitivity follows the OS: a Linux/macOS scan is case-sensitive,
Windows is case-insensitive (architecture §3.1). The match function
detects the platform via ``os.path.normcase`` rather than checking
``sys.platform`` so unit tests that override the case helper see the
expected behaviour.
"""

from __future__ import annotations

import fnmatch
import os
import re
from collections.abc import Iterable
from dataclasses import dataclass, field

__all__ = [
    "BUILT_IN_IGNORE_GLOBS",
    "DEFAULT_SUPPORTED_EXTS",
    "IgnoreFilter",
    "compile_globs",
    "is_supported_extension",
]


#: AC-1 — every scan ignores these silently. ``**/.maktaba/**`` covers
#: deep nesting (``/library/sub/.maktaba/...``) per the EC; we keep the
#: explicit ``.maktaba`` rule in :mod:`scanner.walker` too so the walker
#: can prune the dir without re-reading the rule list.
BUILT_IN_IGNORE_GLOBS: tuple[str, ...] = (
    "**/.*",
    "**/*.part",
    "**/*.crdownload",
    "**/.maktaba/**",
    "**/.DS_Store",
    "**/Thumbs.db",
)

#: AC-2 — the default supported set. Reads as **lower-cased** suffixes
#: (``".mp4"``, etc.). Matches architecture §3.1 verbatim. Users extend
#: this in app settings (out of scope for v1; see the EC).
DEFAULT_SUPPORTED_EXTS: frozenset[str] = frozenset(
    {
        ".mp4",
        ".mkv",
        ".mov",
        ".m4v",
        ".avi",
        ".wmv",
        ".flv",
        ".webm",
        ".mpeg",
        ".mpg",
        ".ts",
        ".m2ts",
        ".mts",
        ".vob",
        ".ogv",
        ".3gp",
    }
)


def is_supported_extension(
    name: str,
    extensions: Iterable[str] = DEFAULT_SUPPORTED_EXTS,
) -> bool:
    """True if ``name``'s extension is in ``extensions``.

    The set must contain lower-cased suffixes including the leading dot.
    The check is case-insensitive at the extension level (``Foo.MP4``
    and ``bar.mp4`` both match).
    """
    _, ext = os.path.splitext(name)
    return ext.lower() in {e.lower() for e in extensions}


def compile_globs(patterns: Iterable[str]) -> list[re.Pattern[str]]:
    """Translate glob patterns to compiled regexes once.

    ``fnmatch`` runs a fresh translate() per call; pre-compiling matters
    when the watcher checks every event against a 50-entry ignore list.
    """
    out: list[re.Pattern[str]] = []
    for p in patterns:
        translated = fnmatch.translate(p)
        out.append(re.compile(translated))
    return out


@dataclass(slots=True)
class IgnoreFilter:
    """Combined built-in + user ignore predicate.

    ``user_globs`` come from the library's ``settings.ignore_globs``
    (Story 9.1); ``extensions`` is the per-library effective set (defaults
    to :data:`DEFAULT_SUPPORTED_EXTS`).

    Use one instance per library and reuse it across scans / watcher
    events — :func:`compile_globs` runs once at construction.
    """

    user_globs: tuple[str, ...] = ()
    extensions: frozenset[str] = field(default_factory=lambda: DEFAULT_SUPPORTED_EXTS)
    case_insensitive: bool | None = None
    _compiled: list[re.Pattern[str]] = field(init=False, repr=False)

    def __post_init__(self) -> None:
        all_patterns = list(BUILT_IN_IGNORE_GLOBS) + list(self.user_globs)
        self._compiled = compile_globs(all_patterns)
        if self.case_insensitive is None:
            # Detect from os.path.normcase: on Windows / case-insensitive
            # filesystems, normcase lower-cases the input.
            self.case_insensitive = os.path.normcase("ABC") == "abc"

    @property
    def compiled_patterns(self) -> list[re.Pattern[str]]:
        return self._compiled

    def is_ignored(self, path: str) -> bool:
        """Return True if ``path`` matches a built-in or user ignore.

        Path is matched as-given; callers should pass the absolute path
        so deep ``**`` patterns work.
        """
        candidate = path
        if self.case_insensitive:
            candidate = path.lower()
        return any(pat.match(candidate) for pat in self._compiled)

    def is_acceptable(self, path: str) -> bool:
        """True if the file should be enqueued (passes ignore + ext)."""
        if self.is_ignored(path):
            return False
        return is_supported_extension(os.path.basename(path), self.extensions)
