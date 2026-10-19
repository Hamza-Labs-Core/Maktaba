"""Checks for the cluster e2e harness (run by run.sh with the harness's pipeline venv).

Reads the run's context.json (written by run.sh) and drives the simulated cluster: a home
(API + its own worker) and two joined machines, A and B, each a separate pipeline
process with its own node config, HOME and path to the share.

Phase 1 (multi-instance, merged):
  1. both machines registered: GET /api/workers shows their workers as role=node with
     the machine's name; the home's worker as role=home
  2. each machine inferred its own path map to the share (different local paths), and
     its storage check reads and writes
  3. home-only queues: asked for with --stages, still refused (stages_off = home_only),
     and never pulled by a machine; the home's worker pulls them
  4. a stage switched off for a machine: it stops pulling from that queue (latency
     measured); the other machine pulls everything; then the other way round
  5. revoke: the machine's process exits and its database login is refused, the other
     machine keeps working
Phase 2 (the cluster channel; each check skips with a clear message when its endpoint
is absent, since it is being built in parallel; E2E_REQUIRE_PHASE2=1 turns the skips
into failures):
  4b. a switch reaches the machine at once (the hub's reload_stages, <= E2E_SWITCH_FAST)
  6. the WebSocket hub at /api/cluster/ws takes a subscription and pushes `job` and
     `queues` messages while jobs run
  7. every machine's files appear on the share under maktaba/cluster/<ip>/{logs,metrics,
     jobs}/<date>/<worker>.jsonl, and the hub's queues/<date>/hub.jsonl
  8. an LLM request through /api/cluster/llm/v1/chat/completions (plain and streamed) is
     served by machine B (the one with a runtime: a fake Ollama), not by A
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import contextlib
import glob
import hashlib
import json
import os
import socket
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

try:
    import asyncpg  # type: ignore[import-untyped]
except ImportError:  # pragma: no cover - run.sh uses the pipeline venv
    sys.exit("check.py needs asyncpg: run it with the pipeline venv's python (run.sh does)")

HOME_ONLY = ("thumb_small", "review_pages", "index")
LIVE_STATES = ("pending", "claimed", "running", "resuming", "paused")
SWITCH_TIMEOUT = float(os.environ.get("E2E_SWITCH_TIMEOUT", "75"))
BATCH_TIMEOUT = float(os.environ.get("E2E_BATCH_TIMEOUT", "120"))
REVOKE_TIMEOUT = float(os.environ.get("E2E_REVOKE_TIMEOUT", "60"))
FILES_TIMEOUT = float(os.environ.get("E2E_FILES_TIMEOUT", "45"))
LLM_TIMEOUT = float(os.environ.get("E2E_LLM_TIMEOUT", "75"))
#: phase 2: how fast an admin's switch must reach a machine (the hub's reload_stages)
SWITCH_FAST = float(os.environ.get("E2E_SWITCH_FAST", "10"))
#: once phase 2 is merged: a missing phase-2 endpoint fails instead of skipping
REQUIRE_PHASE2 = os.environ.get("E2E_REQUIRE_PHASE2", "0") not in ("", "0", "false", "no")


def say(msg: str) -> None:
    print(f"[check {time.strftime('%H:%M:%S')}] {msg}", flush=True)


# --------------------------------------------------------------------------- results


@dataclass
class Results:
    rows: list[tuple[str, str, str]] = field(default_factory=list)

    def add(self, status: str, name: str, detail: str = "") -> None:
        self.rows.append((status, name, detail))
        mark = {"PASS": "PASS", "FAIL": "FAIL", "SKIP": "SKIP"}[status]
        print(f"  {mark}  {name}" + (f" -- {detail}" if detail else ""), flush=True)

    def ok(self, name: str, detail: str = "") -> None:
        self.add("PASS", name, detail)

    def fail(self, name: str, detail: str = "") -> None:
        self.add("FAIL", name, detail)

    def skip(self, name: str, detail: str = "") -> None:
        self.add("SKIP", name, detail)

    def absent(self, name: str, detail: str) -> None:
        """A phase-2 feature this build does not have: skipped, or failed when required."""
        if REQUIRE_PHASE2:
            self.fail(name, detail + " (E2E_REQUIRE_PHASE2 is set)")
        else:
            self.skip(name, detail)

    def check(self, cond: bool, name: str, ok: str = "", bad: str = "") -> bool:
        if cond:
            self.ok(name, ok)
        else:
            self.fail(name, bad or ok)
        return cond

    @property
    def failed(self) -> int:
        return sum(1 for s, _, _ in self.rows if s == "FAIL")

    def summary(self) -> str:
        c = Counter(s for s, _, _ in self.rows)
        return f"{c['PASS']} passed, {c['FAIL']} failed, {c['SKIP']} skipped"


class Abort(Exception):
    """A check failed in a way that makes the rest meaningless."""


# ------------------------------------------------------------------------------ http

_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


@dataclass
class Resp:
    status: int
    body: bytes
    headers: dict[str, str]

    def json(self) -> Any:
        try:
            return json.loads(self.body or b"null")
        except json.JSONDecodeError:
            return None

    def text(self) -> str:
        return self.body.decode("utf-8", "replace")


class Api:
    def __init__(self, base: str, token: str) -> None:
        self.base = base.rstrip("/")
        self.token = token

    def call(self, method: str, path: str, body: Any = None, *, timeout: float = 15) -> Resp:
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.token}")
        req.add_header("Accept", "application/json")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with _OPENER.open(req, timeout=timeout) as r:
                return Resp(r.status, r.read(), dict(r.headers))
        except urllib.error.HTTPError as e:
            return Resp(e.code, e.read(), dict(e.headers or {}))
        except (urllib.error.URLError, OSError) as e:
            return Resp(0, str(e).encode(), {})

    def get(self, path: str) -> Resp:
        return self.call("GET", path)

    def exists(self, path: str, method: str = "GET") -> bool:
        """A route is there unless the router answers 404 (chi's not-found)."""
        r = self.call(method, path, {} if method == "POST" else None, timeout=10)
        return r.status not in (0, 404)


# ------------------------------------------------------------------- tiny websocket


class WSError(Exception):
    pass


class WebSocket:
    """A minimal RFC 6455 client (text frames, ping/pong, close), stdlib only."""

    GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

    def __init__(self, url: str, headers: dict[str, str], timeout: float = 10) -> None:
        u = urllib.parse.urlsplit(url)
        port = u.port or (443 if u.scheme == "wss" else 80)
        self.sock = socket.create_connection((u.hostname or "127.0.0.1", port), timeout=timeout)
        key = base64.b64encode(os.urandom(16)).decode()
        path = (u.path or "/") + (f"?{u.query}" if u.query else "")
        lines = [
            f"GET {path} HTTP/1.1",
            f"Host: {u.hostname}:{port}",
            "Upgrade: websocket",
            "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}",
            "Sec-WebSocket-Version: 13",
            *(f"{k}: {v}" for k, v in headers.items()),
        ]
        self.sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
        raw = b""
        while b"\r\n\r\n" not in raw:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise WSError("connection closed during the handshake")
            raw += chunk
        head, _, self.buf = raw.partition(b"\r\n\r\n")
        status_line, *header_lines = head.decode("latin-1").split("\r\n")
        self.status = int(status_line.split()[1])
        hdrs = {
            h.split(":", 1)[0].strip().lower(): h.split(":", 1)[1].strip() for h in header_lines if ":" in h
        }
        if self.status != 101:
            raise WSError(f"handshake answered {status_line}")
        want = base64.b64encode(hashlib.sha1((key + self.GUID).encode()).digest()).decode()
        if hdrs.get("sec-websocket-accept") != want:
            raise WSError("bad Sec-WebSocket-Accept")
        self.closed = False
        self._send_lock = threading.Lock()

    def _recv_exact(self, n: int) -> bytes:
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise ConnectionError("closed")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def _send_frame(self, opcode: int, payload: bytes) -> None:
        head = bytes([0x80 | opcode])
        n = len(payload)
        if n < 126:
            head += bytes([0x80 | n])
        elif n < 1 << 16:
            head += bytes([0x80 | 126]) + struct.pack("!H", n)
        else:
            head += bytes([0x80 | 127]) + struct.pack("!Q", n)
        mask = os.urandom(4)
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        with self._send_lock:
            self.sock.sendall(head + mask + masked)

    def send_json(self, obj: Any) -> None:
        self._send_frame(0x1, json.dumps(obj).encode())

    def recv(self) -> str | None:
        """The next text message; None when the peer closed. Raises socket.timeout."""
        parts: list[bytes] = []
        while True:
            b0, b1 = self._recv_exact(2)
            fin, opcode = b0 & 0x80, b0 & 0x0F
            n = b1 & 0x7F
            if n == 126:
                n = struct.unpack("!H", self._recv_exact(2))[0]
            elif n == 127:
                n = struct.unpack("!Q", self._recv_exact(8))[0]
            mask = self._recv_exact(4) if b1 & 0x80 else b""
            payload = self._recv_exact(n)
            if mask:
                payload = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
            if opcode == 0x8:
                self.closed = True
                with contextlib.suppress(OSError):
                    self._send_frame(0x8, payload[:2])
                return None
            if opcode == 0x9:
                self._send_frame(0xA, payload)
                continue
            if opcode == 0xA:
                continue
            parts.append(payload)
            if fin:
                return b"".join(parts).decode("utf-8", "replace")

    def close(self) -> None:
        with contextlib.suppress(OSError):
            self._send_frame(0x8, struct.pack("!H", 1000))
        with contextlib.suppress(OSError):
            self.sock.close()


class WSCollector:
    """Reads a websocket in a thread and keeps every JSON message."""

    def __init__(self, ws: WebSocket) -> None:
        self.ws = ws
        self.messages: list[Any] = []
        self.error: str | None = None
        self._stop = threading.Event()
        ws.sock.settimeout(1.0)
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()

    def _run(self) -> None:
        while not self._stop.is_set():
            try:
                msg = self.ws.recv()
            except TimeoutError:
                continue
            except (OSError, ConnectionError) as e:
                if not self._stop.is_set():
                    self.error = str(e)
                return
            if msg is None:
                self.error = self.error or "the hub closed the socket"
                return
            try:
                self.messages.append(json.loads(msg))
            except json.JSONDecodeError:
                self.messages.append({"_raw": msg})

    def stop(self) -> None:
        self._stop.set()
        self.ws.close()
        self.thread.join(timeout=3)

    @staticmethod
    def kind(m: Any) -> str:
        if not isinstance(m, dict):
            return "?"
        for k in ("type", "t", "op", "kind", "event"):
            v = m.get(k)
            if isinstance(v, str):
                return v
        return "?"

    def kinds(self) -> Counter[str]:
        return Counter(self.kind(m) for m in self.messages)

    def topics(self) -> Counter[str]:
        c: Counter[str] = Counter()
        for m in self.messages:
            if isinstance(m, dict) and isinstance(m.get("topic"), str):
                c[m["topic"]] += 1
        return c


# ---------------------------------------------------------------------------- utils


def wait_for(cond: Callable[[], Any], timeout: float, step: float = 0.5) -> tuple[Any, float]:
    """(the first truthy value of cond(), seconds it took); (None, timeout) on timeout."""
    t0 = time.monotonic()
    while True:
        v = cond()
        if v:
            return v, time.monotonic() - t0
        if time.monotonic() - t0 >= timeout:
            return None, timeout
        time.sleep(step)


def alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    st = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], capture_output=True, text=True).stdout
    return bool(st.strip()) and not st.strip().startswith("Z")


