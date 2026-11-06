"""The components CI jobs' rig (Epic 51, story 51.06): a migrated Postgres of its own, real
sleepy workers (``sleepy/worker.py``) and the queries the scenarios measure with.

Postgres comes from ``MAKTABA_TEST_PG_URL`` (a server the run may create databases on).
Every worker's log goes to ``MAKTABA_COMPONENTS_LOGS``, which the workflow keeps as an
artifact on every run. Without it (a local run) each run has a folder of its own,
``components-logs/<time>-<pid>/``, so a later run never writes over a failed one's logs
(#646); the newest :data:`KEEP_RUNS` are kept. A failed test also leaves its report there
(``conftest.py``) and the database's state (:meth:`Rig.dump_jobs`).
"""

from __future__ import annotations

import asyncio
import contextlib
import hashlib
import importlib.util
import json
import os
import signal
import subprocess
import sys
import time
import uuid
from collections.abc import AsyncIterator, Iterable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
#: the local runs' folders kept under components-logs/ (the oldest go first)
KEEP_RUNS = 20


def _logs_dir() -> Path:
    given = os.environ.get("MAKTABA_COMPONENTS_LOGS", "")
    if given:
        return Path(given)
    base = ROOT / "components-logs"
    return base / (time.strftime("%Y%m%d-%H%M%S") + f"-{os.getpid()}")


def prune_runs(base: Path, keep: int = KEEP_RUNS) -> list[Path]:
    """Remove all but the newest ``keep`` run folders under ``base``; the removed ones."""
    import shutil

    if not base.is_dir():
        return []
    runs = sorted(
        (p for p in base.iterdir() if p.is_dir() and p.name[:8].isdigit()), key=lambda p: p.name
    )
    gone = runs[: max(0, len(runs) - keep)]
    for p in gone:
        shutil.rmtree(p, ignore_errors=True)
    return gone


LOGS = _logs_dir()
WORKER = Path(__file__).resolve().parent / "sleepy" / "worker.py"


def _migrate_module() -> Any:
    path = ROOT / "pipeline" / "tests" / "engine" / "migrate.py"
    spec = importlib.util.spec_from_file_location("_components_migrate", path)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def _swap_db(url: str, name: str) -> str:
    base, _, _ = url.rpartition("/")
    return f"{base}/{name}"


@contextlib.asynccontextmanager
async def database() -> AsyncIterator[str]:
    """A freshly migrated database; yields its URL and drops it at the end."""
    import asyncpg  # type: ignore[import-untyped]

    name = "maktaba_components_" + uuid.uuid4().hex[:8]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}"')
    await admin.close()
    url = _swap_db(PG_URL, name)
    conn = await asyncpg.connect(url)
    await _migrate_module().apply_postgres(conn)
    await conn.close()
    try:
        yield url
    finally:
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()


# A port nothing listens on: the rig's workers never reach a real API.
DEAD_API_URL = "http://127.0.0.1:9"


def worker_env(base: dict[str, str], home: Path) -> dict[str, str]:
    """A rig worker's environment. The machine running the rig may be a live Maktaba home
    (its API on 127.0.0.1:8080, its agent key under ~/.maktaba): a worker gets a throwaway
    home folder, no cluster agent and an API address nothing answers, so it can read no
    key of the real home and dial no real API. Whatever ``env`` the caller passes, these
    win."""
    home.mkdir(parents=True, exist_ok=True)
    penv = dict(base)
    penv.setdefault("MAKTABA_LOG_FORMAT", "json")
    penv["HOME"] = str(home)
    penv["USERPROFILE"] = str(home)
    penv["MAKTABA_CLUSTER_AGENT"] = "0"
    penv["MAKTABA_API_URL"] = DEAD_API_URL
    penv["MAKTABA_HOME_AGENT_KEY"] = str(home / "home-agent.key")
    penv["MAKTABA_SECRETS_KEY"] = str(home / "secrets.key")
    return penv


