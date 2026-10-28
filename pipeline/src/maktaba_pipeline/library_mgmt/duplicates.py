"""Find a library's duplicate groups (Epic 47, story 47.6: ``dedup_scan``).

Two tiers (``specs/epics/47-offline-convert-dedup`` section B):

* **exact**: the same ``content_hash`` (BLAKE3 of the first and last 4 MiB and the size)
  **and** the same full-file BLAKE3. Within a library two such files are one video
  (``videos`` is unique on ``(library_id, content_hash)``), so they are a video's own path
  and its ``video_copies`` (story 47.5); across libraries they are rows of different
  libraries with one content hash. The full hash is computed here, for these candidates
  only (``videos.full_hash`` for a row's own file; a copy's is in its member ``score``).
* **same_video**: other files of one video: the same YouTube id
  (``media_metadata_enrichment``, provider ``youtube``), or the same title once
  normalised with a duration within 1 s and ``video_signals.frame_hashes`` within dHash
  distance 6 on at least 80 % of the sampled frames.

A group is remembered by ``members_key``, the hash of its sorted members (``video id |
path``: a copy is a member by its path). A dismissed group with the same members never
comes back; an open one is left as it is (no second read of its files).

The keeper is chosen by the spec's rule (:func:`keeper_order`); among the paths of one
row (its own file and its copies, the same bytes) the row's own path is kept: it is the
one the library has known longest and the one its sidecars and derived data name.

Paths are the database's form; ``to_local`` maps them to this machine.
"""

from __future__ import annotations

import asyncio
import hashlib
import os
import re
import unicodedata
import uuid
from collections.abc import Awaitable, Callable, Iterable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any, Literal

from ..engine import paths
from ..engine.sql import dumps, loads, now, parse_ts, to_ts, uid
from ..identity.hasher import hash_full_file
from ..thumbnail.dhash import hamming

__all__ = [
    "DIRECT_AUDIO",
    "DIRECT_VIDEO",
    "Facts",
    "Group",
    "Member",
    "ScanPlan",
    "WriteReport",
    "auto_exact",
    "find_groups",
    "frames_match",
    "keeper_order",
    "load_facts",
    "members_key",
    "normalise_title",
    "write_groups",
]

Tier = Literal["exact", "same_video"]
ToLocal = Callable[[str], str]
Progress = Callable[[int, int], Awaitable[None]]

#: rule 1: a file that plays directly in a browser: MP4 with H.264/HEVC and AAC
DIRECT_VIDEO = frozenset({"h264", "hevc"})
DIRECT_AUDIO = frozenset({"aac"})
_MP4_EXT = (".mp4", ".m4v")

#: same_video tier thresholds (spec section B)
DURATION_SLACK_S = 1.0
DHASH_MAX_DISTANCE = 6
FRAMES_MIN_SHARE = 0.8

#: states whose row is not a candidate: merged already, or no file to compare
_SKIP_STATES = ("superseded", "missing")


# ----------------------------------------------------------------------- data


@dataclass(frozen=True, slots=True)
class Member:
    video_id: str
    library_id: str
    path: str
    is_copy: bool
    size_bytes: int


@dataclass
class Group:
    tier: Tier
    library_id: str | None  # None: across libraries
    members: list[Member]
    keeper_video_id: str
    reason: str
    #: per member path: what the keeper rule read (and the full hash for exact)
    scores: dict[str, dict[str, Any]] = field(default_factory=dict)
    auto: bool = False

    @property
    def key(self) -> str:
        return members_key(self.members)

    @property
    def video_ids(self) -> list[str]:
        return sorted({m.video_id for m in self.members})

    def as_json(self) -> dict[str, Any]:
        return {
            "tier": self.tier,
            "library_id": self.library_id,
            "keeper_video_id": self.keeper_video_id,
            "reason": self.reason,
            "auto": self.auto,
            "members": [
                {"video_id": m.video_id, "path": m.path, "is_copy": m.is_copy, "size": m.size_bytes}
                for m in self.members
            ],
        }


