"""tv.maktaba.classify does exactly what its stages did in-process (Epic 54, stories 54.26,
54.28): a seeded channel library run through the whole graph on two copies of one database,
the wave's stages in-process on one and through the plugin on the other (the real plugin
API, its statements and its role), then every row the plugin writes, every job the queue
recorded and every log line compared.

Wave 4 (story 54.31) adds the stages that ask the language model and the encyclopedia: a
fake model server over HTTP answers both sides (the plugin's questions reach it through
the plugin API's ``compute.generate``, which forwards to ``rules.tagcheck``'s server), the
encyclopedia is a recorded answer, and the review pages are compared file by file."""

from __future__ import annotations

import json
import os
import threading
import urllib.parse
import uuid
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from tests.plugins.firstparty.harness import parity, plugin_api_dev
from tests.stages.world import make_fake_media, models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = os.path.dirname(__file__)
#: the stages wave 1 moved (story 54.28), then wave 4 (story 54.31)
STAGES = [
    "promo_links",
    "clip_groups",
    "topic_collections",
    "rate_style",
    "detect",
    "tag",
    "check_tags",
    "check_clips",
    "books",
    "review_pages",
]
#: the stages that ask the model or the encyclopedia: with both off (wave 1's library) each
#: returns before any work, so N16 times them in wave 4's test, where they do it
MODEL_STAGES = ("check_tags", "check_clips", "books")
KEYS = {
    "series": ("id",),
    "series_seasons": ("id",),
    "series_episodes": ("series_id", "video_id"),
    "season_tags": ("season_id", "tag"),
    "episode_tags": ("video_id", "tag"),
    "video_topic_tags": ("video_id", "tag"),
    "tag_checks": ("season_id",),
    "clip_checks": ("video_id",),
    "books": ("name",),
    "series_books": ("season_id",),
    "promo_links": ("video_id",),
    "clip_groups": ("id",),
    "clip_group_items": ("group_id", "video_id"),
    "topic_collections": ("id",),
    "topic_items": ("topic_id", "video_id"),
    "season_style": ("season_id",),
}


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES if not want or s in want]


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.classify selected")
    # the plugin API's dev server is built here, in setup, not in the timed call
    plugin_api_dev()
    return pg_template


class _Ids:
    """The same ids on both sides: uuid4 answers a fixed sequence while a side is seeded."""

    def __init__(self) -> None:
        import random

        self.rng = random.Random(54)

    def __call__(self) -> uuid.UUID:
        return uuid.UUID(int=self.rng.getrandbits(128), version=4)


def seeder(monkeypatch: pytest.MonkeyPatch) -> Any:
    from tests.stages.test_leftover_stages import make_catalog

    async def seed(db: Any) -> None:
        monkeypatch.setattr(uuid, "uuid4", _Ids())
        try:
            await models_off(db)
            cat = await make_catalog(db)
            await enqueue_job(db, subject_kind="library", subject_id=cat["lib"], stage="detect")
        finally:
            monkeypatch.undo()

    return seed


@pytest.mark.asyncio
async def test_classify_wave_1_is_identical(template: str, monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.plugins import firstparty

    cfg = ParityConfig.load(__import__("pathlib").Path(HERE).parent / "parity.toml")
    raw = firstparty._raw()
    async with parity(
        PG_URL,
        template,
        bundle=raw,
        stages=selected(),
        seed=seeder(monkeypatch),
        monkeypatch=monkeypatch,
    ) as p:
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in selected():
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.classify'"
        )
        assert n >= len(selected()), f"only {n} jobs went through the plugin"
        rows = await p.compare(
            [(t, KEYS[t]) for t in cfg.tables],
            ignore={k: list(v) for k, v in cfg.ignore.items()},
            perf=False,
        )
        assert rows == [], "\n".join(rows[:20])
        problems = await p.compare_n16(
            [(t, KEYS[t]) for t in cfg.tables],
            ignore={k: list(v) for k, v in cfg.ignore.items()},
            stages=[s for s in selected() if s not in MODEL_STAGES],
            perf=cfg.perf,
        )
        slowest = p.report("tv.maktaba.classify")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest


class _FakeModel:
    """An Ollama server on a loopback port: answers each chat with the model stand-in the
    stage tests use, and keeps each side's requests (the plugin's must be the same)."""

    def __init__(self) -> None:
        from tests.stages.test_model_stages import Model

        self.answer = Model(["fiqh"])
        self.asked: dict[str, list[Any]] = {"a": [], "b": []}
        self.side = "a"
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - http.server's name
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                owner.asked[owner.side].append((self.path, body))
                text = owner.answer(str(body["messages"][0]["content"]), None)
                out = json.dumps({"message": {"role": "assistant", "content": text}}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

            def log_message(self, *_a: Any) -> None:
                return None

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()


def model_seeder(url: str) -> Any:
    from tests.stages.test_model_stages import BOOK, _world
    from tests.stages.world import set_rules

    async def seed(db: Any) -> None:
        # its own patch: the test's (the encyclopedia's stand-in) stays for the run
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", _Ids())
            await set_rules(db, "tagcheck", {"url": url, "model": "parity-model"})
            await set_rules(db, "books", {"request_gap_s": 0})
            lib, _ = await _world(db, name=BOOK)
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")

    return seed


@pytest.mark.asyncio
async def test_classify_wave_4_asks_the_model_and_the_book_the_same(
    template: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """T01/T03 (story 54.31): with the model on, each question the plugin asks reaches
    ``rules.tagcheck``'s server unchanged through ``compute.generate``; the encyclopedia is
    asked only on the manifest's hosts; rows, review pages and jobs are identical."""
    from maktaba_pipeline.classify import books
    from maktaba_pipeline.plugins import firstparty
    from tests.stages.test_model_stages import _encyclopedia

    fake = _FakeModel()
    looked_up: dict[str, list[str]] = {"a": [], "b": []}
    monkeypatch.setattr(
        books, "http_get", lambda url, cfg: _encyclopedia(looked_up[fake.side])(url)
    )
    reviews = tmp_path / "reviews"
    reviews.mkdir()
    monkeypatch.setenv("MAKTABA_REVIEWS_ROOT", str(reviews))
    cfg = ParityConfig.load(Path(HERE).parent / "parity.toml")

    sides = iter(("a", "b"))  # the harness drains A, then B

    def next_side() -> None:
        fake.side = next(sides)

    try:
        async with parity(
            PG_URL,
            template,
            bundle=firstparty._raw(),
            stages=selected(),
            seed=model_seeder(fake.url),
            files={"data.review": reviews},
            monkeypatch=monkeypatch,
        ) as p:
            await p.run(before_side=next_side)
            # the channel plan pages are livetv's (in-process on both sides, and stamped with
            # the time they were made): the review pages are classify's
            for side in ("files-a", "files-b"):
                for page in (p.tmp / side / "data.review").glob("channel-plan*.html"):
                    page.unlink()
            assert any((p.tmp / "files-b" / "data.review").glob("*-*.html")), "no review page"
            assert p.a.ran == p.b.ran, "the two sides ran different jobs"
            for s in ("detect", "tag", "check_tags", "check_clips", "books", "review_pages"):
                if s in selected():
                    assert s in p.b.ran, f"{s} never ran"
            assert fake.asked["a"], "the model was never asked"
            assert fake.asked["b"] == fake.asked["a"], "the plugin asked the model otherwise"
            assert looked_up["b"] == looked_up["a"] and looked_up["a"], "the book lookups differ"
            hosts = set(firstparty.by_id("tv.maktaba.classify").trusted["net"])  # type: ignore[union-attr]
            for url in looked_up["b"]:
                assert urllib.parse.urlsplit(url).hostname in hosts, url
            assert p.fallbacks(selected()) == []
            tables = [(t, KEYS[t]) for t in cfg.tables]
            ignore = {k: list(v) for k, v in cfg.ignore.items()}
            rows = await p.compare(tables, ignore=ignore, perf=False)
            assert rows == [], "\n".join(rows[:30])
            problems = await p.compare_n16(
                tables, ignore=ignore, stages=selected(), perf=cfg.perf, timing_only=True
            )
            # its own report: wave 1's library is two shows, this one's a single show
            slowest = p.report("tv.maktaba.classify.wave4")
    finally:
        fake.close()
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
