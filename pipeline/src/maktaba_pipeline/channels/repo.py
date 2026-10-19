"""Database access for the channel scheduler (slots 0081/0082).

:class:`SqlRepo` is the production wiring over the pipeline connection
facade; the pass (and its tests) depend only on the :class:`ScheduleRepo`
Protocol, so a fake repo drives the planner/packer logic without a
database. SQL is parameterised with ``$N`` placeholders (the SQLite path
rewrites them at the connection-wrapper layer, the same convention as
``db/jobs.py``).

Content resolution is intentionally best-effort: it selects the library's
playable videos. Epic 26 enrichment (genre for smart-mix, season/episode
for marathon) is layered in when the columns are present; when absent the
planners degrade exactly as documented (smart-mix → shuffle, marathon →
title order). The ``smart_query`` ``source_filter`` evaluator is a future
reuse hook (Plan 27.2 §6) — until then resolution is library-scoped.
"""

from __future__ import annotations

import json
import random
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass, field, replace
from datetime import datetime
from typing import Any, Protocol
from uuid import UUID

from ..schedule.tv_settings import MaktabaTvConfig
from .base import Block, BlockKind, ChannelDef, ChannelMode, ContentItem, FillerItem
from .filler import FillerList, FillerSet

__all__ = ["ReleaseView", "ScheduleState", "ScheduleRepo", "SqlRepo", "live_gen"]


async def live_gen(db: Any) -> int:
    """The live schedule generation (``tv_routing`` scope ``cluster``, slot 0127): 0 when
    staging was never used, or on a database without the table yet."""
    try:
        v = await db.fetchval("SELECT live_gen FROM tv_routing WHERE scope = 'cluster'")
    except Exception as exc:  # noqa: BLE001 - a database before slot 0127
        if "tv_routing" not in str(exc) and "no such table" not in str(exc).lower():
            raise
        return 0
    return int(v or 0)


@dataclass(slots=True)
class ReleaseView:
    """What a generation that is not the live one plans from (Epic 33): its release's
    channel definitions (``tv_release_channels.def`` by channel id), its ``rules.tv``
    overrides, and the filler pools a pending ``write_channels`` change will write
    (``pool key -> items``, each ``(filler item id, video id, duration ms)``)."""

    defs: dict[str, dict[str, Any]]
    tv: dict[str, Any] | None = None
    pools: dict[str, list[tuple[str, str, int]]] = field(default_factory=dict)


@dataclass(slots=True)
class ScheduleState:
    """The per-channel generator cursor (slot 0082
    ``channel_schedule_state``)."""

    channel_id: UUID
    anchor_at: datetime | None = None
    horizon_until: datetime | None = None
    last_generated_at: datetime | None = None
    cursor: dict[str, Any] = field(default_factory=dict)
    stale: bool = False


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class DBConn(Protocol):
    """Minimal async connection shape (matches ``db/jobs.py``)."""

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


class ScheduleRepo(Protocol):
    """The surface the pass needs. Production is :class:`SqlRepo`; tests
    supply a fake."""

    async def load_channel(self, channel_id: UUID) -> ChannelDef | None: ...

    async def load_state(self, channel_id: UUID) -> ScheduleState: ...

    async def resolve_content(self, channel: ChannelDef) -> list[ContentItem]: ...

    async def resolve_slot_content(
        self, channel: ChannelDef
    ) -> list[list[ContentItem] | None] | None: ...

    async def load_filler(self, channel: ChannelDef) -> FillerSet | list[FillerItem]: ...

    async def load_tv(self) -> MaktabaTvConfig: ...

    async def future_programs(self, channel_id: UUID, from_at: datetime) -> list[UUID]: ...

    async def max_seq_before(self, channel_id: UUID, anchor: datetime) -> int: ...

    async def airing_until(self, channel_id: UUID, at: datetime) -> datetime | None: ...

    async def replace_future_blocks(
        self, channel_id: UUID, *, from_at: datetime, blocks: list[Block]
    ) -> None: ...

    async def save_state(self, state: ScheduleState) -> None: ...

    async def channels_needing_topup(self, now: datetime, low_water: datetime) -> list[UUID]: ...


