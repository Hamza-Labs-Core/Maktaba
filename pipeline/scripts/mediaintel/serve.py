"""serve: a live status page for a running ``mediaintel run`` (or any of its steps).

Reads the cache read-only (``index_jobs``, ``files``, ``series``, ``plex_collections``)
and, when given, the run's log, and serves one auto-refreshing page plus
``/api/status`` (JSON). Inside Maktaba the same information comes from
``processing_jobs`` over the ``jobs.progress`` WebSocket channel (Processing Queue
page); this page is the stand-in until these stages run there.
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import re
import signal
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

_STEP_RE = re.compile(r"^##### (.+)$")
_TIME_RE = re.compile(r"\((\d\d:\d\d:\d\d)\)\s*$")
_SIG_RE = re.compile(r"^fingerprinting (\d+) videos")
_SIG_DONE_RE = re.compile(r"^\s*\[(\d+)/\d+\] \d+ per minute")
_COUNT_RE = re.compile(r"\((\d+) (?:folders|libraries)\)")

#: the run's steps in order: (log heading prefix, plain title, what it does)
STEPS: list[tuple[str, str, str]] = [
    (
        "scan-local, listing",
        "List files in each library",
        "every video file, its size and date; nothing is opened yet",
    ),
    (
        "scan-plex",
        "Read lengths from Plex",
        "Plex already measured most files: length, resolution, Plex item",
    ),
    (
        "scan-local (",
        "Measure the rest",
        "ffprobe for the few files Plex lacks; thumbnail fingerprints of episode-length videos",
    ),
    (
        "scan-youtube (",
        "Read YouTube channels",
        "playlists, positions and upload dates (listing only, polite)",
    ),
    ("link", "Match files to YouTube videos", "by file name, title and length"),
    ("detect", "Find series and seasons", "playlists + titles + dates + fingerprints"),
    (
        "signatures",
        "Fingerprint intros, end songs and frames",
        "only a few episodes per season and the unplaced videos that could still join one",
    ),
    ("detect again", "Find seasons again", "with the fingerprints as extra evidence"),
    ("scan-youtube --exact", "Exact upload dates", "only with --exact"),
    ("tag", "Tag topics", "seerah, tafsir, hadith, Quran …, format and occasion"),
    ("scan-visual", "Rate production style", "talk / produced / cinematic, from sampled frames"),
    ("report", "Write review pages", "one HTML page per library"),
    ("export-plex", "Write Plex collections", "one collection per season, in episode order"),
    ("dizquetv-shows", "Update dizqueTV shows", "a show per series and per season"),
    ("plan-channels", "Plan TV channels", "themed / mix / presenter channels, time slots, fillers"),
    ("export-dizquetv", "Create the TV channels", "in dizqueTV, with schedules and fillers"),
]

STAGE_NAMES = {
    "probe": "Length measured",
    "thumbnail": "Thumbnail fingerprint",
    "signature": "Intro/end-song + frame fingerprint",
    "visual_style": "Production style sample",
}


def _steps(lines: list[str]) -> tuple[list[dict[str, Any]], dict[str, Any] | None]:
    """The step list with state, and progress inside the current step."""
    seen: list[tuple[int, str, int]] = []  # (step index, start time, log line)
    for i, ln in enumerate(lines):
        m = _STEP_RE.match(ln)
        if not m:
            continue
        head = m.group(1)
        idx = next((k for k, (pre, _, _) in enumerate(STEPS) if head.startswith(pre)), None)
        if idx is not None:
            t = _TIME_RE.search(head)
            seen.append((idx, t.group(1) if t else "", i))
    done_line = any(ln.startswith("done in ") for ln in lines)
    current = seen[-1][0] if seen and not done_line else None
    started = {idx: t for idx, t, _ in seen}
    out = []
    for k, (_, title, what) in enumerate(STEPS):
        state = (
            "done" if k in started and k != current else "running" if k == current else "waiting"
        )
        out.append({"n": k + 1, "title": title, "what": what, "state": state,
                    "started": started.get(k, "")})  # fmt: skip
    progress = None
    if seen and current is not None:
        _, _, at = seen[-1]
        head = _STEP_RE.match(lines[at]).group(1)  # type: ignore[union-attr]
        total_m = _COUNT_RE.search(head)
        body = lines[at + 1 :]
        if STEPS[current][0].startswith("scan-local"):
            n = sum(1 for ln in body if ": {'videos'" in ln and ln.count("/") <= 2)
            unit = "libraries"
        elif STEPS[current][0] == "export-plex":
            n, unit = sum(1 for ln in body if ln.startswith("=== ")), "libraries"
        elif STEPS[current][0].startswith("scan-youtube"):
            n, unit = sum(1 for ln in body if ": scanning" in ln or ": cached" in ln), "channels"
        else:
            n, unit = len(body), "log lines"
        progress = {"done": n, "total": int(total_m.group(1)) if total_m else None, "unit": unit}
    return out, progress


PAGE = (Path(__file__).with_name("status.html")).read_text(encoding="utf-8")


def status(db_path: str, log_path: Path | None) -> dict[str, Any]:
    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=30)
    conn.row_factory = sqlite3.Row

    def has(table: str) -> bool:
        return bool(
            conn.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", (table,)
            ).fetchone()
        )

    out: dict[str, Any] = {"stages": [], "libraries": [], "errors": [], "log": [], "steps": []}
    files, long_ = conn.execute("SELECT COUNT(*), SUM(duration_s >= 180) FROM files").fetchone()
    out["totals"] = {
        "files": files,
        "episode-length": long_ or 0,
        "seasons": conn.execute("SELECT COUNT(*) FROM series").fetchone()[0]
        if has("series")
        else 0,
        "Plex collections written": (
            conn.execute("SELECT COUNT(*) FROM plex_collections").fetchone()[0]
            if has("plex_collections")
            else 0
        ),
    }
    if has("index_jobs"):
        # each stage over the files it applies to, "done" counted within that same set:
        #   probe: every file; thumbnail: files with a thumbnail that are not known shorts;
        #   signature: episode-length files (length known); visual_style: sampled episodes
        applies = {
            "probe": "1",
            "thumbnail": "f.thumb_path IS NOT NULL AND (f.duration_s IS NULL OR f.duration_s >= 180)",
            "signature": "f.duration_s >= 180",
        }
        for stage, cond in applies.items():
            r = conn.execute(
                f"""SELECT COUNT(*) total,
                           SUM(j.state = 'done') done, SUM(j.state = 'failed') failed
                    FROM files f LEFT JOIN index_jobs j ON j.path = f.path AND j.stage = ?
                    WHERE {cond}""",  # noqa: S608 — fixed fragments
                (stage,),
            ).fetchone()
            recent = conn.execute(
                """SELECT COUNT(*) FROM index_jobs WHERE stage = ? AND state = 'done'
                   AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-10 minutes')""",
                (stage,),
            ).fetchone()[0]
            out["stages"].append(
                {
                    "stage": STAGE_NAMES.get(stage, stage),
                    "per_min": recent / 10,
                    "done": r["done"] or 0,
                    "failed": r["failed"] or 0,
                    "of": r["total"] or 0,
                }  # fmt: skip
            )
        r = conn.execute(
            "SELECT SUM(state='done') d, SUM(state='failed') f FROM index_jobs WHERE stage='visual_style'"
        ).fetchone()
        if r["d"] or r["f"]:
            out["stages"].append(
                {
                    "stage": "visual_style",
                    "done": r["d"] or 0,
                    "failed": r["f"] or 0,
                    "of": (r["d"] or 0) + (r["f"] or 0),
                }  # fmt: skip
            )
        unknown = conn.execute("SELECT COUNT(*) FROM files WHERE duration_s IS NULL").fetchone()[0]
        out["totals"]["length not known yet"] = unknown
        out["errors"] = [
            {
                "stage": r["stage"],
                "file": Path(r["path"]).name,
                "error": r["error"],
                "attempts": r["attempts"],
            }  # fmt: skip
            for r in conn.execute(
                "SELECT stage, path, error, attempts FROM index_jobs WHERE state='failed' "
                "ORDER BY updated_at DESC LIMIT 15"
            )
        ]

    # per-library counts: one grouped query each (no per-folder subqueries)
    def per_folder(sql: str, needs: str) -> dict[str, int]:
        if not has(needs):
            return {}
        return {r[0]: r[1] for r in conn.execute(sql)}

    sig = per_folder(
        """SELECT f.folder, COUNT(*) FROM index_jobs j JOIN files f ON f.path = j.path
           WHERE j.stage = 'signature' AND j.state = 'done' GROUP BY f.folder""",
        "index_jobs",
    )
    seasons = per_folder(
        "SELECT json_extract(metadata, '$.folder'), COUNT(*) FROM series GROUP BY 1", "series"
    )
    eps = per_folder(
        """SELECT json_extract(s.metadata, '$.folder'), COUNT(*) FROM series_episodes e
           JOIN series s ON s.id = e.series_id GROUP BY 1""",
        "series",
    )
    cols = per_folder(
        """SELECT json_extract(s.metadata, '$.folder'), COUNT(*) FROM plex_collections c
           JOIN series s ON s.id = c.series_id GROUP BY 1""",
        "plex_collections",
    )
    for r in conn.execute(
        """SELECT folder, COUNT(*) n, SUM(duration_s IS NOT NULL) probed,
                  SUM(duration_s >= 180) long
           FROM files GROUP BY folder ORDER BY folder"""
    ):
        fo = r["folder"]
        out["libraries"].append(
            {
                "name": fo.replace("\\", "/").rstrip("/").rsplit("/", 1)[-1],
                "folder": fo,
                "files": r["n"],
                "probed": r["probed"] or 0,
                "long": r["long"] or 0,
                "signatures": sig.get(fo, 0),
                "seasons": seasons.get(fo, 0),
                "episodes": eps.get(fo, 0),
                "collections": cols.get(fo, 0),
            }
        )
    out["styles"] = (
        {
            r[0]: r[1]
            for r in conn.execute("SELECT style, COUNT(*) FROM series_style GROUP BY style")
        }
        if has("series_style")
        else {}
    )
    out["genres"] = (
        [
            {"genre": r[0][len("genre:") :], "seasons": r[1]}
            for r in conn.execute(
                "SELECT tag, COUNT(*) n FROM series_tags WHERE tag LIKE 'genre:%' "
                "GROUP BY tag ORDER BY n DESC LIMIT 12"
            )
        ]
        if has("series_tags")
        else []
    )
    out["now"] = time.time()
    conn.close()
    if log_path and log_path.exists():
        lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
        out["steps"], out["progress"] = _steps(lines)
        # fingerprints are made only for selected videos: the step says how many
        sel = [int(m.group(1)) for ln in lines if (m := _SIG_RE.match(ln))]
        done_now = [int(m.group(1)) for ln in lines if (m := _SIG_DONE_RE.match(ln))]
        for st in out["stages"]:
            if st["stage"] == STAGE_NAMES["signature"]:
                if sel:
                    st["of"] = st["done"] - (done_now[-1] if done_now else 0) + sel[-1]
                    st["note"] = (
                        "only 6 episodes per season and the unplaced videos that could join one"
                    )
                else:
                    st["of"] = st["done"]
                    st["note"] = "selected after the first season detection"
        out["log"] = lines[-40:]
    return out