@dataclass
class Worker:
    worker_id: str
    stages: tuple[str, ...]
    proc: subprocess.Popen[bytes]
    log: Path

    def stop(self, timeout: float = 30.0) -> None:
        if self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()


@dataclass
class Rig:
    url: str
    db: Any
    workers: list[Worker] = field(default_factory=list)

    def start(
        self,
        worker_id: str,
        stages: Iterable[str],
        *,
        sleep: float = 0.3,
        env: dict[str, str] | None = None,
    ) -> Worker:
        LOGS.mkdir(parents=True, exist_ok=True)
        log = LOGS / f"{worker_id}.log"
        st = tuple(stages)
        penv = worker_env({**os.environ, **(env or {})}, LOGS / "home" / worker_id)
        proc = subprocess.Popen(  # noqa: S603 - our own script
            [
                sys.executable,
                str(WORKER),
                "--db",
                self.url,
                "--worker-id",
                worker_id,
                "--stages",
                ",".join(st),
                "--sleep",
                str(sleep),
            ],
            stdout=log.open("ab"),
            stderr=subprocess.STDOUT,
            env=penv,
            cwd=str(ROOT / "pipeline"),
        )
        w = Worker(worker_id, st, proc, log)
        self.workers.append(w)
        return w

    def stop_all(self) -> None:
        for w in self.workers:
            w.stop()

    async def wait_online(self, n: int, timeout: float = 90.0) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            got = await self.db.fetchval(
                "SELECT count(*) FROM workers WHERE last_seen_at > now() - interval '30 seconds'"
            )
            if int(got or 0) >= n:
                return
            for w in self.workers:
                if w.proc.poll() is not None:
                    raise RuntimeError(f"worker {w.worker_id} exited: see {w.log}")
            await asyncio.sleep(0.5)
        raise TimeoutError(f"{n} workers not online after {timeout}s")

    # -- desired state ---------------------------------------------------------

    async def want(self, cid: str, want: str, *, hold: bool = False) -> float:
        """Set ``cid``'s desired row (the NOTIFY trigger tells the runners); the time."""
        await self.db.execute(
            "INSERT INTO components (id, want, hold_by, hold_at, updated_by)"
            " VALUES ($1, $2, $3, $4, 'components-ci')"
            " ON CONFLICT (id) DO UPDATE SET want = EXCLUDED.want,"
            " hold_by = EXCLUDED.hold_by, hold_at = EXCLUDED.hold_at,"
            " updated_by = EXCLUDED.updated_by, updated_at = now()",
            cid,
            want,
            "admin:ci" if hold else None,
            time_now() if hold else None,
        )
        return time.time()

    async def extension(self, name: str, enabled: bool) -> float:
        """Switch a built-in extension as PUT /api/extensions/{name} does (51.07): its
        row and its component's desired state in one transaction; the time."""
        async with self.db.transaction():
            await self.db.execute(
                "INSERT INTO extensions (name, enabled, settings, updated_at)"
                " VALUES ($1, $2, '{}'::jsonb, now())"
                " ON CONFLICT (name) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()",
                name,
                enabled,
            )
            at = await self.want(f"ext.{name}", "enabled" if enabled else "installed")
        return at

    # -- the queue -------------------------------------------------------------

    async def videos(self, n: int) -> list[str]:
        lib = str(uuid.uuid4())
        await self.db.execute(
            "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
            lib,
            f"ci-{lib[:6]}",
            ["/lib"],
        )
        ids = [str(uuid.uuid4()) for _ in range(n)]
        await self.db.executemany(
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes,"
            " mtime, duration_sec) VALUES ($1, $2, $3, $4, $5, 1000, now(), 60)",
            [
                (v, lib, hashlib.sha256(v.encode()).hexdigest(), f"/lib/{v}.mp4", f"{v}.mp4")
                for v in ids
            ],
        )
        return ids

    async def libraries(self, n: int) -> list[str]:
        ids = [str(uuid.uuid4()) for _ in range(n)]
        await self.db.executemany(
            "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
            [(i, f"ci-{i[:8]}", ["/lib"]) for i in ids],
        )
        return ids

    async def enqueue(
        self, stage: str, subjects: Iterable[str], *, kind: str = "video", priority: int = 100
    ) -> dict[str, int]:
        """Enqueue ``stage`` for each subject; the outcomes counted."""
        out: dict[str, int] = {}
        for sid in subjects:
            row = await self.db.fetchrow(
                "SELECT job_id, outcome FROM processing_jobs_enqueue("
                "$1, $2::uuid, $3, $4, false, false, $5::jsonb, NULL, NULL, NULL)",
                kind,
                sid,
                stage,
                priority,
                json.dumps({"why": "components-ci", "by": "system"}),
            )
            out[row["outcome"]] = out.get(row["outcome"], 0) + 1
        return out

    async def run_now(self, stage: str) -> None:
        """Lift the debounce of ``stage``'s pending jobs, as the queue's run-now does."""
        await self.db.execute(
            "UPDATE processing_jobs SET not_before = NULL WHERE stage = $1 AND state = 'pending'",
            stage,
        )

    async def states(self, stage: str | None = None) -> dict[str, int]:
        rows = await self.db.fetch(
            "SELECT state, count(*) AS n FROM processing_jobs"
            + (" WHERE stage = $1" if stage else "")
            + " GROUP BY state",
            *([stage] if stage else []),
        )
        return {r["state"]: int(r["n"]) for r in rows}

    async def claims_after(self, stages: Iterable[str], at: float) -> int:
        """Jobs of ``stages`` claimed after ``at`` (epoch seconds)."""
        return int(
            await self.db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE stage = ANY($1::text[])"
                " AND claimed_at > to_timestamp($2)",
                list(stages),
                at,
            )
            or 0
        )

    async def lost(self) -> int:
        """Jobs that failed or were cancelled (none may, because of a flip)."""
        return int(
            await self.db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE state IN ('failed', 'cancelled')"
            )
            or 0
        )

    async def stage_row(self, stage: str) -> tuple[bool, str | None]:
        r = await self.db.fetchrow(
            "SELECT enabled, disabled_reason FROM job_stages WHERE stage = $1", stage
        )
        return bool(r["enabled"]), r["disabled_reason"]

    async def dump_jobs(self, name: str) -> Path:
        """The whole job history (``<name>-jobs.json``) and the components' state
        (``<name>-state.json``: desired rows, status rows, stage rows), kept as an artifact
        when a scenario fails."""
        LOGS.mkdir(parents=True, exist_ok=True)
        path = LOGS / f"{name}-jobs.json"
        rows = await self.db.fetch(
            "SELECT id, stage, state, outcome, attempts, paused_reason, claimed_by,"
            " created_at, claimed_at, finished_at, payload FROM processing_jobs ORDER BY id"
        )
        path.write_text(
            json.dumps([dict(r) for r in rows], default=str, indent=1), encoding="utf-8"
        )
        state: dict[str, Any] = {}
        for key, q in (
            ("components", "SELECT * FROM components ORDER BY id"),
            ("component_status", "SELECT * FROM component_status ORDER BY machine, id, part"),
            ("component_meta", "SELECT * FROM component_meta"),
            (
                "job_stages",
                "SELECT stage, enabled, disabled_reason, paused, updated_at FROM job_stages"
                " ORDER BY stage",
            ),
            ("workers", "SELECT * FROM workers ORDER BY 1"),
        ):
            try:
                state[key] = [dict(r) for r in await self.db.fetch(q)]
            except Exception as e:  # noqa: BLE001 - keep what can be read
                state[key] = f"{type(e).__name__}: {e}"
        (LOGS / f"{name}-state.json").write_text(
            json.dumps(state, default=str, indent=1), encoding="utf-8"
        )
        return path