class SqlRepo:
    """Production repo over a pipeline DB connection, for one schedule generation
    (``gen``; None: the live one, read once). A generation other than the live one
    plans from its release (``release``), not from ``channels`` / ``app_settings``."""

    def __init__(
        self, conn: DBConn, *, gen: int | None = None, release: ReleaseView | None = None
    ) -> None:
        self._c = conn
        # the latest plan's lanes, read once per repo (one top-up sweep)
        self._lane_cache: dict[str, Any] | None = None
        self._tv: MaktabaTvConfig | None = None
        self._gen = gen
        self.release = release

    async def gen(self) -> int:
        """The schedule generation this repo reads and writes."""
        if self._gen is None:
            self._gen = await live_gen(self._c)
        return self._gen

    async def load_channel(self, channel_id: UUID) -> ChannelDef | None:
        if self.release is not None:
            d = self.release.defs.get(str(channel_id))
            if d is None:
                return None
            return ChannelDef(
                id=UUID(str(d["id"])),
                mode=ChannelMode(d["mode"]),
                mode_config=_json(d.get("mode_config")),
                source_filter=_json(d.get("source_filter")) or None,
                library_id=UUID(str(d["library_id"])) if d.get("library_id") else None,
                enabled=bool(d.get("enabled", True)),
                transition=d.get("transition") or "cut",
                number=int(d.get("number") or 0),
            )
        row = await self._c.fetchrow(
            """
            SELECT id, library_id, number, mode, mode_config, source_filter, transition, enabled
            FROM channels WHERE id = $1
            """,
            channel_id,
        )
        if row is None:
            return None
        return ChannelDef(
            id=UUID(str(row["id"])),
            mode=ChannelMode(row["mode"]),
            mode_config=_json(row["mode_config"]),
            source_filter=_json(row["source_filter"]) or None,
            library_id=UUID(str(row["library_id"])) if row["library_id"] else None,
            enabled=bool(row["enabled"]),
            transition=row["transition"],
            number=int(row["number"] or 0),
        )

    async def load_state(self, channel_id: UUID) -> ScheduleState:
        row = await self._c.fetchrow(
            """
            SELECT channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale
            FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2
            """,
            channel_id,
            await self.gen(),
        )
        if row is None:
            return ScheduleState(channel_id=channel_id)
        return ScheduleState(
            channel_id=channel_id,
            anchor_at=_ts(row["anchor_at"]),
            horizon_until=_ts(row["horizon_until"]),
            last_generated_at=_ts(row["last_generated_at"]),
            cursor=_json(row["cursor"]),
            stale=bool(row["stale"]),
        )

    async def resolve_content(self, channel: ChannelDef) -> list[ContentItem]:
        if channel.library_id is not None:
            rows = await self._c.fetch(
                """
                SELECT id, title, duration_sec
                FROM videos
                WHERE library_id = $1 AND duration_sec IS NOT NULL AND duration_sec > 0
                  AND deleted_at IS NULL
                ORDER BY title ASC
                """,
                channel.library_id,
            )
        else:
            rows = await self._c.fetch(
                """
                SELECT id, title, duration_sec
                FROM videos
                WHERE duration_sec IS NOT NULL AND duration_sec > 0 AND deleted_at IS NULL
                ORDER BY title ASC
                """,
            )
        out: list[ContentItem] = []
        for r in rows:
            dur_ms = int(float(r["duration_sec"]) * 1000)
            out.append(
                ContentItem(
                    video_id=UUID(str(r["id"])),
                    duration_ms=dur_ms,
                    title=r["title"] or "",
                )
            )
        return out

    async def resolve_slot_content(
        self, channel: ChannelDef
    ) -> list[list[ContentItem] | None] | None:
        """Each slot's own content, by index: a ``lane`` source (what the TV-grid
        planner writes) is that lane's episodes, in queue order, from the latest
        channel plan (which also holds the planner's synthetic "loose" shows, so
        it, not ``series_episodes``, is the source); a ``series`` source (one a person
        picked in the channel editor) is that series' episodes in season / episode
        order; a ``collection`` source is the collection's items in their order. With
        ``order: "shuffle"`` the queue is shuffled once, the same way every time (so a
        top-up carries on the same queue). Any other slot is None (the channel-wide
        content). None when no slot has a source of its own. The Ramadan grid's slots
        follow the regular ones (the planner indexes both grids so)."""
        slots = all_slots(channel.mode_config)
        names = [slot_lane(slot) for slot in slots]
        refs = [slot_ref(slot) for slot in slots]
        wanted = sorted({n for n in names if n})
        if not wanted and not any(refs):
            return None
        queues = (
            {name: lane_episodes(lane) for name, lane in (await self._lanes(wanted)).items()}
            if wanted
            else {}
        )
        ids = sorted({str(c.video_id) for q in queues.values() for c in q})
        live = await self._live_videos(ids)  # id -> title (the plan's is the file name)
        own: dict[tuple[str, str], list[ContentItem]] = {}
        for want in {r for r in refs if r}:
            own[want] = await self._ref_items(*want)
        out: list[list[ContentItem] | None] = []
        for slot, n, ref in zip(slots, names, refs, strict=True):
            items: list[ContentItem] | None
            if n:
                items = [
                    ContentItem(c.video_id, c.duration_ms, live[str(c.video_id)] or _bare(c.title))
                    for c in queues.get(n, [])
                    if str(c.video_id) in live
                ]
            elif ref:
                items = list(own.get(ref, []))
            else:
                out.append(None)
                continue
            if slot_order(slot) == "shuffle":
                random.Random(f"{channel.id}:{n or ':'.join(ref or ())}").shuffle(items)
            out.append(items)
        return out

    async def _ref_items(self, kind: str, ref_id: str) -> list[ContentItem]:
        """A ``series`` or ``collection`` slot source's playable videos, in order."""
        try:
            UUID(ref_id)
        except ValueError:
            return []
        cast = "::uuid" if self._pg else ""
        if kind == "series":
            sql = (
                "SELECT v.id, v.title, v.duration_sec FROM series_episodes se"
                " JOIN videos v ON v.id = se.video_id"
                f" WHERE se.series_id = $1{cast} AND v.deleted_at IS NULL"
                " AND v.duration_sec IS NOT NULL AND v.duration_sec > 0"
                " ORDER BY COALESCE(se.season_override, se.season, 0),"
                " COALESCE(se.episode_override, se.episode, se.absolute_number, 0), v.title"
            )
        else:
            sql = (
                "SELECT v.id, v.title, v.duration_sec FROM collection_items ci"
                " JOIN videos v ON v.id = ci.video_id"
                f" WHERE ci.collection_id = $1{cast} AND v.deleted_at IS NULL"
                " AND v.duration_sec IS NOT NULL AND v.duration_sec > 0"
                " ORDER BY ci.position, v.title"
            )
        rows = await self._c.fetch(sql, ref_id)
        return [
            ContentItem(
                video_id=UUID(str(r["id"])),
                duration_ms=int(float(r["duration_sec"]) * 1000),
                title=r["title"] or "",
            )
            for r in rows
        ]

    @property
    def _pg(self) -> bool:
        return getattr(self._c, "dialect", "postgres") == "postgres"

    async def _lanes(self, names: list[str]) -> dict[str, Any]:
        """The named lanes of the latest plan. The plan is large (one TOASTed
        value), so the first call reads every lane any channel's slots name, in
        one pass over it, and later channels of the sweep reuse them."""
        if self._lane_cache is None:
            self._lane_cache = await self._read_lanes(await self._all_slot_lanes())
        missing = [n for n in names if n not in self._lane_cache]
        if missing:  # a lane named only since the sweep began
            self._lane_cache.update(await self._read_lanes(missing))
        return {n: self._lane_cache[n] for n in names if n in self._lane_cache}

    async def _all_slot_lanes(self) -> list[str]:
        out: set[str] = set()
        for r in await self._c.fetch("SELECT mode_config FROM channels WHERE enabled = true"):
            for slot in all_slots(_json(r["mode_config"])):
                name = slot_lane(slot)
                if name:
                    out.add(name)
        return sorted(out)

    async def _read_lanes(self, names: list[str]) -> dict[str, Any]:
        if not names:
            return {}
        if self._pg:
            # MATERIALIZED: the plan is detoasted once, not once per lane
            row = await self._c.fetchrow(
                """
                WITH l AS MATERIALIZED (
                    SELECT plan->'lanes' AS lanes FROM channel_plans ORDER BY id DESC LIMIT 1
                )
                SELECT jsonb_object_agg(k, l.lanes->k) AS lanes
                FROM l, unnest($1::text[]) AS k WHERE l.lanes ? k
                """,
                names,
            )
            return _json(row["lanes"]) if row is not None else {}
        out: dict[str, Any] = {}
        for name in names:
            path = '$.lanes."' + name.replace('"', '\\"') + '"'
            row = await self._c.fetchrow(
                "SELECT json_extract(plan, $1) AS lane FROM channel_plans ORDER BY id DESC LIMIT 1",
                path,
            )
            lane = _json(row["lane"]) if row is not None else {}
            if lane:
                out[name] = lane
        return out

    async def _live_videos(self, ids: list[str]) -> dict[str, str]:
        """The ids still present, with their titles."""
        if not ids:
            return {}
        if self._pg:
            rows = await self._c.fetch(
                "SELECT id, title FROM videos WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL",
                ids,
            )
            return {str(r["id"]): r["title"] or "" for r in rows}
        live: dict[str, str] = {}
        for i in range(0, len(ids), 500):
            part = ids[i : i + 500]
            marks = ",".join(f"${n}" for n in range(1, len(part) + 1))
            rows = await self._c.fetch(
                f"SELECT id, title FROM videos WHERE id IN ({marks}) AND deleted_at IS NULL",
                *part,
            )
            live.update((str(r["id"]), r["title"] or "") for r in rows)
        return live

    async def load_filler(self, channel: ChannelDef) -> FillerSet:
        """The channel's filler (slots 0085/0113): its weighted lists
        (``mode_config.filler.lists``: a plan pool key, or a pool id, with weight and
        cooldown), its slots' own pools (``filler_pool``), ``fill.pool`` when it has no
        lists, and any pool made for this channel by hand (weight 1)."""
        mc = channel.mode_config
        spec: dict[str, Any] = mc["filler"] if isinstance(mc.get("filler"), dict) else {}
        lists = [x for x in spec.get("lists") or [] if isinstance(x, dict) and x.get("pool")]
        fill: dict[str, Any] = mc["fill"] if isinstance(mc.get("fill"), dict) else {}
        if not lists and fill.get("pool"):
            lists = [{"pool": fill["pool"], "weight": 1, "cooldown_ms": 0}]
        slot_keys = sorted({str(x["filler_pool"]) for x in all_slots(mc) if x.get("filler_pool")})
        refs = sorted({str(x["pool"]) for x in lists} | set(slot_keys))
        pools = await self._filler_pools(refs, channel.id)
        out_lists: list[FillerList] = []
        seen: set[str] = set()
        for x in lists:
            got = pools.get(str(x["pool"]))
            if got is None or got[0] in seen:
                continue
            seen.add(got[0])
            out_lists.append(
                FillerList(
                    key=str(x["pool"]),
                    items=got[1],
                    weight=max(1, int(x.get("weight") or 1)),
                    cooldown_ms=max(0, int(x.get("cooldown_ms", x.get("cooldown")) or 0)),
                )
            )
        for ref, (pid, items) in pools.items():  # the channel's own hand-made pools
            if ref.startswith("channel:") and pid not in seen:
                seen.add(pid)
                out_lists.append(FillerList(key=pid, items=items))
        slot_pools = {k: FillerList(key=k, items=pools[k][1]) for k in slot_keys if k in pools}
        return FillerSet(
            lists=tuple(out_lists),
            pools=slot_pools,
            repeat_cooldown_ms=max(0, int(spec.get("repeat_cooldown_ms") or 0)),
        )

    async def _filler_pools(
        self, refs: list[str], channel_id: UUID
    ) -> dict[str, tuple[str, tuple[FillerItem, ...]]]:
        """``ref -> (pool id, its playable clips)`` for each ref (a plan pool key or a
        pool id) that names a pool, plus ``channel:<id>`` for the pools tied to the
        channel. A clip whose video is gone is left out."""
        rows = await self._c.fetch(
            "SELECT id, external_key, channel_id FROM filler_pools"
            " WHERE external_key IS NOT NULL OR channel_id IS NOT NULL"
        )
        want = set(refs)
        overlay = self.release.pools if self.release is not None else {}
        rows = [
            r for r in rows if r["external_key"] is None or str(r["external_key"]) not in overlay
        ]
        by_ref: dict[str, str] = {}
        for r in rows:
            pid = str(r["id"])
            key = r["external_key"]
            if key is not None and str(key) in want:
                by_ref[str(key)] = pid
            elif pid in want:
                by_ref[pid] = pid
            if r["channel_id"] is not None and str(r["channel_id"]) == str(channel_id):
                by_ref.setdefault(f"channel:{pid}", pid)
        staged = {k: v for k, v in overlay.items() if k in want}
        ids = sorted(set(by_ref.values()))
        if not ids and not staged:
            return {}
        items: dict[str, list[FillerItem]] = {pid: [] for pid in ids}
        cast = "::uuid" if self._pg else ""
        for i in range(0, len(ids), 200):
            part = ids[i : i + 200]
            marks = ",".join(f"${n}{cast}" for n in range(1, len(part) + 1))
            got = await self._c.fetch(
                "SELECT fi.id, fi.pool_id, fi.video_id, fi.type, fi.duration_ms,"
                " v.duration_sec, v.title FROM filler_items fi JOIN videos v ON v.id = fi.video_id"
                f" WHERE fi.pool_id IN ({marks}) AND v.deleted_at IS NULL"
                " ORDER BY fi.pool_id, fi.id",
                *part,
            )
            for r in got:
                ms = r["duration_ms"]
                if ms is None and r["duration_sec"] is not None:
                    ms = int(float(r["duration_sec"]) * 1000)
                if not ms or int(ms) <= 0:
                    continue
                kind = BlockKind.BUMPER if r["type"] == "bumper" else BlockKind.FILLER
                items[str(r["pool_id"])].append(
                    FillerItem(
                        filler_item_id=UUID(str(r["id"])),
                        video_id=UUID(str(r["video_id"])),
                        duration_ms=int(ms),
                        kind=kind,
                        snapshot={"title": _bare(r["title"] or ""), "duration_ms": int(ms)},
                    )
                )
        out = {ref: (pid, tuple(items[pid])) for ref, pid in by_ref.items()}
        for key, clips in staged.items():  # a pool the staged change writes (Epic 33)
            titles = await self._live_videos(sorted({v for _, v, _ in clips}))
            out[key] = (
                f"staged:{key}",
                tuple(
                    FillerItem(
                        filler_item_id=UUID(fid),
                        video_id=UUID(vid),
                        duration_ms=int(ms),
                        kind=BlockKind.FILLER,
                        snapshot={"title": _bare(titles[vid]), "duration_ms": int(ms)},
                    )
                    for fid, vid, ms in clips
                    if vid in titles and int(ms) > 0
                ),
            )
        return out

    async def load_tv(self) -> MaktabaTvConfig:
        """The ``tv`` rules, with the dizqueTV export's live broadcasts when Maktaba TV
        names none of its own (``rules.dizquetv.live`` keeps working)."""
        if self._tv is None:
            from .. import rules
            from ..schedule.tv_settings import live_slots

            if self.release is not None and self.release.tv is not None:
                tv, _ = rules.config("tv", self.release.tv)
            else:
                tv, _, _ = await rules.read(self._c, "tv")
            dz, _, _ = await rules.read(self._c, "dizquetv")
            self._tv = replace(tv, live=live_slots(tv, dz))
        return self._tv

    async def future_programs(self, channel_id: UUID, from_at: datetime) -> list[UUID]:
        rows = await self._c.fetch(
            "SELECT video_id FROM channel_programs WHERE channel_id = $1 AND start_at >= $2"
            " AND sched_gen = $3 AND kind = 'program' AND video_id IS NOT NULL ORDER BY start_at",
            channel_id,
            from_at,
            await self.gen(),
        )
        return [UUID(str(r["video_id"])) for r in rows]

    async def airing_until(self, channel_id: UUID, at: datetime) -> datetime | None:
        row = await self._c.fetchrow(
            "SELECT max(end_at) AS e FROM channel_programs"
            " WHERE channel_id = $1 AND sched_gen = $3 AND start_at <= $2 AND end_at > $2",
            channel_id,
            at,
            await self.gen(),
        )
        return None if row is None else _ts(row["e"])

    async def max_seq_before(self, channel_id: UUID, anchor: datetime) -> int:
        row = await self._c.fetchrow(
            "SELECT max(seq) AS m FROM channel_programs"
            " WHERE channel_id = $1 AND sched_gen = $3 AND start_at < $2",
            channel_id,
            anchor,
            await self.gen(),
        )
        if row is None or row["m"] is None:
            return -1
        return int(row["m"])

    async def replace_future_blocks(
        self, channel_id: UUID, *, from_at: datetime, blocks: list[Block]
    ) -> None:
        snaps = await self._display_snapshots(blocks)
        gen = await self.gen()
        async with self._c.transaction():
            # D3: only the future tail is rewritten — past/current blocks
            # (start_at < from_at) are immutable.
            await self._c.execute(
                "DELETE FROM channel_programs WHERE channel_id = $1 AND sched_gen = $3"
                " AND start_at >= $2",
                channel_id,
                from_at,
                gen,
            )
            for i, b in enumerate(blocks):
                await self._c.execute(
                    """
                    INSERT INTO channel_programs
                        (channel_id, seq, kind, video_id, filler_item_id,
                         start_at, end_at, source_offset, source_duration, title_snapshot,
                         source_url, sched_gen)
                    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
                    """,
                    channel_id,
                    b.seq,
                    b.kind.value,
                    b.video_id,
                    b.filler_item_id,
                    b.start_at,
                    b.end_at,
                    b.source_offset,
                    b.source_duration,
                    json.dumps(snaps[i]),
                    b.source_url,
                    gen,
                )

    async def _display_snapshots(self, blocks: list[Block]) -> list[dict[str, Any]]:
        """Each block's snapshot with its video's display title laid over it (Story
        27.11: the guide and the channel strip show the clean on-air titles)."""
        from ..classify.display_title import DisplayTitleConfig
        from ..rules import read as read_rules
        from .display import display_info, merge_snapshot

        cfg: DisplayTitleConfig = (await read_rules(self._c, "display_titles"))[0]
        info = await display_info(self._c, [str(b.video_id) for b in blocks if b.video_id])
        return [
            merge_snapshot(b.title_snapshot, info.get(str(b.video_id)) if b.video_id else None, cfg)
            for b in blocks
        ]

    async def save_state(self, state: ScheduleState) -> None:
        await self._c.execute(
            """
            INSERT INTO channel_schedule_state
                (channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale, sched_gen)
            VALUES ($1,$2,$3,$4,$5,false,$6)
            ON CONFLICT (channel_id, sched_gen) DO UPDATE SET
                anchor_at = EXCLUDED.anchor_at,
                horizon_until = EXCLUDED.horizon_until,
                last_generated_at = EXCLUDED.last_generated_at,
                cursor = EXCLUDED.cursor,
                stale = false
            """,
            state.channel_id,
            state.anchor_at,
            state.horizon_until,
            state.last_generated_at,
            json.dumps(state.cursor),
            await self.gen(),
        )

    async def channels_needing_topup(self, now: datetime, low_water: datetime) -> list[UUID]:
        gen = await self.gen()
        if self.release is not None:
            # a staged / previous generation: its release's enabled channels
            want = [k for k, d in self.release.defs.items() if d.get("enabled", True)]
            if not want:
                return []
            states = {
                str(r["channel_id"]): r
                for r in await self._c.fetch(
                    "SELECT channel_id, horizon_until, stale FROM channel_schedule_state"
                    " WHERE sched_gen = $1",
                    gen,
                )
            }
            out: list[UUID] = []
            for cid in sorted(want):
                st = states.get(cid)
                until = _ts(st["horizon_until"]) if st is not None else None
                if st is None or until is None or until < low_water or bool(st["stale"]):
                    out.append(UUID(cid))
            return out
        rows = await self._c.fetch(
            """
            SELECT c.id AS id
            FROM channels c
            LEFT JOIN channel_schedule_state s ON s.channel_id = c.id AND s.sched_gen = $2
            WHERE c.enabled = true AND c.lifecycle = 'live'
              AND (s.horizon_until IS NULL OR s.horizon_until < $1 OR s.stale = true)
            """,
            low_water,
            gen,
        )
        return [UUID(str(r["id"])) for r in rows]

    async def stale_channels(self) -> list[UUID]:
        """The live channels whose schedule is flagged for a regeneration (``stale``)."""
        rows = await self._c.fetch(
            "SELECT s.channel_id AS id FROM channel_schedule_state s"
            " JOIN channels c ON c.id = s.channel_id AND c.lifecycle = 'live'"
            " WHERE s.sched_gen = $1 AND s.stale = true",
            await self.gen(),
        )
        return [UUID(str(r["id"])) for r in rows]

    async def clear_stale(self, channel_id: UUID) -> None:
        """Drop the ``stale`` flag (a staged change took the regeneration over)."""
        await self._c.execute(
            "UPDATE channel_schedule_state SET stale = $3 WHERE channel_id = $1 AND sched_gen = $2",
            channel_id,
            await self.gen(),
            False if getattr(self._c, "dialect", "postgres") == "postgres" else 0,
        )


