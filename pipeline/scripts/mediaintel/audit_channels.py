"""Audit: is what each channel airs about the channel?

Reads what dizqueTV actually holds (its channels, their programmes and filler lists) and
traces every item back to its file, series and tags. Each item gets a verdict against
its channel's theme (the same rules the planner uses):

* ``strong``: the show's genre is the theme, or a topic/occasion tag with real evidence
  (the name, the description, most episodes), or the episode's own tag;
* ``weak``: matched only on thin evidence: a playlist name, having aired during
  Ramadan, a few episode titles, or a person's name («الشيخ رمضان …» is not Ramadan);
* ``audience``/``format``: matched by audience (Kids) or format (Talk Shows), the
  channel's own rule;
* ``none``: nothing ties it to the channel.

Writes ``channel-audit.txt`` and ``channel-audit.html`` to the reports folder.
"""

from __future__ import annotations

import json
import sqlite3
from collections import defaultdict
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from maktaba_pipeline.schedule.channel_audit import (
    NAME_RAMADAN,
    VERDICTS,
    WINDOW_DAYS,
    AuditLookup,
    ChannelAudit,
    airtime,
    evidence_strength,
    html_report,
    judge,
    text_report,
)
from maktaba_pipeline.schedule.channel_audit import audit as audit_lookup
from maktaba_pipeline.schedule.channel_planner import PlannerConfig

__all__ = [
    "NAME_RAMADAN",
    "VERDICTS",
    "WINDOW_DAYS",
    "airtime",
    "audit",
    "evidence_strength",
    "judge",
]


# --------------------------------------------------------------------------- reading


def _get(url: str) -> Any:
    import urllib.request

    with urllib.request.urlopen(url, timeout=300) as r:  # noqa: S310 — local dizqueTV
        return json.loads(r.read().decode("utf-8"))


def read_dizquetv(url: str) -> tuple[list[dict[str, Any]], dict[str, dict[str, Any]]]:
    """Every channel (with its programmes) and the filler lists they use, from the API."""
    base = url.rstrip("/")
    channels = [_get(f"{base}/api/channel/{int(n)}") for n in sorted(_get(f"{base}/api/channels"))]
    ids = {str(fc.get("id")) for ch in channels for fc in ch.get("fillerCollections", [])}
    return channels, {i: _get(f"{base}/api/filler/{i}") for i in ids}


def lookup_from_cache(conn: sqlite3.Connection) -> AuditLookup:
    """The mediaintel cache's files, series and tags, as the audit traces them."""
    item_of = {str(k): p for k, p in conn.execute(
        "SELECT plex_rating_key, path FROM files WHERE plex_rating_key IS NOT NULL")}  # fmt: skip
    stags: dict[str, dict[str, tuple[float, str]]] = defaultdict(dict)
    for sid, tag, score, ev in conn.execute(
        "SELECT series_id, tag, score, evidence FROM series_tags"
    ):
        stags[sid][tag] = (float(score), ev or "")
    itags: dict[str, dict[str, float]] = defaultdict(dict)
    for table in ("video_tags", "episode_tags"):  # an episode's own tags win
        for p, tag, score in conn.execute(f"SELECT path, tag, score FROM {table}"):  # noqa: S608
            itags[p][tag] = float(score)
    return AuditLookup(
        item_of=item_of,
        series_of=dict(conn.execute("SELECT path, series_id FROM series_episodes")),
        library_of=dict(conn.execute("SELECT id, library FROM series")),
        folder_of={
            p: Path(f or "").name for p, f in conn.execute("SELECT path, folder FROM files")
        },
        series_tags=stags,
        item_tags=itags,
    )


def audit(
    conn: sqlite3.Connection,
    channels: Sequence[Mapping[str, Any]],
    fillers: Mapping[str, Mapping[str, Any]],
    cfg: PlannerConfig,
    now_ms: int | None = None,
) -> list[ChannelAudit]:
    def progress(n: int, total: int, name: str) -> None:
        print(f"  [{n}/{total}] {name}", flush=True)

    return audit_lookup(channels, fillers, lookup_from_cache(conn), cfg, now_ms,
                        on_channel=progress)  # fmt: skip


def run(conn: sqlite3.Connection, url: str, out: Path, cfg: PlannerConfig | None = None) -> str:
    if cfg is None:
        from . import settings_store

        cfg = settings_store.get(conn, "planner")
    print("phase: reading dizqueTV's channels and filler lists", flush=True)
    channels, fillers = read_dizquetv(url)
    print("phase: judging every programme and clip", flush=True)
    audits = audit(conn, channels, fillers, cfg)
    out.mkdir(parents=True, exist_ok=True)
    text = text_report(audits)
    (out / "channel-audit.txt").write_text(text, encoding="utf-8")
    (out / "channel-audit.html").write_text(html_report(audits), encoding="utf-8")
    print(text.split("\n\n", 2)[0] + "\n" + text.split("\n\n", 2)[1].split("\n===")[0])
    print(f"wrote {out / 'channel-audit.html'}")
    return text
