"""Each library's YouTube sources from its linked videos (slot 0182).

The ``link`` stage learns every video's YouTube id; ``youtube_videos`` names its channel
and ``youtube_playlist_items`` its playlists (``is_real``: a ``PL…`` playlist, not a
channel's tab). From those, for each library:

* **playlist**: when most of its linked videos sit in real playlists that no other
  library's videos dominate (it has strictly more of the playlist's linked videos than
  any other library), those playlists are its sources. Only a playlist that holds at
  least a tenth of the library's linked videos counts, so a stray video does not make a
  source. A channel that several libraries share through a playlist each gives each
  library its own playlist.
* **channel**: otherwise, when one channel holds most of its linked videos, that channel.
* **clash**: the channel or playlist is already another library's source (or another
  library has more of the channel's videos): no second source. The decision names that
  library, and the card shows it as the duplicate hint ("followed by …", merge).
* **mixed**: no channel holds most of them; **none**: no linked video names a channel.

A source the derivation makes is **off** (``derived_at`` set); a source that exists is
never changed, whatever its mode. Every decision, with its reason, is kept in
``youtube_source_decisions``; the channels' libraries go to ``youtube_channel_libraries``
(many to many), and a channel no ``youtube_channels`` row knows gets one (its
``library_id`` only when it became that library's channel source, so the listing reads
it).

It runs at the end of the ``link`` stage for a link that names a channel or playlist the
library's last decision did not see (:func:`after_link`), and over every library from the
reconciler's pass (:func:`derive`), which is also how links made before this existed are
covered. It is idempotent: a second run decides the same and creates nothing.
"""

from __future__ import annotations

import math
from collections import defaultdict
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from ..engine.sql import dumps, is_pg, loads, now, to_ts, uid
from ..log import get_logger

__all__ = [
    "Decision",
    "Evidence",
    "after_link",
    "decide",
    "derive",
    "load_evidence",
]

_log = get_logger()

#: a playlist counts for a library when it holds at least this share of its linked videos
MIN_PLAYLIST_SHARE = 0.1
#: the Postgres advisory lock one derivation holds while it decides and writes
LOCK_KEY = "maktaba.youtube_derive"


def channel_url(channel_id: str) -> str:
    return f"https://www.youtube.com/channel/{channel_id}"


def playlist_url(playlist_id: str) -> str:
    return f"https://www.youtube.com/playlist?list={playlist_id}"


@dataclass(slots=True)
class Evidence:
    """What Maktaba knows of every library's linked videos, and the sources it has."""

    #: library -> YouTube id -> its channel (None when youtube_videos does not say)
    videos: dict[str, dict[str, str | None]] = field(default_factory=dict)
    #: YouTube id -> the real playlists it is in
    playlists: dict[str, set[str]] = field(default_factory=dict)
    #: playlist id -> title
    playlist_titles: dict[str, str] = field(default_factory=dict)
    #: playlist id -> its channel (None when not known)
    playlist_channel: dict[str, str | None] = field(default_factory=dict)
    #: (kind, external id) -> the libraries whose source it is already
    sources: dict[tuple[str, str], set[str]] = field(default_factory=dict)
    #: channel id -> the library youtube_channels gives it (None: none)
    channel_holder: dict[str, str | None] = field(default_factory=dict)


@dataclass(slots=True)
class Decision:
    library_id: str
    outcome: str  # playlist | channel | clash | mixed | none
    kind: str | None
    external_ids: list[str]
    reason: str
    linked_videos: int
    matched_videos: int = 0
    clash_external_id: str | None = None
    clash_library_id: str | None = None
    #: every channel and real playlist of the library's linked videos
    seen: list[str] = field(default_factory=list)
    #: (channel, linked videos of this library) for youtube_channel_libraries
    channels: dict[str, int] = field(default_factory=dict)
    #: what this run made (filled when applied)
    created: list[str] = field(default_factory=list)

    def row(self) -> dict[str, Any]:
        return {
            "library_id": self.library_id,
            "outcome": self.outcome,
            "kind": self.kind,
            "external_ids": self.external_ids,
            "created": self.created,
            "clash_external_id": self.clash_external_id,
            "clash_library_id": self.clash_library_id,
            "linked_videos": self.linked_videos,
            "matched_videos": self.matched_videos,
            "reason": self.reason,
        }