@dataclass
class ScanPlan:
    groups: list[Group] = field(default_factory=list)
    #: groups found again unchanged (open or dismissed): not read again
    known: list[str] = field(default_factory=list)
    bytes_hashed: int = 0
    #: paths a full hash could not read
    unreadable: list[str] = field(default_factory=list)
    #: a copy whose partial hash matched but whose full hash did not (not merged)
    collisions: list[str] = field(default_factory=list)
    #: (video_id, full_hash) of rows whose own file was hashed
    full_hashes: list[tuple[str, str]] = field(default_factory=list)


def members_key(members: Iterable[Member]) -> str:
    """The hash of the sorted members (``video id|path``): a group's identity."""
    items = sorted(f"{m.video_id}|{m.path}" for m in members)
    return hashlib.sha256("\n".join(items).encode()).hexdigest()


_NON_WORD = re.compile(r"[\W_]+", re.UNICODE)


def normalise_title(title: str | None) -> str:
    """Case-folded NFKC words, punctuation and spacing dropped."""
    if not title:
        return ""
    t = unicodedata.normalize("NFKC", title).casefold()
    return _NON_WORD.sub(" ", t).strip()


def parse_frames(raw: str | None) -> list[int]:
    out: list[int] = []
    for x in (raw or "").split(","):
        x = x.strip()
        if x:
            try:
                out.append(int(x, 16))
            except ValueError:
                continue
    return out


def frames_match(a: list[int], b: list[int]) -> bool:
    """dHash distance <= 6 on at least 80 % of the frames both sampled."""
    n = min(len(a), len(b))
    if n == 0:
        return False
    hits = sum(1 for i in range(n) if hamming(a[i], b[i]) <= DHASH_MAX_DISTANCE)
    return hits / n >= FRAMES_MIN_SHARE


# ----------------------------------------------------------------------- facts


@dataclass
class Facts:
    """What the keeper rule reads of one video row."""

    video_id: str
    library_id: str
    path: str
    created_at: datetime | None
    plays_directly: bool = False
    pixels: int = 0
    bitrate_kbps: int = 0
    streams: int = 0  # audio tracks + subtitle streams
    user_data: int = 0

    def as_json(self) -> dict[str, Any]:
        return {
            "plays_directly": self.plays_directly,
            "pixels": self.pixels,
            "bitrate_kbps": self.bitrate_kbps,
            "streams": self.streams,
            "user_data": self.user_data,
            "created_at": None if self.created_at is None else self.created_at.isoformat(),
            "path_len": len(self.path),
        }


def keeper_order(f: Facts) -> tuple[Any, ...]:
    """Sort key, best first: plays directly, resolution, video bitrate, audio + subtitle
    streams, user data, the oldest row, the shorter path (then the path itself)."""
    created = f.created_at or datetime.max.replace(tzinfo=UTC)
    return (
        not f.plays_directly,
        -f.pixels,
        -f.bitrate_kbps,
        -f.streams,
        -f.user_data,
        created,
        len(f.path),
        f.path,
    )


def _subtitle_streams(raw: Any) -> int:
    doc = loads(raw) if raw is not None else None
    streams = doc.get("streams") if isinstance(doc, dict) else None
    if not isinstance(streams, list):
        return 0
    return sum(1 for s in streams if isinstance(s, dict) and s.get("codec_type") == "subtitle")


def _ids_sql(n: int, start: int = 1) -> str:
    return ", ".join(f"${i}" for i in range(start, start + n))


