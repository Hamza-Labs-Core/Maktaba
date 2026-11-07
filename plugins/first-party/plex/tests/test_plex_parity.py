"""tv.maktaba.plex does exactly what the Plex extension's stages did in-process (Epic 54,
stories 54.26, 54.35): a library indexed in Plex, its season written as a collection and its
Plex collections read back, on two copies of one database. In-process on one side the
worker's client calls Plex with the token. Through the plugin on the other (the ``service``
runner, which has no network), every call goes through the home's service door, which adds
the token and scrubs it out of the answer. Each side has its own fake Plex behind HTTP. The
calls Plex received (method, path and query, token) and the rows are compared, then N16."""

from __future__ import annotations

import os
import threading
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import bound_graph
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.store import ExtensionState
from tests.extensions.harness import FakePlex, add_episode
from tests.plugins.firstparty.harness import SameIds, parity

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["plex_index", "plex_collections_import", "plex_collection", "plex_groups"]
TOKEN = "SECRET-PLEX-TOKEN-for-parity"
KEYS = {
    "external_links": ("target", "subject_kind", "subject_id"),
    "plex_items": ("server", "rating_key"),
    "videos": ("id",),
}


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


class _Server:
    """A FakePlex behind HTTP, so the door can forward to it as it would to Plex."""

    def __init__(self, fake: Any, base: str | None = None) -> None:
        self.fake = fake
        self.base = base
        self.seen: list[tuple[str, str, str]] = []  # method, path?query, token
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def _serve(self) -> None:
                n = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(n) if n else None
                token = self.headers.get("X-Plex-Token") or ""
                outer.seen.append((self.command, self.path, token))
                base = outer.base or outer.fake.base
                status, data = outer.fake.request(
                    self.command, base + self.path, dict(self.headers), body, 30.0
                )
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            do_GET = do_POST = do_PUT = do_DELETE = _serve

            def log_message(self, *_a: Any) -> None:
                return None

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        if base is None:
            fake.base = self.url
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def close(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()


def _fake() -> FakePlex:
    f = FakePlex()
    for i in range(1, 4):
        f.add_item(str(100 + i), f"الحلقة {i}", f"/volume1/share/Show/e{i}.mp4")
    f.add_item("999", "Not ours", "/volume1/other/x.mp4")
    f.add_collection("مجموعة يدوية", ["101", "102"])  # read back by plex_collections_import
    return f


@pytest.fixture
def servers() -> Iterator[tuple[_Server, _Server]]:
    a, b = _Server(_fake()), _Server(_fake())
    yield a, b
    a.close()
    b.close()


def seeder() -> Any:
    async def seed(db: Any) -> None:
        import uuid

        from tests.engine.factory import make_library, make_series_and_season, make_video

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5446))
            lib = await make_library(db, "مكتبة Plex")
            series, _ = await make_series_and_season(db, lib, name="قصص")
            for i in range(1, 4):
                v = await make_video(
                    db, lib, path=f"Y:/Show/e{i}.mp4", title=f"الحلقة {i}", duration=None
                )
                await add_episode(db, series, v, episode=i)
            await db.execute(
                "INSERT INTO extensions (name, enabled, settings) VALUES ('plex', true, '{}')"
                " ON CONFLICT (name) DO UPDATE SET enabled = true"
            )
            # the extension enabled and configured: its stages run (the graph sync, with no
            # extension states, left them extension_disabled)
            await db.execute(
                "UPDATE job_stages SET enabled = true, disabled_reason = NULL"
                " WHERE stage LIKE 'plex\\_%'"
            )
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="plex_index")

    return seed


def _states(url: str) -> dict[str, Any]:
    settings = {"url": url, "token": TOKEN, "path_map": "Y:/=/volume1/share/"}
    return {"plex": ExtensionState("plex", True, settings, True)}


