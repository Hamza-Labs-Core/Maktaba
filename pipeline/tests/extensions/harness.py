"""Test harness for the extension and stage tests: runtime setup, a one-job runner, fakes."""

from __future__ import annotations

import json
import urllib.parse
import uuid
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, StageDef
from maktaba_pipeline.engine.loader import build_full_graph, register_extension_impls
from maktaba_pipeline.engine.sql import dumps, loads
from maktaba_pipeline.engine.stages.registry import reset_registry
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.extensions.base import Extension
from maktaba_pipeline.extensions.store import ExtensionState
from maktaba_pipeline.integrations.plex import TransportError


async def setup_runtime(
    db: Any,
    extensions: Sequence[Extension],
    settings: Mapping[str, Mapping[str, str]] | None = None,
    *,
    enabled: bool = True,
) -> EngineRuntime:
    """Build the full graph, register implementations, sync ``job_stages``."""
    settings = settings or {}
    reset_registry()
    graph: tuple[StageDef, ...] = build_full_graph(extensions) if extensions else GRAPH
    register_extension_impls(extensions)
    states = {
        e.manifest.name: ExtensionState(
            e.manifest.name,
            enabled,
            dict(settings.get(e.manifest.name, {})),
            e.is_configured(settings.get(e.manifest.name, {})),
        )
        for e in extensions
    }
    await sync_graph(db, graph, states)
    return EngineRuntime(graph={s.name: s for s in graph}, extension_states=states)


async def run_stage(
    db: Any,
    rt: EngineRuntime,
    stage: str,
    kind: str,
    subject_id: UUID,
    *,
    force: bool = False,
    payload: dict[str, Any] | None = None,
) -> Any:
    """Enqueue one job, claim it and dispatch it; returns the finished ``processing_jobs`` row."""
    res = await enqueue_job(
        db, subject_kind=kind, subject_id=subject_id, stage=stage, force=force, payload=payload
    )
    assert res.job_id is not None, res.outcome
    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE id = $1", res.job_id)
    job = await claim_for_pool(db, worker_id="w1", pool=rt.graph[stage].pool, stages=[stage])
    assert job is not None and job.id == res.job_id
    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    return await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", res.job_id)


def error_of(row: Any) -> dict[str, Any]:
    return dict(loads(row["error"]) or {})


def summary_of(row: Any) -> dict[str, Any]:
    return dict(loads(row["output_summary"]) or {})


async def events(db: Any, job_id: int, kind: str) -> list[dict[str, Any]]:
    rows = await db.fetch(
        "SELECT data FROM job_events WHERE job_id = $1 AND kind = $2 ORDER BY id", job_id, kind
    )
    return [dict(loads(r["data"]) or {}) for r in rows]


async def all_event_text(db: Any) -> str:
    rows = await db.fetch("SELECT message, data FROM job_events")
    summaries = await db.fetch("SELECT output_summary, error FROM processing_jobs")
    return json.dumps(
        [[str(r["message"]), str(r["data"])] for r in rows]
        + [[str(r["output_summary"]), str(r["error"])] for r in summaries]
    )


async def add_episode(
    db: Any, series_id: UUID, video_id: UUID, *, season: int = 1, episode: int | None = None
) -> None:
    await db.execute(
        "INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1,$2,$3,$4)",
        str(series_id),
        str(video_id),
        season,
        episode,
    )


async def set_membership(db: Any, season_id: UUID, membership_hash: str) -> None:
    await db.execute(
        "UPDATE series_seasons SET membership_hash = $2 WHERE id = $1",
        str(season_id),
        membership_hash,
    )


def new_id() -> UUID:
    return uuid.uuid4()


# ------------------------------------------------------------------ fake Plex


def _meta(rk: str, item: Mapping[str, Any]) -> dict[str, Any]:
    return {
        "ratingKey": rk,
        "key": f"/library/metadata/{rk}",
        "title": item["title"],
        "type": "episode",
        "duration": item.get("duration", 1_500_000),
        "librarySectionID": item["section"],
        "grandparentTitle": "Show",
        "parentIndex": 1,
        "index": 1,
        "summary": "",
        "Media": [{"Part": [{"key": f"/library/parts/{rk}/file", "file": item["file"]}]}],
    }