def tail(path: str, n: int = 15) -> str:
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            return "".join(fh.readlines()[-n:])
    except OSError:
        return ""


def norm(p: str) -> str:
    return p.rstrip("/") + "/"


# ---------------------------------------------------------------------------- checks


class Cluster:
    def __init__(self, ctx: dict[str, Any]) -> None:
        self.ctx = ctx
        self.api = Api(ctx["api"], ctx["token"])
        self.r = Results()
        self.loop = asyncio.new_event_loop()
        self.db = self.loop.run_until_complete(asyncpg.connect(ctx["pg_dsn"]))
        self.node_id = {m: self._node_cfg(m)["node_id"] for m in ("a", "b")}
        self.workers: dict[str, list[str]] = {"a": [], "b": []}
        self.videos: list[tuple[str, str]] = []  # (id, path)
        self.libraries: list[str] = []
        self.has_agent = False

    # -- plumbing --

    def q(self, sql: str, *args: Any) -> list[Any]:
        return list(self.loop.run_until_complete(self.db.fetch(sql, *args)))

    def _node_cfg(self, m: str) -> dict[str, Any]:
        with open(self.ctx["node_configs"][m], encoding="utf-8") as fh:
            return dict(json.load(fh))

    def cluster(self) -> dict[str, Any]:
        r = self.api.get("/api/cluster")
        if r.status != 200:
            raise Abort(f"GET /api/cluster answered {r.status}: {r.text()[:200]}")
        return dict(r.json())

    def node(self, m: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        data = data or self.cluster()
        for n in data.get("nodes", []):
            if n["id"] == self.node_id[m]:
                return dict(n)
        raise Abort(f"machine {m.upper()} is not on the home's machine list")

    def node_workers(self, m: str, data: dict[str, Any] | None = None) -> list[dict[str, Any]]:
        return [w for w in self.node(m, data).get("workers", []) if w.get("online")]

    def enqueue(self, kind: str, subject: str, stage: str) -> int:
        rows = self.q(
            "SELECT job_id, outcome FROM processing_jobs_enqueue(p_subject_kind => $1,"
            " p_subject_id => $2::uuid, p_stage => $3, p_force => true, p_reason => $4::jsonb)",
            kind,
            subject,
            stage,
            json.dumps({"why": "cluster-e2e", "by": "cluster-e2e"}),
        )
        if not rows or rows[0]["job_id"] is None:
            raise Abort(f"enqueue {stage} for {kind} {subject}: {rows[0]['outcome'] if rows else 'no row'}")
        return int(rows[0]["job_id"])

    def jobs_finished(self, ids: list[int]) -> list[Any] | None:
        rows = self.q("SELECT id, state, stage, error FROM processing_jobs WHERE id = ANY($1::bigint[])", ids)
        return rows if rows and all(r["state"] not in LIVE_STATES for r in rows) else None

    def claims(self, ids: list[int]) -> dict[int, list[str]]:
        out: dict[int, list[str]] = {}
        for r in self.q(
            "SELECT job_id, claimed_by FROM e2e_claims WHERE job_id = ANY($1::bigint[]) ORDER BY id", ids
        ):
            out.setdefault(int(r["job_id"]), []).append(str(r["claimed_by"]))
        return out

    def machine_of(self, worker_id: str) -> str:
        for m, ws in self.workers.items():
            if worker_id in ws:
                return m.upper()
        n8 = {m: self.node_id[m].replace("-", "")[:8] for m in ("a", "b")}
        for m, p in n8.items():
            if worker_id.startswith(f"node-{p}-"):
                return m.upper()
        if worker_id == self.ctx["home_worker"]:
            return "home"
        return f"?{worker_id}"

    def switch(self, m: str, stages: dict[str, bool | None]) -> None:
        r = self.api.call("PUT", f"/api/cluster/nodes/{self.node_id[m]}", {"stages": stages})
        if r.status != 200:
            raise Abort(f"PUT stages for {m.upper()} answered {r.status}: {r.text()[:200]}")

    def served(self, m: str) -> set[str] | None:
        ws = self.node_workers(m)
        if not ws:
            return None
        return set().union(*(set(w.get("stages") or []) for w in ws))

    def stages_off(self, m: str) -> dict[str, str]:
        out: dict[str, str] = {}
        for w in self.node_workers(m):
            out.update(w.get("stages_off") or {})
        return out

    def processes_alive(self) -> None:
        for k in ("api", "home", "a", "b"):
            pid = self.ctx["pids"][k]
            if not alive(pid):
                log = {"api": "api.log", "home": "worker-home.log", "a": "node-a.log", "b": "node-b.log"}[k]
                raise Abort(
                    f"process {k} (pid {pid}) is not running:\n" + tail(os.path.join(self.ctx["logs"], log))
                )

    # -- 1. registration --

    def check_registration(self) -> None:
        say("1. registration: waiting for both machines' workers on GET /api/workers")
        names = self.ctx["names"]

        def ready() -> list[dict[str, Any]] | None:
            self.processes_alive()
            r = self.api.get("/api/workers")
            if r.status != 200:
                return None
            ws = r.json().get("workers", [])
            online = [w for w in ws if w.get("online")]
            nodes = {w.get("node_name") for w in online if w.get("role") == "node"}
            home = [w for w in online if w.get("worker_id") == self.ctx["home_worker"]]
            return ws if {names["a"], names["b"]} <= nodes and home else None

        ws, took = wait_for(ready, 150)
        if not ws:
            r = self.api.get("/api/workers")
            raise Abort(f"the machines never registered within 150 s; /api/workers: {r.text()[:600]}")
        for w in ws:
            say(
                f"   worker {w['worker_id']}: host={w['host']} role={w.get('role')} "
                f"node_name={w.get('node_name')} online={w.get('online')} serves={w.get('serves')}"
            )
        for m in ("a", "b"):
            mine = [
                w
                for w in ws
                if w.get("online") and w.get("role") == "node" and w.get("node_name") == names[m]
            ]
            self.workers[m] = [w["worker_id"] for w in mine]
            ok = bool(mine) and all(w.get("node_id") == self.node_id[m] for w in mine)
            self.r.check(
                ok,
                f"machine {m.upper()} registered as a node",
                f"{len(mine)} worker(s) {self.workers[m]} role=node node_name={names[m]} node_id ok "
                f"(after {took:.0f}s)",
                f"workers: {mine}",
            )
        home = [w for w in ws if w["worker_id"] == self.ctx["home_worker"]]
        self.r.check(
            bool(home) and home[0].get("role") == "home" and home[0].get("node_id") is None,
            "the home's worker registered as the home",
            f"{self.ctx['home_worker']} role=home node_id=null",
            f"home worker row: {home}",
        )
        data = self.cluster()
        for m in ("a", "b"):
            n = self.node(m, data)
            self.r.check(
                n["state"] == "approved" and n.get("online") is True,
                f"machine {m.upper()} approved and online on GET /api/cluster",
                f"state={n['state']} online={n.get('online')} os={n.get('os')} arch={n.get('arch')}",
            )

    # -- 2. path maps and storage --

    def check_path_maps(self) -> None:
        say("2. path maps: each machine infers its own way to the share")
        share = os.path.realpath(self.ctx["share"])
        db_prefix = norm(self.ctx["db_share"])
        locals_: dict[str, str] = {}
        for m in ("a", "b"):

            def inferred(m: str = m) -> dict[str, Any] | None:
                n = self.node(m)
                st = n.get("storage") or {}
                ok = n.get("path_map") and st.get("read") and st.get("write")
                return n if ok else None

            n, _ = wait_for(inferred, 30)
            if not n:
                n = self.node(m)
            pm = n.get("path_map") or {}
            local = pm.get(db_prefix) or pm.get(db_prefix.rstrip("/")) or ""
            locals_[m] = local
            expected = norm(self.ctx["mounts"][m])
            self.r.check(
                n.get("path_map_source") == "inferred" and local != "" and os.path.realpath(local) == share,
                f"machine {m.upper()} inferred its path map",
                f"{db_prefix} -> {local} (source={n.get('path_map_source')}; "
                f"{'its own mount' if norm(local) == expected else 'expected ' + expected})",
                f"path_map={pm} source={n.get('path_map_source')} (the share is {share})",
            )
            st = n.get("storage") or {}
            self.r.check(
                st.get("read") == "ok" and st.get("write") == "ok",
                f"machine {m.upper()} storage check",
                f"read={st.get('read')} write={st.get('write')}",
                f"storage={st}",
            )
        self.r.check(
            bool(locals_.get("a")) and locals_.get("a") != locals_.get("b"),
            "the two machines reach the share by different local paths",
            f"A: {locals_.get('a')}  B: {locals_.get('b')}",
        )

    # -- 3. home-only stages: refused --

    def check_home_only_refused(self) -> list[int]:
        say("3. home-only queues: asked for with --stages, refused by both machines")
        asked = [s for s in self.ctx["node_stages"] if s in HOME_ONLY]
        for m in ("a", "b"):
            served = self.served(m) or set()
            off = self.stages_off(m)
            bad_served = sorted(served & set(HOME_ONLY))
            reasons = {s: off.get(s) for s in asked}
            ok = not bad_served and all(
                reasons[s] == "home_only" or (s == "index" and reasons[s] == "unavailable") for s in asked
            )
            self.r.check(
                ok,
                f"machine {m.upper()} refuses the home-only stages",
                f"serves {sorted(served)}; off: " + ", ".join(f"{s}={r}" for s, r in reasons.items()),
                f"serves home-only {bad_served}; reasons {reasons}",
            )
        # jobs the machines must never take: thumb_small per video, review_pages per library
        ids = [self.enqueue("video", v, "thumb_small") for v, _ in self.videos]
        ids += [self.enqueue("library", lib, "review_pages") for lib in self.libraries]
        say(
            f"   enqueued {len(ids)} home-only jobs (thumb_small x{len(self.videos)}, review_pages x{len(self.libraries)})"
        )
        return ids

    # -- 4. stage switches --

    def _wait_switch(self, m: str, stage: str, on: bool) -> float | None:
        def done() -> bool:
            served = self.served(m)
            if served is None:
                return False
            if on:
                return stage in served
            return stage not in served and self.stages_off(m).get(stage) == "switched_off"

        ok, took = wait_for(done, SWITCH_TIMEOUT)
        return took if ok else None

    def _batch(
        self, label: str, videos: list[tuple[str, str]], expect: str, ws: WSCollector | None = None
    ) -> None:
        ids = [self.enqueue("video", v, "probe") for v, _ in videos]
        say(f"   {label}: enqueued probe for {len(ids)} videos; expecting machine {expect} only")
        rows, took = wait_for(lambda: self.jobs_finished(ids), BATCH_TIMEOUT)
        if not rows:
            rows = self.q(
                "SELECT id, state, stage, error FROM processing_jobs WHERE id = ANY($1::bigint[])", ids
            )
            self.r.fail(
                f"{label}: probe jobs finish",
                f"not finished in {BATCH_TIMEOUT:.0f}s: " + str([(r["id"], r["state"]) for r in rows]),
            )
            return
        states = Counter(r["state"] for r in rows)
        errors = [str(r["error"])[:160] for r in rows if r["state"] != "done"]
        self.r.check(
            states.get("done", 0) == len(ids),
            f"{label}: every probe ran (ffprobe through machine {expect}'s path map)",
            f"{len(ids)} done in {took:.1f}s",
            f"states {dict(states)}; errors {errors[:3]}",
        )
        claims = self.claims(ids)
        by = Counter(self.machine_of(w) for c in claims.values() for w in c)
        wrong = {k: v for k, v in by.items() if k != expect}
        self.r.check(
            not wrong and by.get(expect, 0) >= len(ids),
            f"{label}: only machine {expect} pulled the probe queue",
            f"claims by machine: {dict(by)}",
            f"claims by machine: {dict(by)} (expected only {expect})",
        )
        vids = [v for v, _ in videos]
        st = Counter(r["state"] for r in self.q("SELECT state FROM videos WHERE id = ANY($1::uuid[])", vids))
        say(f"   {label}: video states now {dict(st)}")

    def check_switches(self, ws_hook: Callable[[], WSCollector | None]) -> WSCollector | None:
        say("4. stage switches: probe off for B, A pulls everything; then the other way round")
        half = (len(self.videos) + 1) // 2
        batch1, batch2 = self.videos[:half], self.videos[half:]
        served_a, served_b = self.served("a") or set(), self.served("b") or set()
        if "probe" not in served_a or "probe" not in served_b:
            raise Abort(f"probe is not served by both machines to begin with: A {served_a}, B {served_b}")

        self.switch("b", {"probe": False})
        took = self._wait_switch("b", "probe", on=False)
        self.r.check(
            took is not None,
            "switch probe off for B: B stops pulling from the probe queue",
            f"B's served stages dropped probe (stages_off=switched_off) after {took or 0:.1f}s",
            f"B still serves probe after {SWITCH_TIMEOUT:.0f}s: {self.served('b')} off={self.stages_off('b')}",
        )
        if took is not None:
            say(f"   switch latency (B off): {took:.1f}s")
        # phase 1 re-reads the switches every 30 s; with the cluster channel the hub sends
        # reload_stages at once
        if not self.has_agent:
            self.r.absent(
                "phase 2: a switch reaches the machine at once (reload_stages)",
                f"no agent channel in this build; phase 1 took {took or 0:.1f}s (its 30 s re-read)",
            )
        else:
            self.r.check(
                took is not None and took <= SWITCH_FAST,
                "phase 2: a switch reaches the machine at once (reload_stages)",
                f"{took or 0:.1f}s (limit {SWITCH_FAST:.0f}s)",
                f"took {took if took is not None else 'forever'}s (limit {SWITCH_FAST:.0f}s)",
            )
        self._batch("batch 1", batch1, "A")

        self.switch("b", {"probe": None})
        self.switch("a", {"probe": False})
        took_a = self._wait_switch("a", "probe", on=False)
        took_b = self._wait_switch("b", "probe", on=True)
        self.r.check(
            took_a is not None and took_b is not None,
            "switch probe off for A and back to automatic for B",
            f"A dropped probe after {took_a or 0:.1f}s; B serves it again after {took_b or 0:.1f}s",
            f"A serves {self.served('a')} (off {self.stages_off('a')}); B serves {self.served('b')}",
        )
        overrides = self.node("a").get("stage_overrides")
        self.r.check(
            overrides == {"probe": False},
            "the home records the switch",
            f"A stage_overrides={overrides}",
        )
        collector = ws_hook()
        self._batch("batch 2", batch2, "B", collector)
        self.switch("a", {"probe": None})
        return collector

    # -- 3b. home-only stages: never pulled --

    def check_home_only_claims(self, ids: list[int]) -> None:
        claims = self.claims(ids)
        by = Counter(self.machine_of(w) for c in claims.values() for w in c)
        node_claims = by.get("A", 0) + by.get("B", 0)
        home_serves = any(s in HOME_ONLY for s in self.ctx["home_stages"])
        detail = f"claims on home-only jobs by: {dict(by) or 'nobody'}"
        if home_serves:
            self.r.check(
                node_claims == 0 and by.get("home", 0) > 0,
                "home-only queues are pulled by the home, never by a machine",
                detail,
            )
        else:
            self.r.check(node_claims == 0, "home-only queues never pulled by a machine", detail)
        # and nothing outside a machine's served set at all
        node_ws = [w for m in ("a", "b") for w in self.workers[m]]
        rows = self.q(
            "SELECT stage, claimed_by, count(*) AS n FROM e2e_claims WHERE claimed_by = ANY($1::text[])"
            " GROUP BY 1, 2 ORDER BY 1, 2",
            node_ws,
        )
        asked = set(self.ctx["node_stages"]) - set(HOME_ONLY)
        stray = [(r["stage"], r["claimed_by"], r["n"]) for r in rows if r["stage"] not in asked]
        self.r.check(
            not stray,
            "machines pulled only from queues they serve",
            "claims by machines: "
            + ", ".join(f"{r['stage']}@{self.machine_of(r['claimed_by'])}={r['n']}" for r in rows),
            f"unexpected claims: {stray}",
        )

    # -- 6. phase 2: the hub --

    def ws_open(self) -> WSCollector | None:
        if not self.api.exists("/api/cluster/ws"):
            self.r.absent(
                "phase 2: WebSocket hub", "GET /api/cluster/ws answers 404 (the hub is not in this build)"
            )
            return None
        url = self.ctx["api"].replace("http://", "ws://", 1) + "/api/cluster/ws"
        try:
            ws = WebSocket(url, {"Authorization": f"Bearer {self.ctx['token']}"})
        except (OSError, WSError) as e:
            self.r.fail("phase 2: WebSocket hub accepts a connection", str(e))
            return None
        col = WSCollector(ws)
        ws.send_json({"op": "subscribe", "topics": ["queues", "jobs"]})
        time.sleep(1.0)
        say(f"   websocket open; subscribed to queues, jobs (first messages: {dict(col.kinds())})")
        return col

    def ws_check(self, col: WSCollector | None) -> None:
        if col is None:
            return
        time.sleep(3.0)
        col.stop()
        kinds = col.kinds()
        topics = col.topics()
        say(
            f"   websocket messages: kinds {dict(kinds)} topics {dict(topics)}"
            + (f" (error: {col.error})" if col.error else "")
        )
        job_like = kinds.get("job", 0) + topics.get("jobs", 0)
        queue_like = kinds.get("queues", 0) + topics.get("queues", 0)
        self.r.check(
            len(col.messages) > 0,
            "phase 2: WebSocket hub takes a subscription",
            f"{len(col.messages)} messages",
            f"no message after subscribing (error: {col.error})",
        )
        self.r.check(
            job_like > 0,
            "phase 2: hub pushes `job` messages while jobs run",
            f"{job_like} job message(s)",
            f"none; kinds {dict(kinds)}",
        )
        self.r.check(
            queue_like > 0,
            "phase 2: hub pushes `queues` messages",
            f"{queue_like} queues message(s)",
            f"none; kinds {dict(kinds)}",
        )

    # -- 7. phase 2: files on the share --

    def check_files(self) -> None:
        if not self.has_agent:
            self.r.absent(
                "phase 2: cluster files on the share",
                "GET /api/cluster/agent answers 404 (no agent channel in this build)",
            )
            return
        say("7. cluster files: <share>/maktaba/cluster/<ip>/{logs,metrics,jobs}/<date>/<worker>.jsonl")
        share = self.ctx["share"]
        # every machine's own files, and the hub's queue snapshots (every 10 s)
        targets = [
            (m, w, kind) for m in ("a", "b") for w in self.workers[m] for kind in ("logs", "metrics", "jobs")
        ]
        targets.append(("home", "hub", "queues"))

        def scan() -> dict[tuple[str, str, str], list[str]]:
            return {
                (m, w, kind): glob.glob(
                    os.path.join(share, "**", "maktaba", "cluster", "*", kind, "*", f"{w}.jsonl"),
                    recursive=True,
                )
                for m, w, kind in targets
            }

        got, _ = wait_for(lambda: (lambda s: s if all(s.values()) else None)(scan()), FILES_TIMEOUT, step=1.0)
        if got is None:
            got = scan()
        for (m, w, kind), paths in sorted(got.items()):
            name = (
                f"phase 2: the hub's {kind} snapshots on the share"
                if m == "home"
                else f"phase 2: machine {m.upper()} {kind} file on the share"
            )
            if paths:
                size = sum(os.path.getsize(p) for p in paths)
                rel = os.path.relpath(paths[0], share)
                self.r.check(size > 0, name, f"{rel} ({size} bytes)", f"{rel} is empty")
            else:
                fallback = glob.glob(
                    os.path.join(self.ctx["homes"][m], ".maktaba", "cluster", "**", f"{w}.jsonl"),
                    recursive=True,
                )
                self.r.fail(
                    name,
                    f"no {kind}/<date>/{w}.jsonl after {FILES_TIMEOUT:.0f}s"
                    + (f"; found in the local fallback instead: {fallback[:2]}" if fallback else ""),
                )
        ips = sorted(
            {p.split(os.sep + "cluster" + os.sep, 1)[1].split(os.sep)[0] for ps in got.values() for p in ps}
        )
        if ips:
            say(f"   machine folders: {ips}")

    # -- 8. phase 2: LLM routing --

    def check_llm(self) -> None:
        base = "/api/cluster/llm/v1"
        if not self.api.exists(f"{base}/chat/completions", method="POST") and not self.api.exists(
            f"{base}/models"
        ):
            self.r.absent(
                "phase 2: LLM routing", f"{base}/chat/completions answers 404 (no LLM router in this build)"
            )
            return
        say("8. LLM: a request through the home reaches machine B's runtime, not A")
        model = "e2e-fake:latest"
        marker_b = f"from {self.ctx['names']['b']}"

        def listed() -> bool:
            r = self.api.get(f"{base}/models")
            if r.status != 200:
                return False
            data = r.json() or {}
            ids = [m.get("id") for m in (data.get("data") or []) if isinstance(m, dict)]
            return model in ids or model.removesuffix(":latest") in ids

        ok, took = wait_for(listed, LLM_TIMEOUT, step=1.0)
        self.r.check(
            bool(ok),
            "phase 2: the home lists machine B's model",
            f"{model} in {base}/models after {took:.0f}s",
            f"{model} not in {base}/models after {LLM_TIMEOUT:.0f}s: {self.api.get(base + '/models').text()[:300]}",
        )
        before = self._ollama_requests()
        body = {
            "model": model,
            "messages": [{"role": "user", "content": "ping from cluster-e2e"}],
            "stream": False,
        }
        r = self.api.call("POST", f"{base}/chat/completions", body, timeout=60)
        content = ""
        if r.status == 200:
            with contextlib.suppress(Exception):
                content = r.json()["choices"][0]["message"]["content"]
        after = self._ollama_requests()
        self.r.check(
            r.status == 200 and marker_b in content and after > before,
            "phase 2: chat completion served by machine B (fake Ollama), not A",
            f"200, reply {content!r}; B's runtime saw {after - before} request(s)",
            f"status {r.status} body {r.text()[:300]!r}; B's runtime saw {after - before} request(s)",
        )
        # streaming, OpenAI wire format (SSE)
        before = self._ollama_requests()
        body["stream"] = True
        r = self.api.call("POST", f"{base}/chat/completions", body, timeout=60)
        text, done = "", False
        for line in r.text().splitlines():
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                done = True
                continue
            with contextlib.suppress(Exception):
                text += json.loads(data)["choices"][0].get("delta", {}).get("content") or ""
        after = self._ollama_requests()
        self.r.check(
            r.status == 200 and marker_b in text and done and after > before,
            "phase 2: streamed chat completion (SSE) served by machine B",
            f"{text!r}, [DONE] seen",
            f"status {r.status}, text {text!r}, done={done}, B saw {after - before}; body {r.text()[:200]!r}",
        )

    def _ollama_requests(self) -> int:
        try:
            with open(self.ctx["ollama"]["requests"], encoding="utf-8") as fh:
                return sum(
                    1
                    for line in fh
                    if '"/api/chat"' in line or "/chat/completions" in line or '"/api/generate"' in line
                )
        except OSError:
            return 0

    # -- 5. revoke --

    def check_revoke(self) -> None:
        say("5. revoke machine A: its process stops, its login is refused, B keeps working")
        a_id = self.node_id["a"]
        t0 = time.monotonic()
        r = self.api.call("POST", f"/api/cluster/nodes/{a_id}/revoke")
        self.r.check(
            r.status == 200 and (r.json() or {}).get("state") == "revoked",
            "revoke machine A on the home",
            "state=revoked",
            f"{r.status}: {r.text()[:200]}",
        )
        pid = self.ctx["pids"]["a"]
        gone, _ = wait_for(lambda: not alive(pid), REVOKE_TIMEOUT)
        took = time.monotonic() - t0
        self.r.check(
            bool(gone),
            "machine A's process exits after the revoke",
            f"pid {pid} exited {took:.1f}s after the revoke",
            f"pid {pid} still running after {REVOKE_TIMEOUT:.0f}s:\n{tail(os.path.join(self.ctx['logs'], 'node-a.log'))}",
        )
        if gone:
            log = tail(os.path.join(self.ctx["logs"], "node-a.log"), 60)
            why = next(
                (
                    e
                    for e in (
                        "cluster_node_login_refused_stopping",
                        "cluster_node_revoked_stopping",
                        "pipeline_runtime_stopped",
                    )
                    if e in log
                ),
                "no stop event in its log",
            )
            say(f"   machine A's log: {why}")
        dsn = self._node_cfg("a")["database_url"]
        err = ""
        try:
            conn = self.loop.run_until_complete(asyncpg.connect(dsn, timeout=10))
            self.loop.run_until_complete(conn.close())
        except Exception as e:  # noqa: BLE001 - any refusal is the answer we want
            err = f"{type(e).__name__}: {e}"
        refused = bool(err) and any(
            k in err
            for k in (
                "not permitted to log in",
                "password authentication failed",
                "InvalidAuthorization",
                "InvalidPassword",
            )
        )
        self.r.check(
            refused,
            "machine A's database login is refused",
            err or "?",
            "the login still works" if not err else f"failed for another reason: {err}",
        )
        b_alive = alive(self.ctx["pids"]["b"])
        b_workers = self.node_workers("b")
        self.r.check(
            b_alive and bool(b_workers),
            "machine B keeps working after A's revoke",
            f"B's process alive, {len(b_workers)} worker(s) online",
            f"B alive={b_alive} workers={b_workers}",
        )

    # -- run --

    def run(self) -> int:
        say(
            f"cluster {self.ctx['rid']}: home {self.ctx['api']}, machines A={self.node_id['a']} B={self.node_id['b']}"
        )
        self.videos = [
            (str(r["id"]), str(r["path"])) for r in self.q("SELECT id, path FROM videos ORDER BY path")
        ]
        self.libraries = [str(r["id"]) for r in self.q("SELECT id FROM libraries ORDER BY name")]
        # phase 2 (the cluster channel) is detected, not assumed: it is built in parallel
        self.has_agent = self.api.exists("/api/cluster/agent")
        say(f"phase 2 agent channel (/api/cluster/agent): {'present' if self.has_agent else 'absent'}")
        try:
            self.check_registration()
            self.check_path_maps()
            home_only_jobs = self.check_home_only_refused()
            collector = self.check_switches(self.ws_open)
            self.ws_check(collector)
            self.check_home_only_claims(home_only_jobs)
            self.check_files()
            self.check_llm()
            self.check_revoke()
        except Abort as e:
            self.r.fail("aborted", str(e))
        finally:
            with contextlib.suppress(Exception):
                self.loop.run_until_complete(self.db.close())
        print()
        print("=" * 72)
        for status, name, _detail in self.r.rows:
            print(f"{status:4}  {name}")
        print("-" * 72)
        print(f"cluster-e2e: {self.r.summary()}")
        print("=" * 72, flush=True)
        return 1 if self.r.failed else 0


def main() -> int:
    ap = argparse.ArgumentParser(description="cluster e2e checks (run by run.sh)")
    ap.add_argument("--context", required=True, help="the run's context.json")
    args = ap.parse_args()
    with open(args.context, encoding="utf-8") as fh:
        ctx = json.load(fh)
    return Cluster(ctx).run()


if __name__ == "__main__":
    sys.exit(main())
