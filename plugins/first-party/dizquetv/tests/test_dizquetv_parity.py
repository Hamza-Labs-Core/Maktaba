"""tv.maktaba.dizquetv does exactly what the dizqueTV extension's stages did in-process, and
the Plex token reaches no answer the plugin gets (issue #827).

A season's show, the planned channel (its lane, its filler list) and the channel audit, on two
copies of one database, each side with its own fake dizqueTV behind HTTP that stores what it is
sent and echoes it back. In-process on one side the worker builds the programmes with the token.
Through the plugin on the other (the ``service`` runner, which has no network), every dizqueTV
call goes through the home's service door: the plugin sends token-free records and references,
and the door builds and fills the programmes on the home and answers every read with a
projection. The writes dizqueTV received, what it holds, and the rows are compared.

The token is planted in the Plex settings and, as dizqueTV's own copy, in the fake's
plex-servers; a channel and a filler list the user made by hand carry it in their programmes.
Every answer the plugin's runner received through the door, and every setting its jobs were
handed, is searched for it: whole, URL-encoded and in parts."""

from __future__ import annotations

import json
import os
import threading
import urllib.parse
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import CATALOG_ID, bound_graph
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.store import ExtensionState
from tests.extensions.harness import FakeDizque, add_episode
from tests.plugins.firstparty.harness import SameIds, parity

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["dizquetv_show", "dizquetv_channels", "dizquetv_audit"]
TOKEN = "SECRET-PLEX-token-for-dizquetv"
#: dizqueTV's own copy of a Plex token (its plex-servers), another than the home's
DZ_TOKEN = "DZ-OWN-plex-access-token-77"
#: Plex as the home has it set up (never contacted: the programmes come from the saved index)
PLEX_URL = "http://192.168.1.4:32400"
KEYS = {
    "external_links": ("target", "subject_kind", "subject_id"),
    "channel_audit_channels": ("number",),
}
DAY = 86_400_000


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


