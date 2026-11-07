"""Display titles in a block's ``title_snapshot`` (Story 27.11).

The scheduler caches each block's guide metadata in ``channel_programs.title_snapshot``;
the guide, the XMLTV export and the streaming strip read only that blob. This module lays
a video's display title (``videos.display_title_manual``, else ``videos.display_title``),
its series' display name and its episode number over the snapshot:

* ``on_air``: the strip's line («من زاوية أخرى · الحلقة 45 — في الخنادق…»), always;
* ``strip_title`` / ``strip_series`` / ``strip_episode``: its parts; ``strip_ref``: the
  reference of a Quran verse the title quotes («البقرة ١٨٣»), the strip's last page;
* with ``display_titles.use_in_guide``: ``title`` (the display title), ``series``,
  ``episode`` and ``episode_title`` too; the library title stays as ``source_title``.

A video with no display title yet (or a block with no video) keeps its snapshot as it
was; the strip then shows its ``title``.
"""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from typing import Any

from ..classify.display_title import (
    DisplayTitleConfig,
    episode_label,
    is_arabic,
    on_air_line,
    verse_reference,
)

__all__ = ["DisplayInfo", "display_info", "merge_snapshot"]

#: one select of the display parts (each dialect's WHERE is added: a fixed text each)
_DISPLAY_SQL = (
    "SELECT v.id AS id, COALESCE(NULLIF(v.display_title_manual, ''), v.display_title)"
    " AS title, (v.display_title_manual IS NOT NULL AND v.display_title_manual <> '')"
    " AS manual, v.display_title IS NOT NULL AS done,"
    " COALESCE(s.display_name, s.name_override, s.name) AS series,"
    " COALESCE(se.episode_override, se.episode, se.absolute_number) AS ep,"
    " se.series_id AS sid"
    " FROM videos v"
    " LEFT JOIN series_episodes se ON se.video_id = v.id"
    " LEFT JOIN series s ON s.id = se.series_id"
)


class DisplayInfo(dict[str, Any]):
    """``title`` (effective display title, may be empty), ``series``, ``episode``."""


async def display_info(db: Any, video_ids: Sequence[str]) -> dict[str, DisplayInfo]:
    """The display parts of these videos, by id (missing columns: an empty mapping)."""
    ids = sorted({str(v) for v in video_ids if v})
    out: dict[str, DisplayInfo] = {}
    if not ids:
        return out
    if getattr(db, "dialect", "postgres") == "postgres":
        rows = await db.fetch(_DISPLAY_SQL + " WHERE v.id = ANY($1::uuid[])", ids)
    else:
        rows = await db.fetch(
            _DISPLAY_SQL + " WHERE v.id IN (SELECT value FROM json_each($1))", json.dumps(ids)
        )
    for r in rows:
        if not (r["done"] or r["manual"]):
            continue
        ep = r["ep"]
        out[str(r["id"])] = DisplayInfo(
            title=str(r["title"] or ""),
            series=str(r["series"] or "") if r["sid"] is not None else "",
            episode=int(ep) if ep is not None else None,
        )
    return out


def merge_snapshot(
    snap: Mapping[str, Any], info: Mapping[str, Any] | None, cfg: DisplayTitleConfig
) -> dict[str, Any]:
    """``snap`` with the display parts laid over it (see the module doc)."""
    out = dict(snap)
    if info is None or (not cfg.enabled and not info.get("title")):
        return out  # the strip falls back to the title
    raw = str(out.get("source_title") or out.get("title") or "")
    title = str(info.get("title") or "")
    series = str(info.get("series") or "")
    ep = info.get("episode")
    label = episode_label(ep, is_arabic(title or series or raw), cfg)
    line = on_air_line(title, series, label) or raw
    out["on_air"] = line
    out["strip_title"] = title or line
    if ref := verse_reference(title):
        out["strip_ref"] = ref  # the strip shows the quoted verse whole, then this
    if series:
        out["strip_series"] = series
    if label:
        out["strip_episode"] = label
    if cfg.use_in_guide:
        if raw:
            out["source_title"] = raw
        out["title"] = title or line
        if series:
            out["series"] = series
            if title:
                out["episode_title"] = title
        if ep is not None and cfg.keep_episode_number:
            out["episode"] = int(ep)
    return out