def all_slots(mode_config: dict[str, Any]) -> list[dict[str, Any]]:
    """Both grids' slots: the regular one, then the Ramadan one."""
    out = [x for x in mode_config.get("slots") or [] if isinstance(x, dict)]
    ram = mode_config.get("ramadan")
    if isinstance(ram, dict):
        out += [x for x in ram.get("slots") or [] if isinstance(x, dict)]
    return out


def slot_lane(slot: dict[str, Any]) -> str | None:
    """The plan lane a slot's ``lane`` source plays, if it has one."""
    src = slot.get("source") or {}
    if not isinstance(src, dict) or src.get("type") != "lane" or not src.get("lane"):
        return None
    return str(src["lane"])


def slot_ref(slot: dict[str, Any]) -> tuple[str, str] | None:
    """``(kind, id)`` of a slot's ``series`` / ``collection`` source, if it has one."""
    src = slot.get("source") or {}
    if not isinstance(src, dict):
        return None
    kind = src.get("type")
    key = {"series": "series_id", "collection": "collection_id"}.get(str(kind))
    if key is None or not src.get(key):
        return None
    return str(kind), str(src[key])


def slot_order(slot: dict[str, Any]) -> str:
    """How a slot plays its own queue: ``next`` (in order, the default) or ``shuffle``."""
    src = slot.get("source") or {}
    order = src.get("order") if isinstance(src, dict) else None
    return "shuffle" if order == "shuffle" else "next"