@dataclass
class FakePlex:
    """A Plex Media Server in a dict; implements the client's ``Transport``."""

    base: str = "http://plex.test"
    sections: dict[str, str] = field(default_factory=lambda: {"1": "TV"})
    #: rating key -> {title, file, section}
    items: dict[str, dict[str, Any]] = field(default_factory=dict)
    #: rating key -> {title, section, children, smart}
    collections: dict[str, dict[str, Any]] = field(default_factory=dict)
    calls: list[tuple[str, str]] = field(default_factory=list)
    token_seen: set[str] = field(default_factory=set)
    #: drop the last child of every collection built through the API (read-back mismatch)
    corrupt: bool = False
    down: bool = False
    #: items Plex answers 400 for when they are added to a collection
    refuse: set[str] = field(default_factory=set)
    _next: int = 9000

    def add_item(self, rk: str, title: str, file: str, section: str = "1") -> None:
        self.items[rk] = {"title": title, "file": file, "section": section}

    def add_collection(self, title: str, children: list[str], section: str = "1") -> str:
        self._next += 1
        rk = str(self._next)
        self.collections[rk] = {
            "title": title,
            "section": section,
            "children": list(children),
            "smart": False,
        }
        return rk

    @property
    def writes(self) -> list[tuple[str, str]]:
        return [c for c in self.calls if c[0] != "GET"]

    def titles(self) -> list[str]:
        return sorted(c["title"] for c in self.collections.values())

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        if self.down:
            raise TransportError("connection refused")
        if token := headers.get("X-Plex-Token"):
            self.token_seen.add(token)
        assert url.startswith(self.base), url
        path, _, query = url[len(self.base) :].partition("?")
        q = {k: v[0] for k, v in urllib.parse.parse_qs(query).items()}
        self.calls.append((method, path))
        adding = method in ("POST", "PUT") and (
            path == "/library/collections" or path.endswith("/items")
        )
        if adding and self.refuse & set(q.get("uri", "").rsplit("/", 1)[-1].split(",")):
            return 400, b""
        out = self._route(method, path, q)
        return (200, json.dumps(out).encode()) if out is not None else (404, b"")

    def _route(self, method: str, path: str, q: dict[str, str]) -> dict[str, Any] | None:  # noqa: C901
        parts = path.strip("/").split("/")
        if path == "/identity":
            return {"MediaContainer": {"machineIdentifier": "machine-1"}}
        if path == "/library/sections":
            return {
                "MediaContainer": {
                    "Directory": [
                        {"key": k, "title": t, "type": "show", "Location": [{"path": "/x"}]}
                        for k, t in self.sections.items()
                    ]
                }
            }
        if (
            parts[:2] == ["library", "sections"]
            and len(parts) == 4
            and parts[3] == "all"
            and method == "PUT"
            and q.get("type") == "18"
        ):  # a label on a collection
            c = self.collections.get(q["id"])
            if c is None:
                return None
            c.setdefault("labels", []).append(q["label[0].tag.tag"])
            return {}
        if parts[:2] == ["library", "sections"] and len(parts) == 4 and parts[3] == "all":
            # the listing carries the full item record, as real Plex's does
            md = [_meta(rk, it) for rk, it in self.items.items() if it["section"] == parts[2]]
            return {"MediaContainer": {"Metadata": md}}
        if parts[:2] == ["library", "sections"] and len(parts) == 4 and parts[3] == "collections":
            md = [
                {
                    "ratingKey": rk,
                    "title": c["title"],
                    "smart": c["smart"],
                    "childCount": len(c["children"]),
                }
                for rk, c in self.collections.items()
                if c["section"] == parts[2]
            ]
            return {"MediaContainer": {"Metadata": md}}
        if parts[:2] == ["library", "collections"] and len(parts) == 4 and parts[3] == "children":
            c = self.collections.get(parts[2])
            if c is None:
                return None
            kids = c["children"][:-1] if self.corrupt and c["children"] else c["children"]
            return {"MediaContainer": {"Metadata": [{"ratingKey": k} for k in kids]}}
        if parts[:2] == ["library", "metadata"] and len(parts) == 3 and method == "GET":
            rks = parts[2].split(",")
            md = [_meta(rk, self.items[rk]) for rk in rks if rk in self.items]
            for rk in rks:
                if rk in self.collections:
                    md.append(
                        {"ratingKey": rk, "librarySectionID": self.collections[rk]["section"]}
                    )
            return {"MediaContainer": {"Metadata": md}}
        if parts[:2] == ["library", "metadata"] and len(parts) == 3 and method == "DELETE":
            self.collections.pop(parts[2], None)
            return {}
        if path == "/library/collections" and method == "POST":
            self._next += 1
            rk = str(self._next)
            first = q["uri"].rsplit("/", 1)[-1].split(",")
            self.collections[rk] = {
                "title": q["title"],
                "section": q["sectionId"],
                "children": list(first),
                "smart": False,
            }
            return {"MediaContainer": {"Metadata": [{"ratingKey": rk}]}}
        if parts[:2] == ["library", "collections"] and len(parts) == 4 and parts[3] == "items":
            c = self.collections[parts[2]]
            c["children"].extend(q["uri"].rsplit("/", 1)[-1].split(","))
            return {}
        if parts[:2] == ["library", "collections"] and len(parts) == 6 and parts[5] == "move":
            c = self.collections[parts[2]]
            cur = parts[4]
            c["children"].remove(cur)
            if "after" in q:
                c["children"].insert(c["children"].index(q["after"]) + 1, cur)
            else:  # no "after": to the front
                c["children"].insert(0, cur)
            return {}
        if parts[:2] == ["library", "metadata"] and parts[3:] == ["prefs"]:
            return {}
        return None