async def load_evidence(db: Any) -> Evidence:
    """Every library's linked videos (``media_metadata_enrichment``, provider
    ``youtube``), their channels and real playlists, and every source there is."""
    ev = Evidence()
    rows = await db.fetch(
        "SELECT v.library_id AS lib, m.external_id AS yid, yv.channel_id AS cid"
        " FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id"
        " JOIN libraries l ON l.id = v.library_id"
        " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
        " WHERE m.provider = 'youtube' AND v.deleted_at IS NULL AND l.deleted_at IS NULL"
    )
    for r in rows:
        cid = r["cid"]
        ev.videos.setdefault(str(r["lib"]), {})[str(r["yid"])] = None if cid is None else str(cid)
    prow = await db.fetch(
        "SELECT i.youtube_id AS yid, p.playlist_id AS pid, p.title AS title,"
        " p.channel_id AS cid"
        " FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id"
        " WHERE p.is_real AND i.youtube_id IN (SELECT m.external_id FROM"
        "  media_metadata_enrichment m WHERE m.provider = 'youtube')"
    )
    for r in prow:
        ev.playlists.setdefault(str(r["yid"]), set()).add(str(r["pid"]))
        ev.playlist_titles[str(r["pid"])] = str(r["title"] or "")
        ev.playlist_channel[str(r["pid"])] = None if r["cid"] is None else str(r["cid"])
    for r in await db.fetch("SELECT library_id, kind, external_id FROM youtube_sources"):
        ev.sources.setdefault((str(r["kind"]), str(r["external_id"])), set()).add(
            str(r["library_id"])
        )
    for r in await db.fetch("SELECT channel_id, library_id FROM youtube_channels"):
        lib = r["library_id"]
        ev.channel_holder[str(r["channel_id"])] = None if lib is None else str(lib)
    return ev


def _plurality_owner(counts: Mapping[str, int]) -> str | None:
    """The library with strictly more than every other, or None on a tie."""
    ranked = sorted(counts.items(), key=lambda kv: (-kv[1], kv[0]))
    if not ranked:
        return None
    if len(ranked) > 1 and ranked[1][1] == ranked[0][1]:
        return None
    return ranked[0][0]


