"""Per-host path mapping (multi-host workers, plan 06-11 notes N2; story 31.3).

The database stores media paths in the canonical form ``smb://<server>/<share>/...``
(story 31.3; :mod:`maktaba_pipeline.sharepath`), or, before the migration, exactly as
the home's scanner saw them (``/Volumes/Originals/...``, ``Y:/Channel/a.mp4``). Each
worker converts at the file-system boundary only, through the active map: a
:class:`~maktaba_pipeline.sharepath.resolver.Resolver` (a :class:`PathMap` built from
this machine's mounts, the storage servers and the admin's override) whose prefix
entries cover both forms.
"""

from __future__ import annotations

import json
import os
import re
import threading
import time
import unicodedata
from dataclasses import dataclass

__all__ = [
    "PathMap",
    "active_path_map",
    "find_existing",
    "set_active_path_map",
    "to_db",
    "to_local",
]


@dataclass(frozen=True, slots=True)
class PathMap:
    """Ordered ``(db_prefix, local_prefix)`` pairs; the longest db prefix wins."""

    entries: tuple[tuple[str, str], ...] = ()

    @classmethod
    def parse(cls, text: str | None) -> PathMap:
        """Parse ``Y:/=/Volumes/Y/;O:/=/Volumes/O/`` or a JSON object ``{"Y:/": "/Volumes/Y/"}``."""
        if not text or not text.strip():
            return cls()
        text = text.strip()
        pairs: list[tuple[str, str]] = []
        if text.startswith("{"):
            pairs = [(str(k), str(v)) for k, v in json.loads(text).items()]
        else:
            for part in text.split(";"):
                if "=" in part:
                    a, _, b = part.partition("=")
                    pairs.append((a.strip(), b.strip()))
        return cls(tuple(sorted((_norm(a), _norm(b)) for a, b in pairs if a and b)))

    def rewrite(self, db_path: str) -> str:
        """The prefix rewrite alone, no file-system access: for a path another system
        opens (Plex's view of the NAS), not this machine."""
        p = db_path.replace("\\", "/")
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            if p.lower().startswith(src.lower()) and (best is None or len(src) > len(best[0])):
                best = (src, dst)
        if best is None:
            return db_path
        return best[1] + p[len(best[0]) :]

    def to_local(self, db_path: str) -> str:
        """This machine's path: :meth:`rewrite`, then the spelling that exists here."""
        p = db_path.replace("\\", "/")
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            if p.lower().startswith(src.lower()) and (best is None or len(src) > len(best[0])):
                best = (src, dst)
        if best is None:
            return find_existing(db_path)
        return find_existing(best[1] + p[len(best[0]) :])

    def to_db(self, local_path: str) -> str:
        """Map a local path back to the database's form; matched case-insensitively like
        :meth:`to_local` (``Y:/`` and ``y:/``, ``\\\\NAS\\share`` and ``//nas/share``)."""
        p = local_path.replace("\\", "/")
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            if p.lower().startswith(dst.lower()) and (best is None or len(dst) > len(best[1])):
                best = (src, dst)
        if best is None:
            return p
        return best[0] + p[len(best[1]) :]

    def as_json(self) -> dict[str, str]:
        return dict(self.entries)


def _norm(prefix: str) -> str:
    """Forward slashes and a trailing ``/``: a UNC prefix ``\\\\nas\\share`` becomes
    ``//nas/share/`` (Windows accepts it, and it round-trips through the map)."""
    p = prefix.replace("\\", "/")
    return p if p.endswith("/") else p + "/"


# -- the same name in another Unicode form -------------------------------------------
#
# macOS hands out file names decomposed (NFD: "أ" as alef + U+0654) and its scanner
# stored them so; Windows and Linux open a name only in the form the share holds
# (usually composed, NFC). A non-ASCII path that does not exist as given is tried in
# NFC, then NFD, then component by component against the directory listings compared
# in NFC (cached per directory), so a mixed path (folder in one form, file in the
# other) is found too. An ASCII path, or one that exists, costs nothing extra.

_LISTING_TTL_SEC = 300.0
_LISTING_MAX = 2048
_listings: dict[str, tuple[float, dict[str, str], dict[str, str]]] = {}
_listings_lock = threading.Lock()

_ROOT_RE = re.compile(r"^(?:[A-Za-z]:/|//[^/]+/[^/]+/|/)")


def _exists(p: str) -> bool:
    try:
        return os.path.exists(p)
    except (OSError, ValueError):
        return False


def _nfc(s: str) -> str:
    return unicodedata.normalize("NFC", s)


def _listing(directory: str) -> tuple[dict[str, str], dict[str, str]]:
    """``({nfc name: name}, {nfc casefolded name: name})`` of one directory, cached."""
    now = time.monotonic()
    with _listings_lock:
        hit = _listings.get(directory)
        if hit is not None and now - hit[0] < _LISTING_TTL_SEC:
            return hit[1], hit[2]
    try:
        names = os.listdir(directory)
    except (OSError, ValueError):
        names = []
    exact = {_nfc(n): n for n in names}
    folded = {_nfc(n).casefold(): n for n in names}
    with _listings_lock:
        if len(_listings) >= _LISTING_MAX:
            _listings.clear()
        _listings[directory] = (now, exact, folded)
    return exact, folded


def forget_listings() -> None:
    """Drop the cached directory listings (tests; a share remounted)."""
    with _listings_lock:
        _listings.clear()


def _walk(path: str) -> str | None:
    p = path.replace("\\", "/")
    m = _ROOT_RE.match(p)
    root = m.group(0) if m else ""
    cur = root
    for seg in [x for x in p[len(root) :].split("/") if x]:
        nxt = cur + seg if cur.endswith("/") or not cur else cur + "/" + seg
        if _exists(nxt):
            cur = nxt
            continue
        exact, folded = _listing(cur or ".")
        name = exact.get(_nfc(seg)) or folded.get(_nfc(seg).casefold())
        if name is None:
            return None
        cur = cur + name if cur.endswith("/") or not cur else cur + "/" + name
    return cur if cur else None


def find_existing(path: str) -> str:
    """``path`` as it exists on this machine, whatever Unicode form it was stored in;
    ``path`` itself when no spelling of it exists (a file still to be written)."""
    if not path or path.isascii() or _exists(path):
        return path
    for form in ("NFC", "NFD"):
        cand = unicodedata.normalize(form, path)
        if cand != path and _exists(cand):
            return cand
    found = _walk(path)
    return found if found is not None else path


_ACTIVE = PathMap()


def set_active_path_map(path_map: PathMap) -> None:
    global _ACTIVE
    _ACTIVE = path_map


def active_path_map() -> PathMap:
    return _ACTIVE


def to_local(db_path: str) -> str:
    """Map a database path to this host's file-system path."""
    return _ACTIVE.to_local(db_path)


def to_db(local_path: str) -> str:
    """The database form of a path on this host: canonical when it is on a known
    share; otherwise the legacy map's form, or the path unchanged when nothing maps it."""
    pm = _ACTIVE
    if not pm.entries and not getattr(pm, "shares", ()):
        return local_path
    out = pm.to_db(local_path)
    return local_path if out == local_path.replace("\\", "/") else out