async def load_facts(db: Any, video_ids: Iterable[str]) -> dict[str, Facts]:
    """The keeper rule's facts of these rows (``media_info``, ``audio_tracks``, user data)."""
    ids = sorted(set(video_ids))
    if not ids:
        return {}
    ph = _ids_sql(len(ids))
    rows = await db.fetch(
        "SELECT v.id AS id, v.library_id AS library_id, v.path AS path,"
        " v.created_at AS created_at, mi.container AS container,"
        " mi.video_codec AS video_codec, mi.width AS width, mi.height AS height,"
        " mi.bitrate_kbps AS bitrate_kbps, mi.raw_ffprobe AS raw_ffprobe"
        f" FROM videos v LEFT JOIN media_info mi ON mi.video_id = v.id WHERE v.id IN ({ph})",
        *ids,
    )
    audio: dict[str, list[str]] = {}
    for r in await db.fetch(
        f"SELECT video_id, codec FROM audio_tracks WHERE video_id IN ({ph})", *ids
    ):
        audio.setdefault(str(r["video_id"]), []).append(str(r["codec"] or "").lower())
    users: dict[str, int] = {}
    for table in ("playback_state", "user_playlist_items", "user_favorites"):
        for r in await db.fetch(
            f"SELECT video_id, COUNT(*) AS n FROM {table} WHERE video_id IN ({ph})"
            " GROUP BY video_id",
            *ids,
        ):
            users[str(r["video_id"])] = users.get(str(r["video_id"]), 0) + int(r["n"])
    out: dict[str, Facts] = {}
    for r in rows:
        vid = str(r["id"])
        path = str(r["path"])
        codecs = audio.get(vid, [])
        container = str(r["container"] or "").lower()
        mp4 = path.lower().endswith(_MP4_EXT) or "mp4" in container.split(",")
        direct = (
            mp4
            and str(r["video_codec"] or "").lower() in DIRECT_VIDEO
            and all(c in DIRECT_AUDIO for c in codecs)
        )
        out[vid] = Facts(
            video_id=vid,
            library_id=str(r["library_id"]),
            path=path,
            created_at=parse_ts(r["created_at"]),
            plays_directly=direct,
            pixels=int(r["width"] or 0) * int(r["height"] or 0),
            bitrate_kbps=int(r["bitrate_kbps"] or 0),
            streams=len(codecs) + _subtitle_streams(r["raw_ffprobe"]),
            user_data=users.get(vid, 0),
        )
    return out


async def auto_exact(db: Any, library_id: str | None) -> bool:
    """``libraries.settings.dedup.auto_exact`` (on unless turned off); never across
    libraries (``library_id`` None)."""
    if library_id is None:
        return False
    raw = await db.fetchval("SELECT settings FROM libraries WHERE id = $1", uid(library_id))
    settings = loads(raw) if raw is not None else None
    dedup = settings.get("dedup") if isinstance(settings, dict) else None
    value = dedup.get("auto_exact") if isinstance(dedup, dict) else None
    return value is not False


# ----------------------------------------------------------------------- scan


@dataclass(frozen=True, slots=True)
class _Row:
    id: str
    library_id: str
    path: str
    content_hash: str
    size_bytes: int
    title: str
    duration: float | None
    frames: tuple[int, ...]


async def _rows(db: Any, library_id: str | None) -> list[_Row]:
    where, args = ("v.library_id = $1", [uid(library_id)]) if library_id else ("1 = 1", [])
    skip = ", ".join(f"'{s}'" for s in _SKIP_STATES)
    rows = await db.fetch(
        "SELECT v.id AS id, v.library_id AS library_id, v.path AS path,"
        " v.content_hash AS content_hash, v.size_bytes AS size_bytes,"
        " COALESCE(v.display_title, v.title, v.filename) AS title,"
        " v.duration_sec AS duration, vs.frame_hashes AS frames"
        " FROM videos v LEFT JOIN video_signals vs ON vs.video_id = v.id"
        f" WHERE {where} AND v.state NOT IN ({skip}) AND v.superseded_by IS NULL",
        *args,
    )
    return [
        _Row(
            id=str(r["id"]),
            library_id=str(r["library_id"]),
            path=str(r["path"]),
            content_hash=str(r["content_hash"]),
            size_bytes=int(r["size_bytes"] or 0),
            title=normalise_title(_strip_ext(str(r["title"] or ""))),
            duration=None if r["duration"] is None else float(r["duration"]),
            frames=tuple(parse_frames(r["frames"])),
        )
        for r in rows
    ]


def _strip_ext(name: str) -> str:
    stem, ext = os.path.splitext(name)
    return stem if ext.lower() in {".mp4", ".m4v", ".mkv", ".webm", ".avi", ".mov", ".ts"} else name