def decide(ev: Evidence) -> dict[str, Decision]:
    """Every library's decision (pure: reads only ``ev``)."""
    # playlist -> library -> linked videos; channel -> library -> linked videos
    per_playlist: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    per_channel: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    for lib, vids in ev.videos.items():
        for yid, cid in vids.items():
            for pid in ev.playlists.get(yid, ()):
                per_playlist[pid][lib] += 1
            if cid:
                per_channel[cid][lib] += 1
    owner = {pid: _plurality_owner(c) for pid, c in per_playlist.items()}

    out: dict[str, Decision] = {}
    channel_claims: dict[str, list[str]] = defaultdict(list)
    for lib in sorted(ev.videos):
        vids = ev.videos[lib]
        n = len(vids)
        mine_channels = {c: per_channel[c][lib] for c in per_channel if lib in per_channel[c]}
        mine_playlists = {p: per_playlist[p][lib] for p in per_playlist if lib in per_playlist[p]}
        seen = sorted(set(mine_channels) | set(mine_playlists))
        d = Decision(lib, "none", None, [], "", n, seen=seen, channels=mine_channels)
        out[lib] = d
        if not mine_channels and not mine_playlists:
            d.reason = "no linked video names a channel or a playlist"
            continue

        floor = max(1, math.ceil(MIN_PLAYLIST_SHARE * n))
        chosen = sorted(
            (p for p, k in mine_playlists.items() if owner.get(p) == lib and k >= floor),
            key=lambda p: (-mine_playlists[p], p),
        )
        covered = {y for y in vids if ev.playlists.get(y, set()) & set(chosen)}
        if chosen and 2 * len(covered) > n:
            # a playlist of a channel the library already follows whole is in that source
            followed = {c for (k, c), libs in ev.sources.items() if k == "channel" and lib in libs}
            inside = [p for p in chosen if ev.playlist_channel.get(p) in followed]
            if len(inside) == len(chosen):
                cid = str(ev.playlist_channel.get(chosen[0]))
                d.outcome, d.kind, d.external_ids = "channel", "channel", [cid]
                d.matched_videos = mine_channels.get(cid, 0)
                d.reason = (
                    f"{len(covered)} of {n} linked videos are in {len(chosen)} real"
                    " playlist(s) of the channel the library already follows"
                )
                continue
            chosen = [p for p in chosen if p not in inside]
            d.outcome, d.kind, d.external_ids = "playlist", "playlist", chosen
            d.matched_videos = len(covered)
            d.reason = (
                f"{len(covered)} of {n} linked videos are in {len(chosen) + len(inside)} real"
                " playlist(s) no other library's videos dominate"
            )
            if inside:
                d.reason += f" ({len(inside)} in the channel it already follows)"
            continue
        if not mine_channels:
            d.outcome = "mixed"
            d.reason = "the linked videos' playlists are other libraries' or too small"
            continue
        top = sorted(mine_channels.items(), key=lambda kv: (-kv[1], kv[0]))[0]
        if 2 * top[1] <= n:
            d.outcome = "mixed"
            d.reason = f"no channel holds most of the {n} linked videos (the most: {top[1]})"
            continue
        d.outcome, d.kind, d.external_ids, d.matched_videos = "channel", "channel", [top[0]], top[1]
        d.reason = f"{top[1]} of {n} linked videos are the channel's"
        channel_claims[top[0]].append(lib)

    # one library per channel: an existing source wins, then the most videos, then the
    # library youtube_channels gives it
    for cid, libs in channel_claims.items():
        holders = ev.sources.get(("channel", cid), set())
        if holders:
            winner = (
                sorted(holders)[0] if not (holders & set(libs)) else sorted(holders & set(libs))[0]
            )
        else:
            held = ev.channel_holder.get(cid)
            winner = sorted(libs, key=lambda x: (-per_channel[cid][x], 0 if x == held else 1, x))[0]
        for lib in libs:
            if lib == winner:
                continue
            d = out[lib]
            d.outcome, d.clash_external_id, d.clash_library_id = "clash", cid, winner
            d.reason = (
                f"{d.matched_videos} of {d.linked_videos} linked videos are the channel's,"
                " which is another library's source"
                if holders
                else f"{d.matched_videos} of {d.linked_videos} linked videos are the channel's,"
                " and another library has more of them"
            )

    # a playlist already another library's source is not made a second time
    for d in out.values():
        if d.outcome != "playlist":
            continue
        keep: list[str] = []
        for pid in d.external_ids:
            others = ev.sources.get(("playlist", pid), set()) - {d.library_id}
            if others:
                d.clash_external_id = d.clash_external_id or pid
                d.clash_library_id = d.clash_library_id or sorted(others)[0]
            else:
                keep.append(pid)
        if not keep:
            d.outcome = "clash"
            d.reason += "; each is another library's source"
        elif len(keep) < len(d.external_ids):
            d.reason += f"; {len(d.external_ids) - len(keep)} already another library's source"
        d.external_ids = keep
    return out


async def _lock(db: Any) -> None:
    if is_pg(db):
        await db.execute("SELECT pg_advisory_xact_lock(hashtext($1))", LOCK_KEY)