def _bare(name: str) -> str:
    """A file name as a title: without its extension (``….mp4``)."""
    stem, dot, ext = name.rpartition(".")
    return stem if dot and stem and 2 <= len(ext) <= 4 and ext.isalnum() else name


def lane_episodes(lane: dict[str, Any]) -> list[ContentItem]:
    """A plan lane's episodes in queue order: its shows one after the other. An
    episode's ``path`` in the plan is the Maktaba video id."""
    out: list[ContentItem] = []
    for unit in lane.get("units") or []:
        for ep in unit.get("episodes") or []:
            try:
                vid = UUID(str(ep.get("path")))
            except ValueError:
                continue
            ms = int(float(ep.get("duration_s") or 0) * 1000)
            if ms > 0:
                out.append(ContentItem(video_id=vid, duration_ms=ms, title=ep.get("title") or ""))
    return out


def _ts(val: Any) -> datetime | None:
    """A timestamp column as an aware datetime (SQLite hands back text)."""
    if val is None or isinstance(val, datetime):
        return val
    from ..engine.sql import parse_ts

    return parse_ts(val)


def _json(val: Any) -> dict[str, Any]:
    """Decode a JSONB/TEXT column into a dict (asyncpg may hand back a
    str on SQLite; Postgres returns the parsed value)."""
    if val is None:
        return {}
    if isinstance(val, dict):
        return val
    if isinstance(val, (str, bytes)):
        try:
            parsed = json.loads(val)
            return parsed if isinstance(parsed, dict) else {}
        except (ValueError, TypeError):
            return {}
    return {}