async def _copies(db: Any, library_id: str | None) -> dict[str, list[tuple[str, int]]]:
    where, args = ("v.library_id = $1", [uid(library_id)]) if library_id else ("1 = 1", [])
    out: dict[str, list[tuple[str, int]]] = {}
    for r in await db.fetch(
        "SELECT c.video_id AS video_id, c.path AS path, c.size_bytes AS size_bytes"
        f" FROM video_copies c JOIN videos v ON v.id = c.video_id WHERE {where}"
        " ORDER BY c.path",
        *args,
    ):
        out.setdefault(str(r["video_id"]), []).append((str(r["path"]), int(r["size_bytes"])))
    return out


async def _youtube_ids(db: Any, library_id: str | None) -> dict[str, list[str]]:
    where, args = ("v.library_id = $1", [uid(library_id)]) if library_id else ("1 = 1", [])
    out: dict[str, list[str]] = {}
    for r in await db.fetch(
        "SELECT DISTINCT e.external_id AS yt, e.video_id AS video_id"
        " FROM media_metadata_enrichment e JOIN videos v ON v.id = e.video_id"
        f" WHERE {where} AND e.provider = 'youtube' AND NOT COALESCE(e.is_dismissed, false)",
        *args,
    ):
        out.setdefault(str(r["yt"]), []).append(str(r["video_id"]))
    return out


async def _known_keys(db: Any, keys: Iterable[str]) -> dict[str, str]:
    """members_key -> status of the newest group with it."""
    ks = sorted(set(keys))
    out: dict[str, str] = {}
    for i in range(0, len(ks), 500):
        part = ks[i : i + 500]
        for r in await db.fetch(
            "SELECT members_key, status, created_at FROM duplicate_groups"
            f" WHERE members_key IN ({_ids_sql(len(part))}) ORDER BY created_at",
            *part,
        ):
            out[str(r["members_key"])] = str(r["status"])
    return out


class _UnionFind:
    def __init__(self) -> None:
        self.parent: dict[str, str] = {}

    def find(self, x: str) -> str:
        self.parent.setdefault(x, x)
        while self.parent[x] != x:
            self.parent[x] = self.parent[self.parent[x]]
            x = self.parent[x]
        return x

    def union(self, a: str, b: str) -> None:
        ra, rb = self.find(a), self.find(b)
        if ra != rb:
            self.parent[max(ra, rb)] = min(ra, rb)

    def groups(self) -> list[list[str]]:
        out: dict[str, list[str]] = {}
        for x in list(self.parent):
            out.setdefault(self.find(x), []).append(x)
        return [sorted(v) for v in out.values() if len(v) > 1]