# -- the real API (stories 51.08, 51.10, 51.14) ---------------------------------------

#: the API binary built from the commit (the components-live job builds it: go build ./api)
API_BIN = os.environ.get("MAKTABA_COMPONENTS_API_BIN", "")
OWNER, OWNER_PASS = "owner", "components-live-owner-pass"


def _free_port() -> int:
    import socket

    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


class ApiError(Exception):
    def __init__(self, status: int, body: str) -> None:
        super().__init__(f"{status}: {body[:500]}")
        self.status = status
        self.body = body


@dataclass
class Api:
    """The real API (``maktaba-api serve``) on the rig's database, and an admin signed in
    on it as the web app is: its cookies, and the ``X-Maktaba-CSRF`` header echoing
    ``mkt_csrf`` on every mutating call (web/src/lib/api.ts)."""

    url: str
    admin_url: str
    proc: subprocess.Popen[bytes]
    log: Path
    opener: Any = None
    jar: Any = None

    def stop(self, timeout: float = 30.0) -> None:
        if self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()

    def _csrf(self) -> str:
        for c in self.jar or []:
            if c.name == "mkt_csrf":
                return str(c.value)
        return ""

    def call(self, method: str, path: str, body: Any = None) -> Any:
        """One call as the web app makes it; the decoded JSON (ApiError on a refusal)."""
        import urllib.error
        import urllib.request

        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.url + path, data=data, method=method)  # noqa: S310
        req.add_header("Accept", "application/json")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if method not in ("GET", "HEAD"):
            req.add_header("X-Maktaba-CSRF", self._csrf())
        try:
            with self.opener.open(req, timeout=10) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            raise ApiError(e.code, e.read().decode("utf-8", "replace")) from None
        return json.loads(raw) if raw else None

    def get(self, path: str) -> Any:
        return self.call("GET", path)

    def text(self, url: str) -> str:
        """A plain GET of a full URL (the admin port's /metrics)."""
        import urllib.request

        with urllib.request.urlopen(url, timeout=10) as resp:  # noqa: S310 - our own server
            return str(resp.read().decode("utf-8"))

    def sign_in(self, timeout: float = 90.0) -> None:
        import http.cookiejar
        import urllib.request

        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        deadline = time.monotonic() + timeout
        while True:
            try:
                self.call("POST", "/api/auth/login", {"username": OWNER, "password": OWNER_PASS})
                return
            except (ApiError, OSError) as e:
                if self.proc.poll() is not None:
                    raise RuntimeError(f"the API exited: see {self.log}") from e
                if time.monotonic() > deadline:
                    raise TimeoutError(f"no sign-in on the API after {timeout}s: {e}") from e
            time.sleep(0.5)

    # -- what the Components page calls (web/src/lib/componentsApi.ts) -------------

    def component(self, cid: str) -> dict[str, Any]:
        """``componentsApi.show``: the detail, health and status parts among it."""
        out: dict[str, Any] = self.get(f"/api/components/{cid}")
        return out

    def set_want(self, cid: str, want: str) -> float:
        """A toggle on the page: ``componentsApi.list`` for the row's generation,
        ``componentsApi.preview`` (nothing to confirm in these scenarios), then
        ``componentsApi.setWant``; the time the PUT was answered."""
        rows = self.get("/api/components")["components"]
        row = next(c for c in rows if c["id"] == cid)
        preview = self.call(
            "POST", "/api/components/preview", {"changes": [{"id": cid, "want": want}]}
        )
        assert not preview["refused"], preview
        self.call("PUT", f"/api/components/{cid}", {"want": want, "generation": row["generation"]})
        return time.time()