# ------------------------------------------------------------------ fake dizqueTV


@dataclass
class FakeDizque:
    base: str = "http://dz.test"
    shows: dict[str, dict[str, Any]] = field(default_factory=dict)
    fillers: dict[str, dict[str, Any]] = field(default_factory=dict)
    channels: dict[int, dict[str, Any]] = field(default_factory=dict)
    server: str | None = "PlexServer"
    calls: list[tuple[str, str]] = field(default_factory=list)
    #: GET /api/show/ID returns one program fewer than was saved
    corrupt: bool = False
    down: bool = False
    _next: int = 0

    @property
    def writes(self) -> list[tuple[str, str]]:
        return [c for c in self.calls if c[0] != "GET" and "time-slots" not in c[1]]

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        if self.down:
            raise TransportError("connection refused")
        assert url.startswith(self.base), url
        path = urllib.parse.unquote(url[len(self.base) :])
        doc = json.loads(body) if body else None
        self.calls.append((method, path))
        out = self._route(method, path, doc)
        if out is _NOT_FOUND:
            return 404, b""
        return 200, json.dumps(out, ensure_ascii=False).encode()

    def _new_id(self) -> str:
        self._next += 1
        return f"id{self._next}"

    def _route(self, method: str, path: str, doc: Any) -> Any:  # noqa: C901
        parts = path.strip("/").split("/")
        if path == "/api/channels":
            return sorted(self.channels)
        if path.startswith("/api/channel/programless/"):
            ch = self.channels.get(int(parts[-1]))
            return _NOT_FOUND if ch is None else {k: v for k, v in ch.items() if k != "programs"}
        if path == "/api/plex-servers":
            return [] if self.server is None else [{"name": self.server}]
        if path == "/api/channel-tools/time-slots":
            return {"programs": doc["programs"], "startTime": "2026-01-01T00:00:00.000Z"}
        if path == "/api/channel" and method in ("POST", "PUT"):
            self.channels[int(doc["number"])] = doc
            return {}
        for kind, store in (("show", self.shows), ("filler", self.fillers)):
            if path == f"/api/{kind}s":
                return [{"id": i, "name": d["name"]} for i, d in store.items()]
            if path == f"/api/{kind}" and method == "PUT":
                i = self._new_id()
                store[i] = dict(doc)
                return {"id": i}
            if len(parts) == 3 and parts[:2] == ["api", kind]:
                i = parts[2]
                if method == "GET":
                    if i not in store:
                        return _NOT_FOUND
                    d = dict(store[i])
                    if self.corrupt and d.get("content"):
                        d["content"] = d["content"][:-1]
                    return {"id": i, **d}
                if method == "POST":
                    store[i] = dict(doc)
                    return {}
                if method == "DELETE":
                    store.pop(i, None)
                    return {}
        return _NOT_FOUND


_NOT_FOUND = object()


def program_keys(content: Sequence[Mapping[str, Any]]) -> list[str]:
    return [str(p["ratingKey"]) for p in content]


__all__ = [
    "FakeDizque",
    "FakePlex",
    "add_episode",
    "all_event_text",
    "dumps",
    "error_of",
    "events",
    "new_id",
    "program_keys",
    "run_stage",
    "set_membership",
    "setup_runtime",
    "summary_of",
]