async def find_groups(
    db: Any,
    library_id: str,
    *,
    scope: Literal["library", "all"] = "library",
    to_local: ToLocal | None = None,
    progress: Progress | None = None,
    check_cancel: Callable[[], Awaitable[None]] | None = None,
) -> ScanPlan:
    """The duplicate groups of ``library_id`` (``scope="all"``: of every library, cross-
    library groups included). Reads full hashes of the exact candidates only; writes
    nothing (:func:`write_groups` does)."""
    local = to_local or paths.to_local
    lib = None if scope == "all" else str(library_id)
    rows = await _rows(db, lib)
    by_id = {r.id: r for r in rows}
    copies = await _copies(db, lib)
    plan = ScanPlan()

    # ---- exact: one content hash, two paths or more (a row's copies, other libraries)
    buckets: dict[str, list[Member]] = {}
    for r in rows:
        ms = [Member(r.id, r.library_id, r.path, False, r.size_bytes)]
        ms += [Member(r.id, r.library_id, p, True, s) for p, s in copies.get(r.id, [])]
        buckets.setdefault(r.content_hash, []).extend(ms)
    candidates = [ms for ms in buckets.values() if len(ms) > 1]
    known = await _known_keys(db, [members_key(ms) for ms in candidates])
    todo: list[list[Member]] = []
    for ms in candidates:
        status = known.get(members_key(ms))
        if status in ("open", "dismissed"):
            plan.known.append(members_key(ms))
        else:
            todo.append(ms)
    total = sum(m.size_bytes for ms in todo for m in ms)
    done = 0

    async def tick(n: int) -> None:
        nonlocal done
        done += n
        if progress is not None:
            await progress(done, total)

    exact_pairs: set[frozenset[str]] = set()
    for ms in todo:
        by_hash: dict[str, list[Member]] = {}
        hashes: dict[str, str] = {}
        for m in ms:
            if check_cancel is not None:
                await check_cancel()
            try:
                h = await asyncio.to_thread(hash_full_file, paths.find_existing(local(m.path)))
            except (OSError, ValueError):
                plan.unreadable.append(m.path)
                await tick(m.size_bytes)
                continue
            await tick(m.size_bytes)
            plan.bytes_hashed += m.size_bytes
            hashes[m.path] = h
            by_hash.setdefault(h, []).append(m)
            if not m.is_copy:
                plan.full_hashes.append((m.video_id, h))
        for h, group in by_hash.items():
            if len(group) < 2:
                continue
            libs = {m.library_id for m in group}
            g = Group(
                tier="exact",
                library_id=next(iter(libs)) if len(libs) == 1 else None,
                members=sorted(group, key=lambda m: (m.video_id, m.is_copy, m.path)),
                keeper_video_id="",
                reason="byte-identical (full BLAKE3 " + h[:16] + ")",
            )
            for m in group:
                g.scores[m.path] = {"full_hash": h}
            plan.groups.append(g)
            vids = sorted({m.video_id for m in group})
            for v1 in vids:
                for v2 in vids:
                    if v1 < v2:
                        exact_pairs.add(frozenset((v1, v2)))
        # a copy whose bytes are not its row's: the partial hash collided
        own = {m.video_id: hashes.get(m.path) for m in ms if not m.is_copy}
        for m in ms:
            if m.is_copy and m.path in hashes and own.get(m.video_id) not in (None, hashes[m.path]):
                plan.collisions.append(m.path)

    # ---- same video, other file: a YouTube id, or title + duration + frames
    uf = _UnionFind()
    why: dict[str, str] = {}

    def link(a: str, b: str, reason: str) -> None:
        if a == b or frozenset((a, b)) in exact_pairs:
            return
        if by_id[a].content_hash == by_id[b].content_hash:
            return  # exact candidates, settled above
        uf.union(a, b)
        why.setdefault(a, reason)
        why.setdefault(b, reason)

    for yt, vids in (await _youtube_ids(db, lib)).items():
        live = sorted(v for v in set(vids) if v in by_id)
        for v in live[1:]:
            link(live[0], v, f"same YouTube id {yt}")
    titled: dict[str, list[_Row]] = {}
    for r in rows:
        if r.title and r.duration is not None and r.frames:
            titled.setdefault(r.title, []).append(r)
    for same in titled.values():
        for i, a in enumerate(same):
            for b in same[i + 1 :]:
                assert a.duration is not None and b.duration is not None
                if abs(a.duration - b.duration) <= DURATION_SLACK_S and frames_match(
                    list(a.frames), list(b.frames)
                ):
                    link(a.id, b.id, "same title, duration and frames")
    for vids in uf.groups():
        ms = []
        for v in vids:
            r = by_id[v]
            ms.append(Member(r.id, r.library_id, r.path, False, r.size_bytes))
            ms += [Member(r.id, r.library_id, p, True, s) for p, s in copies.get(r.id, [])]
        libs = {m.library_id for m in ms}
        reasons = sorted({why[v] for v in vids if v in why})
        plan.groups.append(
            Group(
                tier="same_video",
                library_id=next(iter(libs)) if len(libs) == 1 else None,
                members=sorted(ms, key=lambda m: (m.video_id, m.is_copy, m.path)),
                keeper_video_id="",
                reason="; ".join(reasons),
            )
        )

    # ---- known same_video groups are left as they are; the rest get a keeper
    known_sv = await _known_keys(db, [g.key for g in plan.groups if g.tier == "same_video"])
    kept: list[Group] = []
    for g in plan.groups:
        if g.tier == "same_video" and known_sv.get(g.key) in ("open", "dismissed"):
            plan.known.append(g.key)
            continue
        kept.append(g)
    plan.groups = kept
    facts = await load_facts(db, {v for g in plan.groups for v in g.video_ids})
    for g in plan.groups:
        ranked = sorted((facts[v] for v in g.video_ids if v in facts), key=keeper_order)
        g.keeper_video_id = ranked[0].video_id if ranked else g.video_ids[0]
        for m in g.members:
            f = facts.get(m.video_id)
            s = g.scores.setdefault(m.path, {})
            if f is not None:
                s.update(f.as_json())
            s["size"] = m.size_bytes
        if g.tier == "exact":
            g.auto = await auto_exact(db, g.library_id)
    return plan


