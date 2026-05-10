"""Canonical and alias paths for subtitle sidecars.

The canonical file lives under ``<library>/.maktaba/subs/`` keyed by
the video's content hash, so renames and moves don't break the link
between a video and its generated subtitles. The alias is a hard
link (or copy fallback) beside the source video so off-the-shelf
players that look for ``Source.lang.srt`` keep working.
"""

from __future__ import annotations

from pathlib import Path

__all__ = [
    "alias_path_for",
    "canonical_subtitle_path",
    "ensure_sidecar_dirs",
]


def canonical_subtitle_path(
    library_root: Path,
    content_hash: str,
    lang: str,
    fmt: str,
) -> Path:
    """Return the canonical sidecar path under ``.maktaba/subs/``.

    Layout: ``<library_root>/.maktaba/subs/<hash>.<lang>.<fmt>``. We
    keep the components flat (no per-hash subdirectory) so the
    directory enumerates cheaply during integrity sweeps.
    """
    return library_root / ".maktaba" / "subs" / f"{content_hash}.{lang}.{fmt}"


def alias_path_for(source_video: Path, lang: str, fmt: str) -> Path:
    """Return the alias path beside ``source_video``.

    Uses the source's *full* stem (everything before the last
    extension) so a video named ``Talk.2024.mp4`` yields an alias of
    ``Talk.2024.<lang>.<fmt>``. ``Path.stem`` strips only one suffix
    which is exactly what we want here; the multi-dot stem is the
    caller's natural identifier.
    """
    stem = source_video.stem
    return source_video.parent / f"{stem}.{lang}.{fmt}"


def ensure_sidecar_dirs(library_root: Path) -> Path:
    """Create ``.maktaba/subs/`` and ``.maktaba/.tmp/`` if missing.

    Returns the ``.maktaba/`` path so callers can chain. Raises
    :class:`OSError` annotated with ``kind="sidecar_dir"`` when the
    library root lacks write permission — we wrap the underlying
    error to make it discoverable in logs without losing the
    original ``errno``.
    """
    maktaba = library_root / ".maktaba"
    subs = maktaba / "subs"
    tmp = maktaba / ".tmp"
    try:
        subs.mkdir(mode=0o755, parents=True, exist_ok=True)
        tmp.mkdir(mode=0o755, parents=True, exist_ok=True)
    except OSError as exc:
        # Re-raise with structured context so the stage handler can
        # surface it without sniffing message strings.
        err = OSError(
            exc.errno,
            f"cannot create sidecar directory under {library_root}: {exc.strerror}",
        )
        err.filename = str(library_root)
        err.kind = "sidecar_dir"  # type: ignore[attr-defined]
        raise err from exc
    return maktaba