def api_env(base: dict[str, str], home: Path, url: str, port: int, admin: int) -> dict[str, str]:
    """The API's environment: ``base`` without a ``MAKTABA_*``, ``XDG_*``, ``HOME`` or
    ``DATABASE_URL`` of its own, every key and folder under ``home``, no update checks,
    no discovery, and the pipeline and streaming at a dead address."""
    env = {
        k: v
        for k, v in base.items()
        if not k.startswith(("MAKTABA_", "XDG_")) and k not in ("HOME", "DATABASE_URL")
    }
    env.update(
        {
            "HOME": str(home),
            # the rig's database URL has no query (rig.database); the CI Postgres has no TLS
            "DATABASE_URL": url if "sslmode=" in url else url + "?sslmode=disable",
            "MAKTABA_HTTP_ADDR": f"127.0.0.1:{port}",
            "MAKTABA_ADMIN_ADDR": f"127.0.0.1:{admin}",
            "MAKTABA_DATA_DIR": str(home / "data"),
            "MAKTABA_SECRETS_KEY": str(home / "secrets.key"),
            "MAKTABA_HOME_AGENT_KEY": str(home / "agent.key"),
            "MAKTABA_HOME_CLUSTER_KEY": str(home / "cluster.key"),
            "MAKTABA_NODE_CONFIG": str(home / "node.json"),
            "MAKTABA_CLUSTER_DIR": str(home / "cluster"),
            "MAKTABA_ARCHIVE_DIR": str(home / "archive"),
            "MAKTABA_STREAM_SIGNING_KEY": str(home / "stream-signing.key"),
            "MAKTABA_PIPELINE_ADDR": DEAD_API_URL.removeprefix("http://"),
            "MAKTABA_STREAMING_ADDR": DEAD_API_URL.removeprefix("http://"),
            "MAKTABA_UPDATE_DISABLE": "1",
            "MAKTABA_DISCOVERY_MDNS": "0",
            "MAKTABA_HSTS": "0",
            "MAKTABA_HEALTH_WARM": "0s",
            "MAKTABA_LOG_FORMAT": "json",
        }
    )
    return env