# ----------------------------------------------------------------------- write


@dataclass
class WriteReport:
    created: list[str] = field(default_factory=list)
    reopened: list[str] = field(default_factory=list)
    skipped_dismissed: int = 0
    removed_stale: int = 0
    #: the open exact groups of a library with auto_exact on: dedup_apply runs them
    auto: list[str] = field(default_factory=list)


async def write_groups(
    db: Any, plan: ScanPlan, library_id: str, *, scope: Literal["library", "all"] = "library"
) -> WriteReport:
    """Store the plan: new groups and their members, ``videos.full_hash`` of the rows
    read; a failed group found again is open again; an open group not found again (its
    members changed or went) is removed. One transaction."""
    report = WriteReport()
    stamp = to_ts(db, now())
    found = {g.key for g in plan.groups} | set(plan.known)
    async with db.transaction():
        for vid, h in plan.full_hashes:
            await db.execute("UPDATE videos SET full_hash = $2 WHERE id = $1", vid, h)
        for g in plan.groups:
            prior = await db.fetchrow(
                "SELECT id, status FROM duplicate_groups WHERE members_key = $1"
                " ORDER BY created_at DESC LIMIT 1",
                g.key,
            )
            if prior is not None and str(prior["status"]) == "dismissed":
                report.skipped_dismissed += 1
                continue
            if prior is not None and str(prior["status"]) in ("open", "failed"):
                gid = str(prior["id"])
                if str(prior["status"]) == "failed":
                    await db.execute(
                        "UPDATE duplicate_groups SET status = 'open', reason = $2 WHERE id = $1",
                        gid,
                        g.reason,
                    )
                    report.reopened.append(gid)
            else:
                gid = str(uuid.uuid4())
                await db.execute(
                    "INSERT INTO duplicate_groups (id, library_id, tier, status,"
                    " keeper_video_id, members_key, reason, created_at)"
                    " VALUES ($1, $2, $3, 'open', $4, $5, $6, $7)",
                    gid,
                    g.library_id,
                    g.tier,
                    g.keeper_video_id,
                    g.key,
                    g.reason,
                    stamp,
                )
                for m in g.members:
                    await db.execute(
                        "INSERT INTO duplicate_members (group_id, video_id, path, is_copy, score)"
                        " VALUES ($1, $2, $3, $4, $5)",
                        gid,
                        m.video_id,
                        m.path,
                        m.is_copy,
                        _json(g.scores.get(m.path, {})),
                    )
                report.created.append(gid)
            if g.auto:
                report.auto.append(gid)
        # the open groups of this scope not found again
        where = "library_id = $1" if scope == "library" else "1 = 1"
        args = [uid(library_id)] if scope == "library" else []
        for r in await db.fetch(
            f"SELECT id, members_key FROM duplicate_groups WHERE {where} AND status = 'open'",
            *args,
        ):
            if str(r["members_key"]) not in found:
                await db.execute("DELETE FROM duplicate_groups WHERE id = $1", str(r["id"]))
                report.removed_stale += 1
    return report


def _json(value: Mapping[str, Any]) -> str:
    return dumps(dict(value))