def _running_pid(db_path: str) -> int | None:
    f = Path(db_path + ".run.pid")
    try:
        pid = int(f.read_text(encoding="utf-8").strip())
    except (OSError, ValueError):
        return None
    return pid if _alive(pid) else None


def _alive(pid: int) -> bool:
    """Whether a process exists, without touching it. Never ``os.kill(pid, 0)`` on
    Windows: there it *terminates* the process (TerminateProcess with exit code 0)."""
    if os.name == "nt":
        import ctypes

        k32 = getattr(ctypes, "windll").kernel32  # noqa: B009 — attribute exists on Windows only
        handle = k32.OpenProcess(0x1000, False, pid)  # PROCESS_QUERY_LIMITED_INFORMATION
        if not handle:
            return False
        code = ctypes.c_ulong()
        ok = k32.GetExitCodeProcess(handle, ctypes.byref(code))
        k32.CloseHandle(handle)
        return bool(ok) and code.value == 259  # STILL_ACTIVE
    try:
        os.kill(pid, 0)  # POSIX: signal 0 only checks existence
    except OSError:
        return False
    return True


def _run_args(
    db_path: str, body: dict[str, Any], dizquetv_db: Path | None, reports: Path
) -> list[str]:
    """The exact ``mediaintel run`` command a page request maps to (no free-form input)."""
    args = [sys.executable, "-m", "scripts.mediaintel", "--db", db_path, "run"]
    folders = [str(f) for f in body.get("folders") or [] if isinstance(f, str)]
    args += folders or ["--from-plex", "--exclude-library", "Movies"]
    if dizquetv_db:
        args += ["--dizquetv-db", str(dizquetv_db)]
    args += ["--out", str(reports)]
    if body.get("plex"):
        args.append("--plex")
    if body.get("tv"):
        args.append("--tv")
    if body.get("exact"):
        args.append("--exact")
    if not body.get("youtube", True):
        args.append("--no-youtube")
    return args