class _Server:
    """A FakeDizque behind HTTP, so the door forwards to it as it would to dizqueTV. Keeps
    every request it received (method, path, parsed body)."""

    def __init__(self, fake: FakeDizque) -> None:
        self.fake = fake
        self.seen: list[tuple[str, str, Any]] = []
        outer = self

        class Handler(BaseHTTPRequestHandler):
            def _serve(self) -> None:
                n = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(n) if n else None
                try:
                    doc = json.loads(body) if body else None
                except ValueError:
                    doc = "<binary>"
                outer.seen.append((self.command, urllib.parse.unquote(self.path), doc))
                status, data = outer.fake.request(
                    self.command, outer.fake.base + self.path, dict(self.headers), body, 30.0
                )
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(data)

            do_GET = do_POST = do_PUT = do_DELETE = do_HEAD = _serve

            def log_message(self, *_a: Any) -> None:
                return None

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        fake.base = self.url
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def writes(self) -> list[tuple[str, str, Any]]:
        """What dizqueTV was told to change (the time-slot tool computes, it stores nothing:
        its input is the plugin's projections on one side, the bodies on the other)."""
        return [(m, p, b) for m, p, b in self.seen if m not in ("GET", "HEAD") and "time-slots" not in p]

    def close(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()


def _hand_made(token: str) -> list[dict[str, Any]]:
    """Programmes as dizqueTV's own page saves them: the token in every picture."""
    return [
        {
            "title": "Made by hand",
            "ratingKey": "901",
            "key": "/library/metadata/901",
            "duration": 600_000,
            "type": "episode",
            "icon": f"{PLEX_URL}/library/metadata/901/thumb/1?X-Plex-Token={token}",
            "episodeIcon": f"{PLEX_URL}/x?X-Plex-Token={urllib.parse.quote(token)}",
            "serverKey": "PlexServer",
        }
    ]


def _fake() -> FakeDizque:
    f = FakeDizque(server="PlexServer", server_uri=PLEX_URL, server_token=DZ_TOKEN)
    f.fillers["mine"] = {"name": "My clips", "content": _hand_made(TOKEN)}
    f.channels[9] = {
        "number": 9,
        "name": "My channel",
        "icon": f"{PLEX_URL}/photo?X-Plex-Token={TOKEN}",
        "startTime": "2026-01-01T00:00:00.000Z",
        "programs": _hand_made(DZ_TOKEN),
        "fallback": _hand_made(TOKEN),
        "fillerCollections": [{"id": "mine", "weight": 1, "cooldown": 0}],
    }
    return f


@pytest.fixture
def servers() -> Iterator[tuple[_Server, _Server]]:
    a, b = _Server(_fake()), _Server(_fake())
    yield a, b
    a.close()
    b.close()


def _detail(rk: str, title: str, file: str, duration: int) -> dict[str, Any]:
    """A Plex item as plex_index saved it (plex_items.detail)."""
    return {
        "ratingKey": rk,
        "key": f"/library/metadata/{rk}",
        "type": "episode",
        "title": title,
        "summary": "",
        "duration": duration,
        "thumb": f"/library/metadata/{rk}/thumb/1700000000",
        "grandparentTitle": "قصص",
        "grandparentThumb": "/library/metadata/100/thumb/1700000000",
        "parentIndex": 1,
        "index": int(rk) % 100,
        "year": 2024,
        "librarySectionID": "1",
        "Media": [{"Part": [{"key": f"/library/parts/{rk}/1700000000/file.mp4", "file": file}]}],
    }


def _plan(keys: list[str]) -> dict[str, Any]:
    eps = [{"plex_rating_key": k, "duration_s": 1500.0, "title": f"Episode {k}"} for k in keys]
    return {
        "planner_version": 1,
        "lanes": {"L1": {"name": "قصص", "units": [{"episodes": eps}]}},
        "filler_pools": {
            "P1": {
                "name": "Clips",
                "items": [{"plex_rating_key": "201", "duration_s": 60.0, "title": "c"}],
            }
        },
        "channels": [
            {
                "key": "ch-5",
                "number": 5,
                "exports": {
                    "dizquetv": {
                        "number": 5,
                        "name": "قناة القصص",
                        "schedule": {
                            "period": DAY,
                            "lateness": 300_000,
                            "maxDays": 365,
                            "flexPreference": "distribute",
                            "pad": 1,
                            "slots": [
                                {"time": 0, "showId": "lane:L1", "order": "next"},
                                {"time": DAY // 2, "showId": "flex.", "order": "shuffle"},
                            ],
                        },
                        "ramadan_schedule": None,
                        "fillerCollections": [{"pool": "P1", "weight": 300, "cooldown": 0}],
                        "fillerRepeatCooldown": 1000,
                    }
                },
            }
        ],
    }


def seeder() -> Any:
    async def seed(db: Any) -> None:
        import uuid

        from maktaba_pipeline.engine.sql import dumps
        from maktaba_pipeline.extensions.links import Link, put_link
        from tests.engine.factory import make_library, make_series_and_season, make_video

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(8270))
            lib = await make_library(db, "مكتبة")
            series, season = await make_series_and_season(db, lib, name="قصص")
            for i in range(1, 4):
                rk = str(100 + i)
                v = await make_video(db, lib, path=f"/lib/e{i}.mp4", title=f"الحلقة {i}")
                await add_episode(db, series, v, episode=i)
                await put_link(db, Link("plex_item", "video", str(v), rk, f"الحلقة {i}", "1:x", False))
                det = _detail(rk, f"الحلقة {i}", f"/volume1/share/e{i}.mp4", 1_500_000)
                await db.execute(
                    "INSERT INTO plex_items (server, rating_key, section_key, section_title,"
                    " library_id, video_id, file, duration_ms, detail)"
                    " VALUES ('machine-1', $1, '1', 'TV', $2, $3, $4, 1500000, $5)",
                    rk, lib, v, det["Media"][0]["Part"][0]["file"], dumps(det),
                )  # fmt: skip
            clip = _detail("201", "مقطع", "/volume1/share/clip.mp4", 60_000)
            await db.execute(
                "INSERT INTO plex_items (server, rating_key, section_key, section_title, file,"
                " duration_ms, detail) VALUES ('machine-1', '201', '1', 'TV', $1, 60000, $2)",
                clip["Media"][0]["Part"][0]["file"], dumps(clip),
            )  # fmt: skip
            await db.execute(
                "INSERT INTO channel_plans (planner_version, input_hash, plan) VALUES (1, $1, $2)",
                "plan-827", json.dumps(_plan(["101", "102", "103"])),
            )  # fmt: skip
            for ext in ("plex", "dizquetv"):
                await db.execute(
                    "INSERT INTO extensions (name, enabled, settings) VALUES ($1, true, '{}')"
                    " ON CONFLICT (name) DO UPDATE SET enabled = true",
                    ext,
                )
            # the extension enabled and configured: its stages run (the graph sync, with no
            # extension states, left them extension_disabled)
            await db.execute(
                "UPDATE job_stages SET enabled = true, disabled_reason = NULL"
                " WHERE stage LIKE 'dizquetv\\_%'"
            )
            await enqueue_job(db, subject_kind="season", subject_id=season, stage="dizquetv_show")
            await enqueue_job(
                db, subject_kind="catalog", subject_id=CATALOG_ID, stage="dizquetv_channels"
            )

    return seed


def _states(dz_url: str) -> dict[str, Any]:
    return {
        "plex": ExtensionState("plex", True, {"url": PLEX_URL, "token": TOKEN}, True),
        "dizquetv": ExtensionState("dizquetv", True, {"url": dz_url}, True),
    }


def _leak_forms(secret: str) -> list[str]:
    """Every form of a secret no answer may hold: whole, URL-encoded (both ways), and each
    part of it eight characters long."""
    forms = {secret, urllib.parse.quote(secret), urllib.parse.quote_plus(secret), secret.lower()}
    forms |= {secret[i : i + 8] for i in range(len(secret) - 7)}
    return sorted(forms)


class _Seen:
    """Every answer the plugin's runner got through the door, and every setting its jobs
    were handed (the runner runs in this process)."""

    def __init__(self, monkeypatch: pytest.MonkeyPatch) -> None:
        from maktaba_pipeline.plugins import native, service

        self.answers: list[str] = []
        self.settings: list[str] = []
        real = service.ServiceTransport.request
        outer = self

        def request(self: Any, *a: Any, **kw: Any) -> tuple[int, bytes]:
            status, body = real(self, *a, **kw)
            outer.answers.append(f"{self.service} {a[1]} -> {status} {body.decode('utf-8', 'replace')}")
            return status, body

        monkeypatch.setattr(service.ServiceTransport, "request", request)
        frame = native._service_frame

        def service_frame(stage: str, ctx: Any) -> dict[str, Any]:
            got = frame(stage, ctx)
            outer.settings.append(json.dumps(got, ensure_ascii=False))
            return got

        monkeypatch.setattr(native, "_service_frame", service_frame)


async def _run(
    template: str, servers: tuple[_Server, _Server], monkeypatch: pytest.MonkeyPatch, tmp: Path
) -> tuple[Any, _Seen]:
    from maktaba_pipeline.extensions.dizquetv import DizqueTVExtension
    from maktaba_pipeline.extensions.plex import PlexExtension
    from maktaba_pipeline.plugins import firstparty

    a_srv, b_srv = servers
    monkeypatch.setenv("MAKTABA_REVIEWS_ROOT", str(tmp / "reviews"))
    seen = _Seen(monkeypatch)
    graph = bound_graph(build_full_graph([PlexExtension(), DizqueTVExtension()]))
    ctx = parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        extra=graph,
        group="service",
        seed=seeder(),
        plex=(PLEX_URL, TOKEN),
        dizquetv=b_srv.url,
        monkeypatch=monkeypatch,
    )
    p = await ctx.__aenter__()
    try:
        p.a.extension_states = _states(a_srv.url)
        p.b.extension_states = _states(b_srv.url)
        await p.run()
    except BaseException:
        await ctx.__aexit__(None, None, None)
        raise
    return (ctx, p), seen


