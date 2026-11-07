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

    def __init__(self, fake: FakePlex) -> None:
        self.fake = fake
        self.seen: list[tuple[str, str, str]] = []  # method, path?query, token
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def _serve(self) -> None:
                n = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(n) if n else None
                token = self.headers.get("X-Plex-Token") or ""
                outer.seen.append((self.command, self.path, token))
                status, data = outer.fake.request(
                    self.command, outer.fake.base + self.path, dict(self.headers), body, 30.0
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