def serve(
    db_path: str,
    log_path: Path | None,
    port: int = 8770,
    *,
    dizquetv_db: Path | None = None,
    reports: Path = Path("reports"),
) -> None:
    """The control centre: status, start / stop a run, explain a video, pin a style,
    open review pages. Binds to localhost only."""
    log = log_path or Path(db_path + ".run.log")
    cache: dict[str, Any] = {"at": 0.0, "st": {}}
    cache_lock = threading.Lock()
    pipeline_dir = Path(__file__).resolve().parents[2]

    class Handler(BaseHTTPRequestHandler):
        def _send(self, code: int, body: bytes, ctype: str) -> None:
            self.send_response(code)
            self.send_header("Content-Type", ctype)
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(body)

        def _json(self, code: int, obj: Any) -> None:
            data = json.dumps(obj, ensure_ascii=False).encode()
            self._send(code, data, "application/json; charset=utf-8")

        def do_GET(self) -> None:  # noqa: N802 — http.server API
            url = urllib.parse.urlsplit(self.path)
            qs = urllib.parse.parse_qs(url.query)
            if url.path == "/api/status":
                with cache_lock:  # one computation at a time; reused for 10 s
                    if time.time() - cache["at"] > 10:
                        cache["st"], cache["at"] = status(db_path, log), time.time()
                    st = dict(cache["st"])
                st["running_pid"] = _running_pid(db_path)
                self._json(200, st)
            elif url.path == "/api/explain":
                from . import explain

                conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=30)
                conn.row_factory = sqlite3.Row
                buf = io.StringIO()
                with contextlib.redirect_stdout(buf):
                    folder = qs["folder"][0] if qs.get("folder") else None
                    explain.explain(conn, (qs.get("q") or [""])[0], folder, 30)
                conn.close()
                self._json(200, {"text": buf.getvalue()})
            elif url.path.startswith("/reports/"):
                name = urllib.parse.unquote(url.path[len("/reports/") :])
                target = (reports / name).resolve()
                if reports.resolve() not in target.parents or not target.is_file():
                    self.send_error(404)
                    return
                ctype = "text/html; charset=utf-8" if target.suffix == ".html" else "image/jpeg"
                self._send(200, target.read_bytes(), ctype)
            elif url.path == "/api/reports":
                names = sorted(f.name for f in reports.glob("*.html")) if reports.exists() else []
                self._json(200, {"reports": names})
            elif url.path in ("/", "/index.html"):
                self._send(200, PAGE.encode(), "text/html; charset=utf-8")
            else:
                self.send_error(404)

        def do_POST(self) -> None:  # noqa: N802
            n = int(self.headers.get("Content-Length") or 0)
            try:
                body = json.loads(self.rfile.read(n) or b"{}")
            except ValueError:
                self._json(400, {"error": "The request body must be JSON."})
                return
            if self.path == "/api/run":
                if _running_pid(db_path):
                    self._json(409, {"error": "A run is already in progress. Stop it first."})
                    return
                args = _run_args(db_path, body, dizquetv_db, reports)
                env = {**os.environ, "PYTHONIOENCODING": "utf-8", "PYTHONUNBUFFERED": "1"}
                with log.open("w", encoding="utf-8") as fh:
                    subprocess.Popen(  # noqa: S603 — fixed argv built by _run_args
                        args, cwd=pipeline_dir, stdout=fh, stderr=subprocess.STDOUT, env=env
                    )
                self._json(202, {"started": True, "command": args[3:]})
            elif self.path == "/api/stop":
                pid = _running_pid(db_path)
                if not pid:
                    self._json(409, {"error": "No run is in progress."})
                    return
                os.kill(pid, signal.SIGTERM)  # TerminateProcess on Windows; finished work is saved
                self._json(200, {"stopped": pid})
            elif self.path == "/api/style":
                from . import db, scan_visual

                style = body.get("style") or None
                conn = db.connect(db_path)
                scan_visual.set_override(
                    conn, str(body.get("library", "")), str(body.get("series", "")), style
                )
                conn.close()
                self._json(200, {"ok": True})
            else:
                self.send_error(404)

        def log_message(self, *_: object) -> None:
            return

    print(f"mediaintel control centre on http://localhost:{port}/  (Ctrl+C to stop)", flush=True)
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