def start_api(url: str, name: str = "api") -> Api:
    """The API built from the commit, on the rig's database, with an admin (adduser) and
    a home of its own under the logs' ``home/<name>`` (api_env); its log ``<name>.log``."""
    if not API_BIN:
        raise RuntimeError("MAKTABA_COMPONENTS_API_BIN is not set (go build -o <bin> ./api)")
    LOGS.mkdir(parents=True, exist_ok=True)
    home = LOGS / "home" / name
    home.mkdir(parents=True, exist_ok=True)
    port, admin = _free_port(), _free_port()
    env = api_env(dict(os.environ), home, url, port, admin)
    pw = home / "owner.pw"
    pw.write_text(OWNER_PASS + "\n", encoding="utf-8")
    add = subprocess.run(  # noqa: S603 - our own binary
        [API_BIN, "adduser", "--password-file", str(pw), OWNER],
        env=env,
        capture_output=True,
        check=False,
    )
    if add.returncode != 0:
        raise RuntimeError(f"adduser: {add.stdout!r} {add.stderr!r}")
    log = LOGS / f"{name}.log"
    proc = subprocess.Popen(  # noqa: S603 - our own binary
        [API_BIN, "serve"], stdout=log.open("ab"), stderr=subprocess.STDOUT, env=env, cwd=str(home)
    )
    api = Api(f"http://127.0.0.1:{port}", f"http://127.0.0.1:{admin}", proc, log)
    try:
        api.sign_in()
    except BaseException:
        api.stop()
        raise
    return api


def time_now() -> Any:
    from datetime import UTC, datetime

    return datetime.now(UTC)


@contextlib.asynccontextmanager
async def rig() -> AsyncIterator[Rig]:
    import asyncpg

    async with database() as url:
        conn = await asyncpg.connect(url)
        r = Rig(url, conn)
        try:
            yield r
        finally:
            r.stop_all()
            await conn.close()


async def until(cond: Any, timeout: float, step: float = 0.1) -> float:
    """Wait for ``await cond()`` to be true; the seconds it took (TimeoutError after)."""
    t0 = time.monotonic()
    while True:
        if await cond():
            return time.monotonic() - t0
        if time.monotonic() - t0 > timeout:
            raise TimeoutError(f"not true after {timeout}s")
        await asyncio.sleep(step)