async def _apply(db: Any, d: Decision, ev: Evidence) -> None:
    lib = d.library_id
    t = to_ts(db, now())
    # the channels' libraries (many to many): this library's rows are what its links say
    await db.execute("DELETE FROM youtube_channel_libraries WHERE library_id = $1", uid(lib))
    for cid, k in sorted(d.channels.items()):
        await db.execute(
            "INSERT INTO youtube_channel_libraries (channel_id, library_id, linked_videos,"
            " updated_at) VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id, library_id) DO UPDATE"
            " SET linked_videos = EXCLUDED.linked_videos, updated_at = EXCLUDED.updated_at",
            cid,
            uid(lib),
            k,
            t,
        )
        # every channel a linked video names is known; it is this library's to list only
        # when it became its channel source
        mine = d.outcome == "channel" and d.external_ids == [cid]
        await db.execute(
            "INSERT INTO youtube_channels (channel_id, library_id, url, fetched_at)"
            " VALUES ($1, $2, $3, $4) ON CONFLICT (channel_id) DO UPDATE SET"
            " library_id = COALESCE(youtube_channels.library_id, EXCLUDED.library_id)",
            cid,
            uid(lib) if mine else None,
            channel_url(cid),
            t,
        )
    if d.outcome in ("playlist", "channel"):
        for ext in d.external_ids:
            if lib in ev.sources.get((d.kind or "", ext), set()):
                continue  # the library's already, as it is
            url = channel_url(ext) if d.kind == "channel" else playlist_url(ext)
            rows = await db.fetch(
                "INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at,"
                " derived_at) VALUES ($1, $2, $3, $4, 'off', $5, $5)"
                " ON CONFLICT (library_id, kind, external_id) DO NOTHING RETURNING id",
                uid(lib),
                d.kind,
                url,
                ext,
                t,
            )
            if rows:
                d.created.append(ext)
                ev.sources.setdefault((d.kind or "", ext), set()).add(lib)
    await db.execute(
        "INSERT INTO youtube_source_decisions (library_id, outcome, kind, external_ids, created,"
        " clash_external_id, clash_library_id, linked_videos, matched_videos, reason, seen,"
        " decided_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)"
        " ON CONFLICT (library_id) DO UPDATE SET outcome = EXCLUDED.outcome,"
        " kind = EXCLUDED.kind, external_ids = EXCLUDED.external_ids,"
        " created = EXCLUDED.created, clash_external_id = EXCLUDED.clash_external_id,"
        " clash_library_id = EXCLUDED.clash_library_id,"
        " linked_videos = EXCLUDED.linked_videos, matched_videos = EXCLUDED.matched_videos,"
        " reason = EXCLUDED.reason, seen = EXCLUDED.seen, decided_at = EXCLUDED.decided_at",
        uid(lib),
        d.outcome,
        d.kind,
        dumps(d.external_ids),
        dumps(d.created),
        d.clash_external_id,
        None if d.clash_library_id is None else uid(d.clash_library_id),
        d.linked_videos,
        d.matched_videos,
        d.reason,
        dumps(d.seen),
        t,
    )
    if d.created or d.outcome == "clash":
        _log.info(
            "youtube_sources_derived",
            library=lib,
            outcome=d.outcome,
            kind=d.kind,
            created=d.created,
            clash=d.clash_external_id,
            clash_library=d.clash_library_id,
            reason=d.reason,
        )


async def derive(
    db: Any, libraries: Iterable[UUID | str] | None = None, *, apply: bool = True
) -> list[Decision]:
    """Decide every library's sources (or only ``libraries``'), and write them unless
    ``apply`` is false (a dry run: it only reads)."""
    want = None if libraries is None else {str(x) for x in libraries}
    if not apply:
        decisions = decide(await load_evidence(db))
        return [d for k, d in sorted(decisions.items()) if want is None or k in want]
    async with db.transaction():
        await _lock(db)
        ev = await load_evidence(db)
        decisions = decide(ev)
        out = [d for k, d in sorted(decisions.items()) if want is None or k in want]
        for d in out:
            await _apply(db, d, ev)
    return out


async def after_link(db: Any, library_id: UUID, youtube_id: str) -> Decision | None:
    """The link stage's step: decide the library again when this link names a channel or
    a real playlist its last decision did not see (or it has none yet)."""
    row = await db.fetchrow(
        "SELECT channel_id FROM youtube_videos WHERE youtube_id = $1", youtube_id
    )
    ids = {str(row["channel_id"])} if row is not None and row["channel_id"] else set()
    for r in await db.fetch(
        "SELECT i.playlist_id AS pid FROM youtube_playlist_items i"
        " JOIN youtube_playlists p ON p.playlist_id = i.playlist_id"
        " WHERE i.youtube_id = $1 AND p.is_real",
        youtube_id,
    ):
        ids.add(str(r["pid"]))
    if not ids:
        return None
    seen_row = await db.fetchrow(
        "SELECT seen FROM youtube_source_decisions WHERE library_id = $1", uid(library_id)
    )
    if seen_row is not None:
        seen = loads(seen_row["seen"]) or []
        if ids <= {str(s) for s in seen}:
            return None
    got = await derive(db, [library_id])
    return got[0] if got else None