@pytest.mark.asyncio
async def test_dizquetv_is_identical_through_the_door(
    template: str,
    servers: tuple[_Server, _Server],
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    from maktaba_plugin_sdk.parity import ParityConfig

    a_srv, b_srv = servers
    (ctx, p), _ = await _run(template, servers, monkeypatch, tmp_path)
    try:
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in STAGES:
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.dizquetv'"
        )
        assert n >= len(STAGES), f"only {n} jobs went through the plugin"
        # dizqueTV was told the same changes, the programmes built alike (in-process by the
        # worker, through the plugin by the door), and holds the same
        assert a_srv.writes() == b_srv.writes()
        assert any(m == "PUT" and pth == "/api/show" for m, pth, _ in b_srv.writes())
        assert any(pth == "/api/channel" for _, pth, _ in b_srv.writes())
        assert a_srv.fake.shows == b_srv.fake.shows
        assert a_srv.fake.fillers == b_srv.fake.fillers
        assert a_srv.fake.channels == b_srv.fake.channels
        # the door wrote the token into every programme it built, and only there
        built = [x for d in b_srv.fake.shows.values() for x in d["content"]]
        assert built and all(
            x["icon"].endswith(f"?X-Plex-Token={TOKEN}") and x["icon"].startswith(PLEX_URL)
            for x in built
        )
        cfg = ParityConfig.load(HERE.parent / "parity.toml")
        tables = [(t, KEYS[t]) for t in cfg.tables]
        ignore = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        problems = await p.compare_n16(
            [], ignore=ignore, stages=["dizquetv_show"], perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.dizquetv")
    finally:
        await ctx.__aexit__(None, None, None)
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest


@pytest.mark.asyncio
async def test_the_plex_token_reaches_no_answer_the_plugin_gets(
    template: str,
    servers: tuple[_Server, _Server],
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    _, b_srv = servers
    (ctx, p), seen = await _run(template, servers, monkeypatch, tmp_path)
    try:
        for s in STAGES:
            assert s in p.b.ran, f"{s} never ran"
    finally:
        await ctx.__aexit__(None, None, None)
    dz_answers = [x for x in seen.answers if x.startswith("dizquetv ")]
    # the stages read everything that holds the token: the channel the user made, its filler
    # list, the Plex servers, the programmes the door built
    for want in ("/maktaba/channel/9", "/maktaba/filler/mine", "/maktaba/plex-servers"):
        assert any(want in x for x in dz_answers), f"no answer for {want}"
    assert seen.settings, "no job was handed its settings"
    # dizqueTV holds the token: it was there to leak
    assert TOKEN in json.dumps(b_srv.fake.shows) and DZ_TOKEN in json.dumps(b_srv.fake.channels)
    for where, texts in (("answer", seen.answers), ("settings", seen.settings)):
        for text in texts:
            for secret in (TOKEN, DZ_TOKEN):
                for form in _leak_forms(secret):
                    assert form not in text, f"a {where} carries {form!r}: {text[:400]}"
            assert "x-plex-token" not in text.lower(), f"a {where} names the token: {text[:400]}"
            assert "192.168.1.4" not in text, f"a {where} names Plex's address: {text[:400]}"