@pytest.mark.asyncio
async def test_plex_wave_8_is_identical_through_the_door(
    template: str, servers: tuple[_Server, _Server], monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.extensions.plex import PlexExtension
    from maktaba_pipeline.plugins import firstparty

    a_srv, b_srv = servers
    graph = bound_graph(build_full_graph([PlexExtension()]))
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    tables = [(t, KEYS[t]) for t in cfg.tables]
    ignore = {k: list(v) for k, v in cfg.ignore.items()}
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        extra=graph,
        group="service",
        seed=seeder(),
        plex=(b_srv.url, TOKEN),
        monkeypatch=monkeypatch,
    ) as p:
        p.a.extension_states = _states(a_srv.url)
        p.b.extension_states = _states(b_srv.url)
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in ("plex_index", "plex_collection", "plex_collections_import"):
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.plex'"
        )
        assert n >= 3, f"only {n} jobs went through the plugin"
        # Plex got the same calls, each with the token: in-process from the worker, through
        # the plugin from the door
        assert [(m, path) for m, path, _ in a_srv.seen] == [(m, path) for m, path, _ in b_srv.seen]
        assert {t for _, _, t in b_srv.seen} == {TOKEN}
        assert any(m != "GET" for m, _, _ in b_srv.seen), "nothing was written to Plex"
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        problems = await p.compare_n16(
            [], ignore=ignore, stages=["plex_index"], perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.plex")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest


# ------------------------------------------------------------------ Live TV (home only)


def livetv_seeder() -> Any:
    async def seed(db: Any) -> None:
        import uuid

        from maktaba_pipeline.engine.graph import CATALOG_ID
        from tests.extensions.test_plex_livetv import DEVICE_ID, TUNER, add_channel, set_rules

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5447))
            await db.execute(
                "INSERT INTO hdhr_device (id, device_id, device_uuid, friendly_name, tuner_count,"
                " enabled) VALUES (1, $1, $2, 'Maktaba', 4, true)",
                DEVICE_ID, str(uuid.uuid4()),
            )  # fmt: skip
            for n in (1, 2, 5):
                await add_channel(db, n)
            await add_channel(db, 9, enabled=False)  # not in the lineup
            await set_rules(db, {"tuner_url": TUNER, "auto_register": True})
            await db.execute(
                "INSERT INTO extensions (name, enabled, settings) VALUES ('plex', true, '{}')"
                " ON CONFLICT (name) DO UPDATE SET enabled = true"
            )
            await db.execute(
                "UPDATE job_stages SET enabled = true, disabled_reason = NULL"
                " WHERE stage = 'plex_livetv'"
            )
            await enqueue_job(
                db, subject_kind="catalog", subject_id=CATALOG_ID, stage="plex_livetv"
            )

    return seed


def _livetv_fake() -> Any:
    from tests.extensions.test_plex_livetv import DEVICE_ID, DIZQUE, TUNER, FakeLiveTV

    fake = FakeLiveTV(reachable={TUNER: DEVICE_ID})
    dk = fake.add_device(DIZQUE, "DIZQUE01", "dizqueTV")  # the user's own, set up by hand
    fake.add_dvr(dk, "dizqueTV", "lineup://tv.plex.providers.epg.xmltv/dz#dizqueTV")
    return fake


@pytest.mark.asyncio
async def test_plex_livetv_wave_8_is_identical_through_the_door(
    template: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Maktaba's tuner registered in Plex Live TV and its channel map pushed: Live TV's calls
    go through the door for the home's own plugin only (no node is ever allowed them)."""
    from maktaba_pipeline.extensions.plex import PlexExtension
    from maktaba_pipeline.plugins import firstparty
    from tests.extensions.test_plex_livetv import BASE, DIZQUE

    a_srv, b_srv = _Server(_livetv_fake(), BASE), _Server(_livetv_fake(), BASE)
    try:
        graph = bound_graph(build_full_graph([PlexExtension()]))

        def states(url: str) -> dict[str, Any]:
            return {
                "plex": ExtensionState("plex", True, {"url": url, "token": "T"}, True),
                "dizquetv": ExtensionState("dizquetv", True, {"url": DIZQUE}, True),
            }

        tables = [("plex_livetv_state", ("id",)), ("hdhr_device", ("id",))]
        # the times; and status.server, which names Plex's address in-process and the door
        # (service:plex) through the plugin, which never learns it (the API and the Live TV
        # page read the address from the extension's settings, never from here)
        ignore = {
            "plex_livetv_state": [
                "updated_at", "checked_at", "channelmap_at", "guide_reloaded_at",
                "status.checked_at", "status.updated_at", "status.server",
            ],
            "hdhr_device": ["created_at", "updated_at"],
        }  # fmt: skip
        async with parity(
            PG_URL,
            template,
            bundle=firstparty._raw(),
            stages=["plex_livetv"],
            extra=graph,
            group="service",
            seed=livetv_seeder(),
            plex=(b_srv.url, "T"),
            monkeypatch=monkeypatch,
        ) as p:
            p.a.extension_states = states(a_srv.url)
            p.b.extension_states = states(b_srv.url)
            await p.run()
            assert p.a.ran == p.b.ran == ["plex_livetv"], (p.a.ran, p.b.ran)
            n = await p.b.db.fetchval(
                "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.plex'"
            )
            assert n >= 1, "no job went through the plugin"
            writes = [(m, path) for m, path, _ in a_srv.seen if m != "GET"]
            assert any(path.startswith("/media/grabbers/devices") for _, path in writes), writes
            assert [(m, path) for m, path, _ in a_srv.seen] == [
                (m, path) for m, path, _ in b_srv.seen
            ]
            assert {t for _, _, t in b_srv.seen} == {"T"}
            rows = await p.compare(tables, ignore=ignore, perf=False)
            assert rows == [], "\n".join(rows[:30])
            problems = await p.compare_n16(
                [], ignore=ignore, stages=["plex_livetv"], timing_only=True
            )
            slowest = p.report("tv.maktaba.plex.livetv")
    finally:
        a_srv.close()
        b_srv.close()
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
