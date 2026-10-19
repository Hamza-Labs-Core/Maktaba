"""The extension relay end to end, two processes (story 31.R): the real Go hub and service
door (``api/internal/cluster/agent/relayfixture``) on a scratch Postgres database, and this
process as a node — its real agent channel, the real Plex stages with the default client
(no token on the node: through the door), against a fake Plex over HTTP.

Runs with ``MAKTABA_TEST_PG_URL`` and a Go toolchain on ``PATH``; skipped otherwise.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import shutil
import subprocess
import threading
import time
import urllib.request
import uuid
from collections.abc import AsyncIterator, Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest
import pytest_asyncio

from maktaba_pipeline import relay
from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.agent.client import AgentIdentity, ChannelAgent
from maktaba_pipeline.cluster.agent.logship import LogShipper
from maktaba_pipeline.cluster.stages import lifted_placements, select_stages
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.links import get_link, get_links
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.extensions.store import load_state
from maktaba_pipeline.home_secrets import put_secret
from maktaba_pipeline.integrations.plex import PlexClient, PlexError
from maktaba_pipeline.llm.runtimes import LLMRuntimes
from maktaba_pipeline.llm.service import LocalLLM
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.engine.fixtures_db import PG_URL, _swap_db
from tests.extensions.harness import (
    FakePlex,
    add_episode,
    all_event_text,
    error_of,
    run_stage,
    set_membership,
    setup_runtime,
    summary_of,
)

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]

TOKEN = "two-process-PLEX-token-0123456789"
API_DIR = Path(__file__).resolve().parents[3] / "api"
PLEX_STAGES = {"plex_index", "plex_collection", "plex_groups"}


def _go() -> str | None:
    return shutil.which("go")


@pytest.fixture(scope="module")
def fixture_binary(tmp_path_factory: pytest.TempPathFactory) -> Path:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    go = _go()
    if go is None:
        pytest.skip("no Go toolchain on PATH")
    out = tmp_path_factory.mktemp("relayfixture") / (
        "relayfixture.exe" if os.name == "nt" else "relayfixture"
    )
    subprocess.run(  # noqa: S603 - the repo's own test fixture
        [go, "build", "-o", str(out), "./internal/cluster/agent/relayfixture"],
        cwd=API_DIR,
        check=True,
        timeout=600,
    )
    return out


@pytest_asyncio.fixture
async def pg(
    fixture_binary: Path, request: pytest.FixtureRequest
) -> AsyncIterator[tuple[Database, str]]:
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    template: str = request.getfixturevalue("pg_template")
    name = "maktaba_relay_" + uuid.uuid4().hex[:10]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{template}"')
    await admin.close()
    dsn = _swap_db(PG_URL, name)
    database = await Database.connect(dsn)
    try:
        yield database, dsn
    finally:
        await database.close()
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()


# ------------------------------------------------------------------ fake Plex over HTTP


@contextlib.contextmanager
def plex_http(fake: FakePlex) -> Iterator[tuple[str, list[str]]]:
    """``fake`` served on 127.0.0.1; yields its address and the tokens each request had."""
    tokens: list[str] = []

    class H(BaseHTTPRequestHandler):
        def log_message(self, *_: Any) -> None:
            pass

        def _do(self) -> None:
            tokens.append(self.headers.get("X-Plex-Token", ""))
            n = int(self.headers.get("Content-Length") or 0)
            body = self.rfile.read(n) if n else None
            status, out = fake.request(
                self.command, fake.base + self.path, dict(self.headers.items()), body, 10
            )
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(out)))
            self.end_headers()
            self.wfile.write(out)

        do_GET = do_POST = do_PUT = do_DELETE = _do  # noqa: N815

    srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        yield f"http://127.0.0.1:{srv.server_address[1]}", tokens
    finally:
        srv.shutdown()
        srv.server_close()


# ------------------------------------------------------------------ the home process


@contextlib.contextmanager
def home(binary: Path, dsn: str, key_file: Path, files: Path) -> Iterator[dict[str, str]]:
    if "sslmode=" not in dsn:  # lib/pq asks for TLS unless told
        dsn += ("&" if "?" in dsn else "?") + "sslmode=disable"
    env = {
        **os.environ,
        "MAKTABA_RELAY_FIXTURE_DSN": dsn,
        "MAKTABA_RELAY_FIXTURE_SECRETS_KEY": str(key_file),
        "MAKTABA_RELAY_FIXTURE_DIR": str(files),
    }
    proc = subprocess.Popen(  # noqa: S603
        [str(binary)], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
    )
    try:
        assert proc.stdout is not None
        line = proc.stdout.readline()
        assert line, "the home fixture did not start"
        yield json.loads(line)
    finally:
        if proc.stdin is not None:
            proc.stdin.close()
        try:
            proc.wait(10)
        except subprocess.TimeoutExpired:
            proc.kill()


def relay_status(url: str) -> dict[str, Any]:
    with urllib.request.urlopen(url + "/api/cluster/relay", timeout=10) as r:  # noqa: S310
        return dict(json.loads(r.read()))


async def until(cond: Any, timeout: float = 10.0) -> None:
    deadline = time.monotonic() + timeout
    while not cond():
        if time.monotonic() > deadline:
            raise AssertionError("timed out")
        await asyncio.sleep(0.05)


# ------------------------------------------------------------------ the test


async def test_plex_stages_run_on_a_node_through_the_home(
    pg: tuple[Database, str],
    fixture_binary: Path,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    db, dsn = pg
    # this process is the node: no secrets key file, so home_secrets opens nothing here
    monkeypatch.setenv("MAKTABA_SECRETS_KEY", str(tmp_path / "not-on-this-machine.key"))
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "1")
    key_file = tmp_path / "home" / "secrets.key"
    key_file.parent.mkdir()
    key = os.urandom(32)
    key_file.write_bytes(key)

    # the library, a season of three episodes, all of them in (fake) Plex
    lib = await make_library(db, "Lib")
    series, season = await make_series_and_season(db, lib, name="Show")
    fake = FakePlex()
    videos = []
    for i in range(1, 4):
        v = await make_video(db, lib, path=f"Y:/Show/e{i}.mp4", title=f"Episode {i}", duration=None)
        await add_episode(db, series, v, episode=i)
        videos.append(v)
        fake.add_item(str(100 + i), f"Episode {i}", f"/volume1/share/Show/e{i}.mp4")

    node_key = crypto.generate_keypair()
    node_id = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO cluster_nodes (id, name, state, node_pubkey, approved_at)"
        " VALUES ($1, 'mac-mini', 'approved', $2, now())",
        node_id,
        node_key.public_b64,
    )

    with plex_http(fake) as (plex_url, tokens):
        # the home's Plex settings: the address in the row, the token sealed (Python's
        # format, opened by the Go door)
        await db.execute(
            "INSERT INTO extensions (name, enabled, settings, updated_at)"
            " VALUES ('plex', true, $1, now())"
            " ON CONFLICT (name) DO UPDATE SET enabled = true, settings = EXCLUDED.settings",
            json.dumps({"url": plex_url, "path_map": "Y:/=/volume1/share/"}),
        )
        await put_secret(db, "ext.plex.token", TOKEN, key)

        with home(fixture_binary, dsn, key_file, tmp_path / "cluster") as info:
            ident = AgentIdentity(
                kind="node",
                url=info["url"],
                worker_id=f"node-{node_id[:8]}-1",
                host="mac-mini",
                role="node",
                node_id=node_id,
                node_private=node_key.private,
                home_static=crypto.b64d(info["cluster_pub"]),
            )
            agent = ChannelAgent(
                ident,
                shipper=LogShipper("info"),
                llm=LocalLLM(
                    LLMRuntimes(env={"MAKTABA_OLLAMA_URL": "", "MAKTABA_LLM_ENDPOINTS": ""})
                ),
                backoff_min=0.05,
                backoff_max=0.2,
            )
            task = asyncio.create_task(agent.run())
            relay.attach(agent)
            try:
                await until(lambda: agent.connected)
                await _node_runs_the_plex_stages(
                    db, info, fake, tokens, lib, season, series, videos
                )
                await _refusals(db, info, agent, node_id)
            finally:
                relay.detach()
                agent.stop()
                with contextlib.suppress(Exception):
                    await asyncio.wait_for(task, 5)


async def _node_runs_the_plex_stages(
    db: Database,
    info: dict[str, str],
    fake: FakePlex,
    tokens: list[str],
    lib: Any,
    season: Any,
    series: Any,
    videos: list[Any],
) -> None:
    ext = PlexExtension()  # the default client: direct with a token, else the relay
    state = await load_state(db, ext)
    assert not state.settings.get("token") and state.settings["url"].startswith("http://127.0.0.1")

    graph = build_full_graph([ext])
    lifted = await lifted_placements(db, graph)
    assert lifted >= PLEX_STAGES and "plex_livetv" not in lifted
    # the home syncs the stage registry (it has the token: the extension is configured);
    # this worker runs with what a node reads: no token
    rt = await setup_runtime(db, [ext], {"plex": {**state.settings, "token": "on-the-home"}})
    rt.extension_states = {"plex": state}
    served, off = select_stages(
        graph, role="node", lifted=lifted, handlers={s.name for s in graph if s.legacy}
    )
    assert set(served) >= PLEX_STAGES, off
    assert off.get("plex_livetv") == "home_only"

    row = await run_stage(db, rt, "plex_index", "library", lib)
    assert row["state"] == "done", error_of(row)
    assert summary_of(row)["matched"] == 3
    links = await get_links(db, "plex_item", "video", [str(v) for v in videos])
    assert [links[str(v)].external_id for v in videos] == ["101", "102", "103"]

    row = await run_stage(db, rt, "plex_collection", "season", season)
    assert row["state"] == "done" and row["outcome"] == "changed", error_of(row)
    ((first_key, col),) = fake.collections.items()
    assert col["children"] == ["101", "102", "103"] and col.get("labels") == ["maktaba-auto"]

    # a new order: the old collection is deleted (the door checks it is a collection)
    # before the new one is made
    await db.execute(
        "UPDATE series_episodes SET episode = 4 - episode WHERE series_id = $1", str(series)
    )
    await set_membership(db, season, "h1")
    row = await run_stage(db, rt, "plex_collection", "season", season)
    assert row["state"] == "done" and row["outcome"] == "changed", error_of(row)
    (col,) = fake.collections.values()
    assert first_key not in fake.collections and col["children"] == ["103", "102", "101"]
    link = await get_link(db, "plex_collection", "season", str(season))
    assert link is not None and link.external_id == next(iter(fake.collections))

    # Plex got the token on every call, from the home; it is nowhere on this side
    assert tokens and set(tokens) == {TOKEN}
    assert TOKEN not in await all_event_text(db)
    st = relay_status(info["url"])
    assert TOKEN not in json.dumps(st)
    assert st["enabled"] and st["extensions"][0]["configured"]
    methods = {c["method"] for c in st["calls"]}
    assert {"GET", "POST", "PUT", "DELETE"} <= methods
    assert all(c["node_name"] == "mac-mini" for c in st["calls"])


async def _refusals(db: Database, info: dict[str, str], agent: ChannelAgent, node_id: str) -> None:
    client = PlexClient(relay.RELAY_BASE + "plex", "", transport=relay.RelayTransport("plex"))
    # not on the allow-list: refused by the home, Plex never asked
    with pytest.raises(PlexError) as e:
        await asyncio.to_thread(client.get, "/:/prefs")
    assert e.value.status == 403
    calls = relay_status(info["url"])["calls"]
    assert calls[0]["path"] == "/:/prefs" and calls[0]["error"] == "not_allowed"

    # switched off in the settings: refused, and the stages stay on the home
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.relay', $1, now())"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        json.dumps({"enabled": False}),
    )
    graph = build_full_graph([PlexExtension()])
    assert not (PLEX_STAGES & await lifted_placements(db, graph))
    await asyncio.sleep(5.5)  # the door re-reads its settings every 5 s
    with pytest.raises(Exception, match="relay_off"):
        await asyncio.to_thread(client.get, "/identity")
    await db.execute("DELETE FROM app_settings WHERE key = 'rules.relay'")
    await asyncio.sleep(5.5)
    await asyncio.to_thread(client.get, "/identity")

    # revoked in the registry: refused at once (the hub reads the registry per call)
    await db.execute("UPDATE cluster_nodes SET state = 'revoked' WHERE id = $1", node_id)
    with pytest.raises(Exception, match="not_member"):
        await asyncio.to_thread(client.get, "/identity")
    assert relay_status(info["url"])["calls"][0]["error"] == "not_member"
