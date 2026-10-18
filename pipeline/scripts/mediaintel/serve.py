"""serve: a live status page for a running ``mediaintel run`` (or any of its steps).

Reads the cache read-only (``index_jobs``, ``files``, ``series``, ``plex_collections``)
and, when given, the run's log, and serves one auto-refreshing page plus
``/api/status`` (JSON). Inside Maktaba the same information comes from
``processing_jobs`` over the ``jobs.progress`` WebSocket channel (Processing Queue
page); this page is the stand-in until these stages run there.
"""

from __future__ import annotations

import contextlib
import dataclasses
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
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

_STEP_RE = re.compile(r"^##### (.+)$")
_TIME_RE = re.compile(r"\((\d\d:\d\d:\d\d)\)\s*$")
_SIG_RE = re.compile(r"^(?:(.+): )?fingerprinting (\d+) videos")
_SIG_DONE_RE = re.compile(r"^\s*\[(\d+)/(\d+)\] \d+ per minute")
_RESUME_RE = re.compile(r"^starting at step '([\w-]+)'")
_INNER_RE = re.compile(r"^\s*\[(\d+)/(\d+)\] (.+): videos checked")
_COUNT_RE = re.compile(r"\((\d+) (?:folders|libraries)\)")

#: the run's steps in order: (log heading prefix, plain title, what it does)
STEPS: list[tuple[str, str, str]] = [
    (
        "scan-local, listing",
        "List files in each library",
        "every video file, its size and date; nothing is opened yet",
    ),
    (
        "plex-index",
        "Index Plex",
        "every Plex item and its details, saved once: lengths for the scan, details for dizqueTV",
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
    (
        "detect again",
        "Place leftover episodes by intro and end song",
        "unplaced videos join the season whose intro, end song or frames they match",
    ),
    (
        "scan-youtube --exact",
        "Exact upload dates",
        'the exact day for series episodes whose date is only "N years ago" (runs with --exact)',
    ),
    ("tag", "Tag topics", "seerah, tafsir, hadith, Quran …, format and occasion"),
    (
        "check-tags",
        "Check tags with a language model",
        "a local model reads each series' name and episode titles and corrects its topics",
    ),
    (
        "check-clips",
        "Check clips with a language model",
        "a local model reads each clip's title and keeps only the topics it is about",
    ),
    (
        "books",
        "Look up the books",
        "the book a lesson series explains (Wikipedia) decides its topics",
    ),
    (
        "group (",
        "Group clips and promos",
        "shorts and clips into filler collections, stand-alone videos by topic, promos to shows",
    ),
    ("export-plex", "Write Plex collections", "one collection per season, in episode order"),
    ("dizquetv-shows", "Update dizqueTV shows", "a show per series and per season"),
    ("report", "Write review pages", "one HTML page per library"),
    (
        "scan-visual",
        "Rate production style",
        "talk / produced / cinematic, from sampled frames; only channel planning needs it",
    ),
    ("plan-channels", "Plan TV channels", "themed / mix / presenter channels, time slots, fillers"),
    ("export-dizquetv", "Create the TV channels", "in dizqueTV, with schedules and fillers"),
    (
        "repair-broken",
        "Repair broken files",
        "unfinished downloads are fetched from YouTube again, checked, then swapped in",
    ),
    (
        "audit-channels",
        "Audit the channels",
        "is what each channel airs about the channel? (channel-audit.html)",
    ),
]

STAGE_NAMES = {
    "probe": "Length measured",
    "thumbnail": "Thumbnail fingerprint",
    "signature": "Intro/end-song + frame fingerprint",
    "visual_style": "Production style sample",
}


#: per-file stages and the files each applies to (SQL over ``files f``)
STAGE_APPLIES = {
    "probe": "1",
    "thumbnail": "f.thumb_path IS NOT NULL AND (f.duration_s IS NULL OR f.duration_s >= 180)",
    "signature": "f.duration_s >= 180",
}

#: for the stage page: what it does, the run steps (STEPS prefixes) before / at / after it,
#: and a pattern for its lines in the run log
STAGE_INFO: dict[str, dict[str, Any]] = {
    "probe": {
        "short": "length of each file (ffprobe), where Plex had none",
        "what": "Measures each video's length with ffprobe, for the files Plex did not already measure.",
        "flow": ("plex-index", "scan-local (", "scan-youtube ("),
        "log": r"ffprobe|probe|scan-local",
    },
    "thumbnail": {
        "short": "a small picture fingerprint per episode, to spot look-alikes",
        "what": "Makes a small thumbnail fingerprint of each episode-length video, to find look-alikes.",
        "flow": ("plex-index", "scan-local (", "scan-youtube ("),
        "log": r"thumb",
    },
    "signature": {
        "short": "intro, end-song and frame fingerprints, to place leftover episodes",
        "what": "Fingerprints the intro, end song and sampled frames of a few episodes per season.",
        "flow": ("detect", "signatures", "detect again"),
        "log": r"fingerprint|^\s*\[\d+/\d+\] \d+ per minute",
    },
    "visual_style": {
        "short": "frames sampled per season, to rate talk / produced / cinematic",
        "what": "Rates each season's production style (talk, produced, cinematic) from sampled frames.",
        "flow": ("report", "scan-visual", "plan-channels"),
        "log": r"sampling|^\s*\[\d+/\d+\] \d+ per minute|style",
    },
}


def _steps(lines: list[str]) -> tuple[list[dict[str, Any]], dict[str, Any] | None]:
    """The step list with state, and progress inside the current step."""
    seen: list[tuple[int, str, int]] = []  # (step index, start time, log line)
    for i, ln in enumerate(lines):
        m = _STEP_RE.match(ln)
        if not m:
            continue
        head = m.group(1)
        # longest matching name wins: "detect again …" is step 8, not "detect" (step 6)
        hits = [k for k, (pre, _, _) in enumerate(STEPS) if head.startswith(pre)]
        idx = max(hits, key=lambda k: len(STEPS[k][0])) if hits else None
        if idx is not None:
            t = _TIME_RE.search(head)
            seen.append((idx, t.group(1) if t else "", i))
    done_line = any(ln.startswith("done in ") for ln in lines)
    # "run --from-step X": the steps before X were done by an earlier run, not skipped
    resumed = next((m.group(1) for ln in lines if (m := _RESUME_RE.match(ln))), None)
    # steps that run alongside the others (review pages): running until their end line
    side = {k for k, (pre, _, _) in enumerate(STEPS) if pre in PARALLEL_STEPS}
    main = [x for x in seen if x[0] not in side]
    current = (main[-1][0] if main else seen[-1][0]) if seen and not done_line else None
    started = {idx: t for idx, t, _ in seen}
    side_running = {
        k: at for k, _, at in seen
        if k in side and not done_line
        and not any(ln.startswith(f"{STEPS[k][0]}: finished") for ln in lines[at:])
    }  # fmt: skip
    out = []
    for k, (_, title, what) in enumerate(STEPS):
        later = any(i > k for i in started)  # a later step ran: this one was left out
        state = (
            "running" if k in side_running
            else "done" if k in started and k != current
            else "running" if k == current
            else "earlier" if later and resumed and k < min(started, default=k)
            else "skipped" if later
            else "waiting"
        )  # fmt: skip
        out.append({"n": k + 1, "title": title, "what": what, "state": state,
                    "started": started.get(k, "")})  # fmt: skip
        if k in side_running:
            out[-1]["note"] = _report_note(lines[side_running[k] + 1 :])
    progress = None
    if seen and current is not None:
        _, _, at = seen[-1]
        head = _STEP_RE.match(lines[at]).group(1)  # type: ignore[union-attr]
        total_m = _COUNT_RE.search(head)
        body = lines[at + 1 :]
        n_lib = next((int(m.group(1)) for ln in lines if (m := _COUNT_RE.search(ln))), None)
        name = STEPS[current][0]
        if name in ("detect", "detect again", "scan-youtube --exact", "tag", "report"):

            def mark(ln: str, name: str = name) -> bool:
                """The line a library's result starts with, for this step."""
                if name == "tag":
                    return ln.startswith("=== tags")
                if name == "report":
                    return ln.rstrip().endswith(".html")
                return ln.startswith("=== ")

            n = sum(1 for ln in body if mark(ln))
            prog = {"done": n, "total": n_lib, "unit": "libraries"}
            # inside a large library: "[n/N] name: videos checked" (detect prints these)
            last_lib = max((i for i, ln in enumerate(body) if mark(ln)), default=-1)
            inner = [m for ln in body[last_lib + 1 :] if (m := _INNER_RE.match(ln))]
            if inner:
                m = inner[-1]
                prog["phase"] = (
                    f"now {m.group(3)}: {int(m.group(1)):,} of {int(m.group(2)):,} videos checked"
                )
            return out, prog
        if name == "scan-visual":
            return out, _sampled_progress(body, "visual_style")
        if STEPS[current][0].startswith("scan-local"):
            n = sum(1 for ln in body if ": {'videos'" in ln and ln.count("/") <= 2)
            unit = "libraries"
        elif STEPS[current][0] == "export-plex":
            n, unit = sum(1 for ln in body if ln.startswith("=== ")), "libraries"
        elif STEPS[current][0].startswith("scan-youtube"):
            n = sum(
                1
                for ln in body
                if ": cached (" in ln or (ln.startswith("  ") and ": {'videos'" in ln)
            )
            unit = "channels"
        elif STEPS[current][0] == "plex-index":
            plex_m = [x for ln in body if (x := re.match(r"^\s*\[(\d+)/(\d+)\] ", ln))]
            tot = next((int(x.group(1)) for ln in body
                        if (x := re.match(r"^reading (\d+) Plex libraries", ln))), None)  # fmt: skip
            return out, {"done": int(plex_m[-1].group(1)) if plex_m else 0, "total": tot,
                         "unit": "libraries"}  # fmt: skip
        elif STEPS[current][0] == "group (":
            return out, _counted(body, r"^grouping clips in (\d+) libraries")
        elif STEPS[current][0] == "dizquetv-shows":
            return out, _dz_shows_progress(body, n_lib)
        elif STEPS[current][0] == "signatures":
            return out, _sig_progress(body)
        else:
            return out, _generic_progress(body, STEPS[current][0])
        progress = {"done": n, "total": int(total_m.group(1)) if total_m else None, "unit": unit}
    return out, progress


_DETAILS_RE = re.compile(r"^\s*\[(\d+)/(\d+)\] episode details from Plex")


#: what a step's "[n/N]" lines count
_UNITS = {"check-tags": "series", "check-clips": "questions", "books": "books", "repair-broken": "files", "plan-channels": "channels", "export-dizquetv": "items",
          "audit-channels": "channels"}  # fmt: skip
_PHASE_RE = re.compile(r"^phase: (.+)$")
_N_OF_RE = re.compile(r"^\s*\[(\d+)/(\d+)\] ")


def _generic_progress(body: list[str], step: str) -> dict[str, Any]:
    """Any step: its last "phase: …" line names what it is doing, and the "[n/N]" lines
    after it count that part. A step printing neither says so."""
    last_phase = max((i for i, ln in enumerate(body) if _PHASE_RE.match(ln)), default=-1)
    phase = _PHASE_RE.match(body[last_phase]).group(1) if last_phase >= 0 else None  # type: ignore[union-attr]
    counts = [m for ln in body[last_phase + 1 :] if (m := _N_OF_RE.match(ln))]
    if not counts and phase is None:
        return {"done": 0, "total": None, "unit": "", "phase": "working (this step has no counter)"}
    prog: dict[str, Any] = {
        "done": int(counts[-1].group(1)) if counts else 0,
        "total": int(counts[-1].group(2)) if counts else None,
        "unit": _UNITS.get(step, "") if counts else "",
    }
    if phase:
        prog["phase"] = phase
    return prog


def _counted(body: list[str], total_re: str) -> dict[str, Any]:
    """A step printing "<total line>" then one "[n/N] …" line per library."""
    done = [int(x.group(1)) for ln in body if (x := re.match(r"^\s*\[(\d+)/(\d+)\] ", ln))]
    total = next((int(t.group(1)) for ln in body if (t := re.match(total_re, ln))), None)
    return {"done": done[-1] if done else 0, "total": total, "unit": "libraries"}


def _dz_shows_progress(body: list[str], n_lib: int | None) -> dict[str, Any]:
    """dizqueTV shows, one library at a time: a library is done at its "Backups of
    changed shows" line. Inside one, the shows it will write ("+" lines), the episode
    details fetched from Plex so far, and whether Plex is stalling."""
    ends = [i for i, ln in enumerate(body) if ln.startswith("Backups of changed shows")]
    cur = body[ends[-1] + 1 :] if ends else body
    prog: dict[str, Any] = {"done": len(ends), "total": n_lib, "unit": "libraries"}
    writing = sum(1 for ln in cur if ln.startswith("  +  "))
    details = [m for ln in cur if (m := _DETAILS_RE.match(ln))]
    parts = []
    if writing:
        parts.append(f"now writing {writing} show{'s' if writing != 1 else ''}")
    if details:
        parts.append(f"{int(details[-1].group(1)):,} of {int(details[-1].group(2)):,}"
                     " episode details from Plex")  # fmt: skip
    elif writing:
        parts.append("fetching episode details from Plex")
    tail = [ln for ln in cur if ln.strip()][-1:]
    if tail and "Plex not answering" in tail[0]:
        n = sum(1 for ln in cur if "Plex not answering" in ln)
        parts.append(f"Plex not answering, retrying ({n} so far in this library)")
    if parts:
        prog["phase"] = " · ".join(parts)
    return prog


PARALLEL_STEPS = ("report",)
_THUMBS_RE = re.compile(r"^\s*\[(\d+)/(\d+)\] (.+): thumbnails")


def _report_note(body: list[str]) -> str:
    """Where the review pages are, while they run alongside the other steps."""
    pages = sum(1 for ln in body if ln.rstrip().endswith(".html"))
    last = max((i for i, ln in enumerate(body) if ln.rstrip().endswith(".html")), default=-1)
    th = [m for ln in body[last + 1 :] if (m := _THUMBS_RE.match(ln))]
    now = (f" · now {th[-1].group(3)}: {int(th[-1].group(1)):,} of {int(th[-1].group(2)):,}"
           " thumbnails") if th else ""  # fmt: skip
    return f"alongside the other steps: {pages} pages written{now}"


def _sampled_progress(body: list[str], stage: str) -> dict[str, Any]:
    """A one-pass "sampling N files" step with [n/N] lines (production style)."""
    tot = [int(m.group(1)) for ln in body if (m := re.match(r"^sampling (\d+) files", ln))]
    done = [int(d.group(1)) for ln in body if (d := _SIG_DONE_RE.match(ln))]
    if not tot:
        return {"done": 0, "total": None, "unit": "files", "phase": "choosing what to read"}
    return {"done": done[-1] if done else 0, "total": tot[-1], "unit": "files",
            "left": tot[-1] - (done[-1] if done else 0), "db_stage": stage}  # fmt: skip


def _sig_progress(body: list[str]) -> dict[str, Any]:
    """Progress of the fingerprint step: which pass (season samples, then candidates),
    and done / total inside it, from the pass header and its [n/N] lines."""
    passes = [(i, m) for i, ln in enumerate(body) if (m := _SIG_RE.match(ln))]
    if not passes:
        return {"done": 0, "total": None, "unit": "videos", "phase": "choosing what to read"}
    # the bar covers the whole step: earlier passes count as finished
    ends = [i for i, _ in passes[1:]] + [len(body)]
    done = total = 0
    plist = [{"name": "season samples", "done": 0, "total": None, "state": "waiting"},
             {"name": "candidates", "done": 0, "total": None, "state": "waiting"}]  # fmt: skip
    for k, ((at, m), end) in enumerate(zip(passes, ends, strict=True)):
        n = [int(d.group(1)) for ln in body[at + 1 : end] if (d := _SIG_DONE_RE.match(ln))]
        finished = end < len(body)
        total += int(m.group(2))
        done += int(m.group(2)) if finished else (n[-1] if n else 0)
        if k < len(plist):
            plist[k].update(done=int(m.group(2)) if finished else (n[-1] if n else 0),
                            total=int(m.group(2)), state="done" if finished else "running")  # fmt: skip
    at, m = passes[-1]
    cur = [int(d.group(1)) for ln in body[at + 1 :] if (d := _SIG_DONE_RE.match(ln))]
    return {
        "done": done,
        "total": total,
        "unit": "videos",
        "phase": f"pass {len(passes)} of 2, {m.group(1) or 'videos'}: "
        f"{cur[-1] if cur else 0:,} of {int(m.group(2)):,}",
        "left": int(m.group(2)) - (cur[-1] if cur else 0),
        "passes": plist,
        "db_stage": "signature",
    }


def live(db_path: str, log_path: Path, base: dict[str, Any]) -> dict[str, Any]:
    """The small, fast update pushed about once a second: steps, heartbeat, and the
    current step's progress. The fingerprint step logs every 25 videos, so its count
    adds the results saved since that line (``base`` keeps that baseline)."""
    out: dict[str, Any] = {"now": time.time()}
    if not log_path.exists():
        return out
    lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    out["steps"], prog = _steps(lines)
    out["log_age_s"] = time.time() - log_path.stat().st_mtime
    out["last_line"] = next((ln.strip() for ln in reversed(lines) if ln.strip()), "")
    if prog and prog.get("db_stage") and prog.get("total"):
        stage = prog["db_stage"]
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=5)
        try:
            passes = prog.get("passes") or []
            k = next((i for i, x in enumerate(passes) if x["state"] == "running"), None)
            cur = passes[k] if k is not None else prog
            key = (stage, k, cur["done"])
            if base.get("key") != key:
                base["key"] = key
                base["since"] = conn.execute(
                    "SELECT strftime('%Y-%m-%dT%H:%M:%fZ', 'now')"
                ).fetchone()[0]
            extra = conn.execute(
                "SELECT COUNT(*) FROM index_jobs WHERE stage = ? AND updated_at > ?",
                (stage, base["since"]),
            ).fetchone()[0]
            extra = max(0, min(extra, (cur["total"] or 0) - cur["done"]))
            if cur is not prog:
                cur["done"] += extra
            prog["done"] += extra
            prog["left"] = prog.get("left", 0) - extra
            prog["per_min"] = (
                conn.execute(
                    """SELECT COUNT(*) FROM index_jobs WHERE stage = ?
                       AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-10 minutes')""",
                    (stage,),
                ).fetchone()[0]
                / 10
            )
        finally:
            conn.close()
    out["progress"] = prog
    return out


_QUOTED_RE = re.compile(r"""(['"]).*?\1""")
_PATH_RE = re.compile(
    r"(?:[A-Za-z]:)?[\\/][^\s,;]*|\S+\.(?:mkv|mp4|avi|mov|webm|m4v|ts|wmv|flv|mp3|m4a)\b"
)
_FRAMES_RE = re.compile(r"\b\d+ frames?\b")
_NUM_RE = re.compile(r"\d+(?:[.,]\d+)*")

#: stages that read the NAS for the sampled fingerprints and production style
NAS_READ_STAGES = ("signature", "visual_style")
NAS_READS_AT_ONCE = 2


def normalize_cause(error: str | None) -> str:
    """A short cause key for a failure message: file names, paths and numbers are
    removed, so "read failed: 1 frames" and "read failed: 2 frames" share the key
    "read failed: frames short" while "read failed: tail" stays its own cause."""
    lines = (error or "").strip().splitlines()
    text = lines[0] if lines else ""
    text = _QUOTED_RE.sub("", text)
    text = _PATH_RE.sub("", text)
    text = _FRAMES_RE.sub("frames short", text)
    text = _NUM_RE.sub("", text)
    text = re.sub(r"\s+", " ", text).strip().lower()
    text = re.sub(r"\s+([,:;])", r"\1", text)
    text = re.sub(r"([:,;])(?:\s*[:,;])+", r"\1", text).strip(" :,;")
    return text[:80] or "unknown error"


def attention_groups(rows: list[tuple[Any, ...]]) -> list[dict[str, Any]]:
    """Merge ``(stage, error, count, retrying[, last])`` rows into groups by stage and
    cause, biggest first. ``retrying`` is how many of ``count`` still have tries left;
    ``last`` when one of them last failed (an old failure is from an old run)."""
    groups: dict[tuple[str, str], dict[str, Any]] = {}
    for stage, error, n, retrying, *rest in rows:
        key = (stage, normalize_cause(error))
        g = groups.setdefault(
            key,
            {"stage": stage, "cause": key[1], "count": 0, "retrying": 0, "example": error or "",
             "last": None},
        )  # fmt: skip
        g["count"] += n
        g["retrying"] += retrying or 0
        if rest and rest[0] and (g["last"] is None or rest[0] > g["last"]):
            g["last"] = rest[0]
    out = sorted(groups.values(), key=lambda g: (-g["count"], g["stage"], g["cause"]))
    for g in out:
        g["given_up"] = g["count"] - g["retrying"]
    return out


def retry_failures(db_path: str, stage: str, cause: str) -> int:
    """Set ``attempts = 0`` on the failed rows of ``stage`` whose normalized cause is
    ``cause``, so the next run retries them. Returns the number of rows reset.
    One short transaction per distinct message; waits up to 60 s for the run's writes."""
    conn = sqlite3.connect(db_path, timeout=60)
    try:
        conn.execute("PRAGMA busy_timeout = 60000")
        rows = conn.execute(
            "SELECT DISTINCT error FROM index_jobs WHERE state = 'failed' AND stage = ?", (stage,)
        ).fetchall()
        reset = 0
        for (err,) in rows:
            if normalize_cause(err) != cause:
                continue
            with conn:
                cur = conn.execute(
                    """UPDATE index_jobs SET attempts = 0
                       WHERE state = 'failed' AND stage = ? AND error IS ? AND attempts <> 0""",
                    (stage, err),
                )
                reset += cur.rowcount
        return reset
    finally:
        conn.close()


_smb: dict[str, Any] = {"at": 0.0, "n": None, "busy": False}
_smb_lock = threading.Lock()
_SMB_PS = (
    "$ErrorActionPreference='SilentlyContinue';"
    "@(Get-WinEvent -FilterHashtable @{LogName='Microsoft-Windows-SMBClient/Connectivity';"
    "Id=30809,30807;StartTime=(Get-Date).AddMinutes(-10)}).Count"
)


def _smb_query() -> int | None:
    try:
        r = subprocess.run(  # noqa: S603 — fixed argv
            ["powershell", "-NoProfile", "-NonInteractive", "-Command", _SMB_PS],  # noqa: S607
            capture_output=True, text=True, timeout=45, check=False,
        )  # fmt: skip
        return int(r.stdout.strip().splitlines()[-1])
    except (OSError, ValueError, IndexError, subprocess.SubprocessError):
        return None


def smb_timeouts() -> dict[str, Any]:
    """SMB client timeouts (events 30809, 30807) in the last 10 min, Windows only.
    The event-log query is slow, so it runs in a background thread and its answer is
    reused for 60 s; until the first answer arrives ``n`` is None."""
    if sys.platform != "win32":
        return {"available": False, "n": None}
    with _smb_lock:
        stale = time.time() - _smb["at"] > 60 and not _smb["busy"]
        if stale:
            _smb["busy"] = True

    def work() -> None:
        n = _smb_query()
        with _smb_lock:
            _smb.update(n=n, at=time.time(), busy=False)

    if stale:
        threading.Thread(target=work, daemon=True).start()
    with _smb_lock:
        return {"available": True, "n": _smb["n"], "pending": _smb["at"] == 0.0}


def health(conn: sqlite3.Connection) -> dict[str, Any]:
    """NAS read counts, failures and files done per minute (last hour) from ``index_jobs``."""
    marks = ",".join("?" * len(NAS_READ_STAGES))
    saved, failed = conn.execute(
        f"""SELECT COALESCE(SUM(state = 'done'), 0), COALESCE(SUM(state = 'failed'), 0)
            FROM index_jobs WHERE stage IN ({marks})
            AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-10 minutes')""",  # noqa: S608
        NAS_READ_STAGES,
    ).fetchone()
    per_minute = {
        r[0]: r[1]
        for r in conn.execute(
            """SELECT substr(updated_at, 1, 16), COUNT(*) FROM index_jobs
               WHERE state = 'done'
               AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-60 minutes')
               GROUP BY 1"""
        )
    }
    now = time.time()
    keys = [time.strftime("%Y-%m-%dT%H:%M", time.gmtime(now - 60 * k)) for k in range(59, -1, -1)]
    return {
        "nas": {"at_once": NAS_READS_AT_ONCE, "saved_10m": saved, "failed_10m": failed},
        "smb": smb_timeouts(),
        "per_minute": [per_minute.get(k, 0) for k in keys],
    }


def kind_counts(conn: sqlite3.Connection) -> dict[str, dict[str, int]]:
    """Distinct series per folder and kind (series rows are per season): one grouped query."""
    out: dict[str, dict[str, int]] = {}
    for folder, kind, n in conn.execute(
        """SELECT json_extract(metadata, '$.folder'), json_extract(metadata, '$.kind'),
                  COUNT(DISTINCT COALESCE(json_extract(metadata, '$.series_key'), id))
           FROM series GROUP BY 1, 2"""
    ):
        out.setdefault(folder, {})[kind or "unknown"] = n
    return out


def bucket_keys(now: float, n: int, width_s: int = 600) -> list[str]:
    """The last ``n`` time buckets (oldest first) as ISO prefixes of ``updated_at``:
    10-minute buckets are ``YYYY-MM-DDTHH:M`` (15 characters, the tens digit of the minute)."""
    out = []
    for k in range(n - 1, -1, -1):
        t = time.gmtime(now - width_s * k)
        out.append(time.strftime("%Y-%m-%dT%H:", t) + str(t.tm_min // 10))
    return out


def fill_buckets(counts: dict[tuple[str, str], int], keys: list[str]) -> list[dict[str, Any]]:
    """``{(bucket, state): n}`` laid onto ``keys``: one ``{t, done, failed}`` per bucket."""
    return [
        {"t": k + "0", "done": counts.get((k, "done"), 0), "failed": counts.get((k, "failed"), 0)}
        for k in keys
    ]


def _leaf(folder: str) -> str:
    return folder.replace("\\", "/").rstrip("/").rsplit("/", 1)[-1]


def stage_detail(
    conn: sqlite3.Connection, name: str, log_lines: list[str], now: float | None = None
) -> dict[str, Any] | None:
    """Everything the stage page shows for one per-file stage, or None for an unknown name."""
    if name not in STAGE_INFO:
        return None
    now = now or time.time()
    info = STAGE_INFO[name]
    cond = STAGE_APPLIES.get(name)
    done, failed = conn.execute(
        "SELECT COALESCE(SUM(state = 'done'), 0), COALESCE(SUM(state = 'failed'), 0) "
        "FROM index_jobs WHERE stage = ?",
        (name,),
    ).fetchone()
    note = ""
    selective = {"signature": ("##### signatures", _sig_progress),
                 "visual_style": ("##### scan-visual", lambda b: _sampled_progress(b, name))}  # fmt: skip
    if name in selective:
        # only selected files are read: the waiting count is what the run picked, not every file
        head, parse = selective[name]
        at = next((i for i in range(len(log_lines) - 1, -1, -1)
                   if log_lines[i].startswith(head)), None)  # fmt: skip
        sp = parse(log_lines[at:]) if at is not None else {"total": None}
        total = done + failed + max(0, int(sp.get("left") or 0) if sp.get("total") else 0)
        note = (
            "the files this run picked"
            if sp.get("total")
            else "only selected files are read; chosen when the run reaches this step"
        )
    elif cond:
        total = conn.execute(f"SELECT COUNT(*) FROM files f WHERE {cond}").fetchone()[0]  # noqa: S608
    else:
        total = done + failed
    rate = {
        w: conn.execute(
            "SELECT COUNT(*) FROM index_jobs WHERE stage = ? AND state = 'done' "
            f"AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-{w} minutes')",  # noqa: S608
            (name,),
        ).fetchone()[0]
        / w
        for w in (10, 60)
    }
    counts = {
        (r[0], r[1]): r[2]
        for r in conn.execute(
            """SELECT substr(updated_at, 1, 15), state, COUNT(*) FROM index_jobs
               WHERE stage = ?
               AND updated_at >= strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-370 minutes')
               GROUP BY 1, 2""",
            (name,),
        )
    }
    recent = [
        {"name": Path(r[0]).stem, "library": _leaf(r[2] or ""), "at": r[1]}
        for r in conn.execute(
            """SELECT j.path, j.updated_at, f.folder FROM index_jobs j
               LEFT JOIN files f ON f.path = j.path
               WHERE j.stage = ? AND j.state = 'done' ORDER BY j.updated_at DESC LIMIT 20""",
            (name,),
        )
    ]
    groups = attention_groups(
        [
            (r[0], r[1], r[2], r[3], r[4])
            for r in conn.execute(
                """SELECT stage, error, COUNT(*), SUM(attempts < max_attempts), MAX(updated_at)
                   FROM index_jobs WHERE state = 'failed' AND stage = ? GROUP BY stage, error""",
                (name,),
            )
        ]
    )
    pat = re.compile(info["log"], re.I)
    titles = {pre: title for pre, title, _ in STEPS}
    before, step, after = (titles[k] for k in info["flow"])
    return {
        "key": name,
        "title": STAGE_NAMES.get(name, name),
        "what": info["what"],
        "done": done,
        "failed": failed,
        "total": total,
        "waiting": max(0, total - done - failed),
        "waiting_note": note,
        "rate_10m": rate[10],
        "rate_1h": rate[60],
        "retrying": sum(g["retrying"] for g in groups),
        "given_up": sum(g["given_up"] for g in groups),
        "flow": {"before": before, "step": step, "after": after},
        "buckets": fill_buckets(counts, bucket_keys(now, 36)),
        "recent": recent,
        "groups": groups,
        "log": [ln for ln in log_lines if pat.search(ln)][-30:],
        "now": now,
    }


def group_series(
    rows: list[tuple[str, str, str]], episodes: dict[str, int], collections: dict[str, str]
) -> list[dict[str, Any]]:
    """Fold per-season ``series`` rows ``(id, name, metadata JSON)`` into one entry per
    series key, with its seasons, episode counts, air span and Plex collection titles."""
    by_key: dict[str, dict[str, Any]] = {}
    for sid, name, meta_json in rows:
        meta = json.loads(meta_json or "{}")
        key = meta.get("series_key") or name
        g = by_key.setdefault(
            key,
            {"key": key, "name": name, "kind": meta.get("kind") or "unknown", "seasons": [],
             "episodes": 0, "first_air": None, "last_air": None},
        )  # fmt: skip
        n = episodes.get(sid, 0)
        g["seasons"].append(
            {
                "season": meta.get("season"),
                "name": meta.get("season_name"),
                "episodes": n,
                "first_air": meta.get("first_air"),
                "last_air": meta.get("last_air"),
                "collection": collections.get(sid),
            }
        )
        g["episodes"] += n
        for field, pick in (("first_air", min), ("last_air", max)):
            v = meta.get(field)
            if v:
                g[field] = pick(g[field], v) if g[field] else v
    out = list(by_key.values())
    for g in out:
        g["seasons"].sort(key=lambda x: (x["season"] is None, x["season"] or 0))
    return sorted(out, key=lambda g: (-g["episodes"], g["name"]))


_TV_CACHE: dict[str, Any] = {"at": 0.0, "url": None, "shows": None}


def tv_shows(url: str = "http://localhost:8002") -> dict[str, str] | None:
    """dizqueTV custom shows by folded name → name, or None when dizqueTV can't be
    reached. Asked at most once a minute."""
    c = _TV_CACHE
    if c["url"] != url or time.time() - c["at"] > 60:
        from maktaba_pipeline.text.arabic import fold

        try:
            with urllib.request.urlopen(f"{url}/api/shows", timeout=3) as r:  # noqa: S310 — local service
                shows = {fold(x["name"]): x["name"] for x in json.load(r)}
        except (OSError, ValueError, KeyError, TypeError):
            shows = None
        c.update(at=time.time(), url=url, shows=shows)
    out: dict[str, str] | None = c["shows"]
    return out


def tv_names(library: str, g: dict[str, Any], shows: dict[str, str]) -> list[str]:
    """The dizqueTV shows a series appears as: its series show and, for several seasons,
    one per season, named as dizquetv-shows names them."""
    from maktaba_pipeline.text.arabic import fold

    from .export_plex import _title

    count = len(g["seasons"])
    wanted = [_title(library, g["name"], 1, 1)]
    if count > 1:
        wanted += [
            _title(library, g["name"], x["season"] or 0, count, x.get("name")) for x in g["seasons"]
        ]
    return [shows[fold(n)] for n in wanted if fold(n) in shows]


def item_detail(
    conn: sqlite3.Connection,
    folder: str,
    series_key: str | None = None,
    tv: dict[str, str] | None = None,
) -> dict[str, Any] | None:
    """The library page: counts, series with where they appear, stage status and history."""
    head = conn.execute(
        "SELECT COUNT(*), COALESCE(SUM(duration_s >= 180), 0) FROM files WHERE folder = ?",
        (folder,),
    ).fetchone()
    if not head[0]:
        return None
    rows = [
        (r[0], r[1], r[2])
        for r in conn.execute(
            "SELECT id, name, metadata FROM series WHERE json_extract(metadata, '$.folder') = ?",
            (folder,),
        )
    ]
    episodes = dict(
        conn.execute(
            """SELECT e.series_id, COUNT(*) FROM series_episodes e
               JOIN series s ON s.id = e.series_id
               WHERE json_extract(s.metadata, '$.folder') = ? GROUP BY 1""",
            (folder,),
        ).fetchall()
    )
    collections = dict(
        conn.execute(
            """SELECT c.series_id, c.title FROM plex_collections c
               JOIN series s ON s.id = c.series_id
               WHERE json_extract(s.metadata, '$.folder') = ?""",
            (folder,),
        ).fetchall()
    )
    series = group_series(rows, episodes, collections)
    lib = conn.execute(
        "SELECT library FROM series WHERE json_extract(metadata, '$.folder') = ? LIMIT 1", (folder,)
    ).fetchone()
    for g in series:  # None: dizqueTV not reachable, so the page says so instead of "none"
        g["tv"] = tv_names(lib[0], g, tv) if (tv is not None and lib) else None
    kinds: dict[str, int] = {}
    for g in series:
        kinds[g["kind"]] = kinds.get(g["kind"], 0) + 1
    stages: dict[str, dict[str, int]] = {}
    for stage, state, n in conn.execute(
        """SELECT j.stage, j.state, COUNT(*) FROM index_jobs j JOIN files f ON f.path = j.path
           WHERE f.folder = ? GROUP BY 1, 2""",
        (folder,),
    ):
        stages.setdefault(stage, {"done": 0, "failed": 0})[state] = n
    history = [
        {"at": r[0], "stage": r[1], "file": Path(r[2]).name, "state": r[3], "error": r[4]}
        for r in conn.execute(
            """SELECT j.updated_at, j.stage, j.path, j.state, j.error FROM index_jobs j
               JOIN files f ON f.path = j.path WHERE f.folder = ?
               ORDER BY j.updated_at DESC LIMIT 50""",
            (folder,),
        )
    ]
    extra = collections_beyond_seasons(conn, folder)
    return {
        **extra,
        "folder": folder,
        "name": _leaf(folder),
        "files": head[0],
        "long": head[1],
        "seasons": len(rows),
        "series": len(series),
        "ongoing": kinds.get("ongoing", 0),
        "shorts": kinds.get("short-form", 0),
        "kinds": kinds,
        "list": [g for g in series if series_key in (None, g["key"])],
        "focus": series_key,
        "stages": stages,
        "history": history,
    }


def collections_beyond_seasons(conn: sqlite3.Connection, folder: str) -> dict[str, Any]:
    """Topic collections, clip / stand-alone groups and promo links of one library, each
    with the Plex collection written for it (when there is one)."""
    from .group_clips import group_title

    written: dict[str, str] = {}
    with contextlib.suppress(sqlite3.OperationalError):
        written = dict(conn.execute("SELECT series_id, title FROM plex_collections"))
    out: dict[str, Any] = {"topics": [], "clip_groups": [], "promos": []}
    with contextlib.suppress(sqlite3.OperationalError):
        out["topics"] = [
            {"name": name, "items": n, "collection": written.get(tid)}
            for tid, name, n in conn.execute(
                """SELECT t.id, t.name, COUNT(i.path) FROM topic_collections t
                   LEFT JOIN topic_items i ON i.topic_id = t.id WHERE t.folder = ?
                   GROUP BY t.id ORDER BY COUNT(i.path) DESC""",
                (folder,),
            )
        ]
        out["clip_groups"] = [
            {"kind": kind, "basis": basis, "name": group_title(lib, kind, basis, name),
             "items": n, "collection": written.get(gid)}
            for gid, lib, kind, basis, name, n in conn.execute(
                """SELECT g.id, g.library, g.kind, g.basis, g.name, COUNT(i.path)
                   FROM clip_groups g LEFT JOIN clip_group_items i ON i.group_id = g.id
                   WHERE g.folder = ? GROUP BY g.id ORDER BY g.kind, COUNT(i.path) DESC""",
                (folder,),
            )
        ]  # fmt: skip
        out["promos"] = [
            {"title": t, "show": key, "library": lib, "season": se, "evidence": ev}
            for t, key, lib, se, ev in conn.execute(
                """SELECT COALESCE(y.title, f.stem), p.series_key, p.series_library, p.season,
                          p.evidence
                   FROM promo_links p JOIN files f ON f.path = p.path
                   LEFT JOIN yt_videos y ON y.youtube_id = f.youtube_id
                   WHERE p.folder = ? ORDER BY p.series_key""",
                (folder,),
            )
        ]
    return out


_HERE = Path(__file__).parent
#: request path -> (file beside this module, content type). Only these files are served;
#: they are read per request so edits show on reload.
_STATIC: dict[str, tuple[Path, str]] = {
    "/stage": (_HERE / "stage.html", "text/html; charset=utf-8"),
    "/item": (_HERE / "item.html", "text/html; charset=utf-8"),
    "/settings": (_HERE / "settings.html", "text/html; charset=utf-8"),
    "/common.css": (_HERE / "common.css", "text/css; charset=utf-8"),
    "/common.js": (_HERE / "common.js", "text/javascript; charset=utf-8"),
}


_REPORT_CACHE: dict[str, Any] = {"at": 0.0, "dir": None, "files": {}}


def _report_files(reports: Path) -> dict[str, Path]:
    """The review pages and their thumbnails, by path inside the reports folder
    ("x.html", "thumbs/y.jpg"): the only report files served. Listed at most every 30 s."""
    c = _REPORT_CACHE
    if c["dir"] != reports or time.time() - c["at"] > 30:
        files = {}
        if reports.is_dir():
            for f in reports.rglob("*"):
                if f.is_file() and f.suffix in (".html", ".jpg"):
                    files[f.relative_to(reports).as_posix()] = f
        c.update(at=time.time(), dir=reports, files=files)
    out: dict[str, Path] = c["files"]
    return out


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
        applies = STAGE_APPLIES
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
                    "key": stage,
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
                    "key": "visual_style",
                    "stage": STAGE_NAMES["visual_style"],
                    "done": r["d"] or 0,
                    "failed": r["f"] or 0,
                    "of": (r["d"] or 0) + (r["f"] or 0),
                }  # fmt: skip
            )
        unknown = conn.execute("SELECT COUNT(*) FROM files WHERE duration_s IS NULL").fetchone()[0]
        out["totals"]["length not known yet"] = unknown
        out["attention"] = attention_groups(
            [
                (r[0], r[1], r[2], r[3], r[4])
                for r in conn.execute(
                    """SELECT stage, error, COUNT(*), SUM(attempts < max_attempts),
                              MAX(updated_at)
                       FROM index_jobs WHERE state = 'failed' GROUP BY stage, error"""
                )
            ]
        )
        out["health"] = health(conn)
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
    kinds = kind_counts(conn) if has("series") else {}
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
                "kinds": kinds.get(fo, {}),
                "ongoing": kinds.get(fo, {}).get("ongoing", 0),
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
    for st in out["stages"]:
        st["what"] = STAGE_INFO.get(st["key"], {}).get("short", "")
    out["now"] = time.time()
    conn.close()
    if log_path and log_path.exists():
        lines = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
        out["steps"], out["progress"] = _steps(lines)
        out["log_age_s"] = time.time() - log_path.stat().st_mtime
        out["last_line"] = next((ln.strip() for ln in reversed(lines) if ln.strip()), "")
        # fingerprints are made only for selected videos: the step says how many
        sp = _sig_progress(lines[next((i for i in range(len(lines) - 1, -1, -1)
                                       if lines[i].startswith("##### signatures")), 0):])  # fmt: skip
        for st in out["stages"]:
            if st["stage"] == STAGE_NAMES["signature"]:
                left = sp.get("left", 0)
                st["of"] = st["done"] + st["failed"] + max(0, left)
                st["note"] = (
                    sp["phase"]
                    + (
                        ""
                        if "of 2, cand" in sp["phase"]
                        else "; the candidates pass is sized once the season samples are read"
                    )
                    if sp["total"]
                    else "selected after the first season detection"
                )
                prog = out.get("progress")
                if prog and prog.get("phase"):
                    prog["per_min"] = st["per_min"]  # measured from saved results, not the log
        out["log"] = lines[-40:]
        prog = out["progress"]
        if prog and prog["total"] is None and prog["unit"] == "channels":
            prog["total"] = len(out["libraries"])
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
    """The exact ``mediaintel run`` command a page request maps to (no free-form input).

    Folders go last, after ``--``, so none can be read as an option; one that starts
    with '-' is refused outright (ValueError) rather than silently dropped."""
    args = [sys.executable, "-m", "scripts.mediaintel", "--db", db_path, "run"]
    folders = [f for f in body.get("folders") or [] if isinstance(f, str) and f]
    if any(f.startswith("-") for f in folders):
        raise ValueError("A folder cannot start with '-'.")
    if not folders:
        args += ["--from-plex", "--exclude-library", "Movies"]
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
    if folders:
        args += ["--", *folders]
    return args


def _tail(path: Path, n: int) -> list[str]:
    try:
        return path.read_text(encoding="utf-8", errors="replace").splitlines()[-n:]
    except OSError:
        return []


def settings_payload(db_path: str) -> dict[str, Any]:
    """Every settings section for the settings page: its fields, values and defaults."""
    from maktaba_pipeline import settings

    from . import settings_store

    file = settings_store.path_of(db_path)
    out = []
    for key, (cls, title, what) in settings_store.SECTIONS.items():
        error = None
        try:
            changes = settings_store.changed(file, key)
        except settings.SettingsError as e:  # not {section: {setting: value}} at all
            changes, error = {}, str(e)
        # each change on its own: a value this page cannot use (a hand edit) is shown with
        # why and a way back to the default, and the others still apply
        valid: dict[str, Any] = {}
        invalid: dict[str, str] = {}
        for name, value in changes.items():
            try:
                settings.apply(cls, {name: value})
                valid[name] = value
            except settings.SettingsError as e:
                invalid[name] = str(e)
        fields = settings.describe(settings.apply(cls, valid))
        for f in fields:
            if f["name"] in invalid:
                f["invalid"] = invalid[f["name"]]
                f["changed"] = True  # offers "Back to default"
        out.append({"key": key, "title": title, "what": what, "error": error,
                    "unknown": sorted(k for k in invalid if k not in {f["name"] for f in fields}),
                    "fields": fields})  # fmt: skip
    return {"file": str(file), "sections": out}


#: one save at a time: each reads the file, changes its section and replaces it
_SETTINGS_LOCK = threading.Lock()


def save_settings(db_path: str, body: dict[str, Any]) -> dict[str, Any]:
    """Apply ``{"section", "set": {name: value}, "reset": [name, …]}`` to the settings
    file. Every value is checked first; nothing is written when one is refused
    (``SettingsError``)."""
    from maktaba_pipeline import settings

    from . import settings_store

    key = body.get("section")
    if key not in settings_store.SECTIONS:
        raise settings.SettingsError("section", f"no such section {key!r}")
    set_ = body.get("set") or {}
    reset = body.get("reset") or []
    if not isinstance(set_, dict) or not isinstance(reset, list):
        raise settings.SettingsError("body", 'send {"section", "set": {...}, "reset": [...]}')
    file = settings_store.path_of(db_path)
    cls = settings_store.SECTIONS[key][0]
    names = {f.name for f in dataclasses.fields(cls)}
    with _SETTINGS_LOCK:
        before = settings_store.changed(file, key)
        # a name that is no longer a setting can never apply: it is dropped, not kept
        kept = {k: v for k, v in before.items() if k not in reset and k in names}
        cfg = settings.apply(cls, {**kept, **set_})
        written = settings.save(file, key, cfg)
    return {"section": key, "changed": sorted(written),
            "dropped": sorted(k for k in before if k not in names)}  # fmt: skip


_LOCAL_HOSTS = ("127.0.0.1", "localhost")


def request_problem(method: str, headers: Any, port: int) -> str | None:
    """Why a request must be refused, or None. The server is reachable only from this
    machine, but any web page open in a browser here can still send it requests:
    a Host other than ours means DNS rebinding, an Origin other than ours means a
    cross-site request, and a POST that is not JSON is a CORS "simple" request that
    skipped the preflight. ``headers`` is anything with ``.get`` (an HTTPMessage)."""
    allowed = {f"{h}:{port}" for h in _LOCAL_HOSTS}
    if (headers.get("Host") or "").lower() not in allowed:
        return "Unexpected Host header."
    origin = headers.get("Origin")
    if origin is not None and origin.lower() not in {f"http://{a}" for a in allowed}:
        return "Cross-origin requests are not allowed."
    if method == "POST":
        ctype = (headers.get("Content-Type") or "").split(";", 1)[0].strip().lower()
        if ctype != "application/json":
            return "Send the body as application/json."
    return None


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
    # absolute: the runs and the preview start in pipeline/, wherever the server started
    db_path = str(Path(db_path).resolve())
    reports = reports.resolve()
    log = log_path or Path(db_path + ".run.log")
    cache: dict[str, Any] = {"at": 0.0, "st": {}}
    cache_lock = threading.Lock()
    detail_cache: dict[tuple[str, str, str | None], tuple[float, dict[str, Any] | None]] = {}
    pipeline_dir = Path(__file__).resolve().parents[2]
    #: the settings page's "Preview channels": plan-channels into reports/preview (no writes)
    preview: dict[str, Any] = {"proc": None}
    preview_lock = threading.Lock()

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

        def _detail(self, path: str, qs: dict[str, list[str]]) -> None:
            """/api/stage?name= and /api/item?folder=[&series=], each cached for 10 s."""
            arg = (qs.get("name" if path == "/api/stage" else "folder") or [""])[0]
            extra = (qs.get("series") or [""])[0] or None if path == "/api/item" else None
            key = (path, arg, extra)
            with cache_lock:
                hit = detail_cache.get(key)
                if hit and time.time() - hit[0] < 10:
                    data = hit[1]
                else:
                    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True, timeout=30)
                    try:
                        if path == "/api/stage":
                            lines = (
                                log.read_text(encoding="utf-8", errors="replace").splitlines()
                                if log.exists()
                                else []
                            )
                            data = stage_detail(conn, arg, lines)
                        else:
                            data = item_detail(conn, arg, extra, tv_shows())
                    finally:
                        conn.close()
                    detail_cache[key] = (time.time(), data)
            if data is None:
                self._json(404, {"error": "Unknown stage or library."})
            else:
                self._json(200, {**data, "running_pid": _running_pid(db_path)})

        def _refused(self, method: str) -> bool:
            """Answer 403 and return True when the request is not from our own page."""
            problem = request_problem(method, self.headers, port)
            if problem:
                self._json(403, {"error": problem})
            return problem is not None

        def do_GET(self) -> None:  # noqa: N802 — http.server API
            if self._refused("GET"):
                return
            url = urllib.parse.urlsplit(self.path)
            qs = urllib.parse.parse_qs(url.query)
            if url.path == "/api/status":
                with cache_lock:  # one computation at a time; reused for 10 s
                    if time.time() - cache["at"] > 10:
                        cache["st"], cache["at"] = status(db_path, log), time.time()
                    st = dict(cache["st"])
                st["running_pid"] = _running_pid(db_path)
                self._json(200, st)
            elif url.path == "/api/live":
                # Server-Sent Events: one small update a second, until the page closes
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                base: dict[str, Any] = {}
                try:
                    while True:
                        ev = json.dumps(live(db_path, log, base), ensure_ascii=False)
                        self.wfile.write(f"data: {ev}\n\n".encode())
                        self.wfile.flush()
                        time.sleep(1)
                except OSError:  # the page went away
                    return
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
            elif url.path.startswith("/reports/") and not url.path.startswith("/reports/thumbs/"):
                # only files listed from the reports folder, looked up by name: a request
                # cannot name a path of its own
                name = urllib.parse.unquote(url.path[len("/reports/") :])
                target = _report_files(reports).get(name)
                if target is None:
                    self.send_error(404)
                    return
                ctype = "text/html; charset=utf-8" if target.suffix == ".html" else "image/jpeg"
                self._send(200, target.read_bytes(), ctype)
            elif url.path.startswith(("/thumbs/", "/reports/thumbs/")):
                # the app's thumbnail cache: a fixed name pattern, never a path
                name = url.path.rsplit("/", 1)[-1]
                if not re.fullmatch(r"[0-9a-f]{16}\.jpg", name):
                    self.send_error(404)
                    return
                thumbs = os.path.normpath(Path(db_path).parent / "thumbs")
                f = os.path.normpath(os.path.join(thumbs, name))
                if not f.startswith(thumbs + os.sep) or not os.path.isfile(f):
                    self.send_error(404)
                    return
                self._send(200, Path(f).read_bytes(), "image/jpeg")
            elif url.path == "/api/settings":
                self._json(200, settings_payload(db_path))
            elif url.path == "/preview/channel-plan.html":
                plan_page = reports / "preview" / "channel-plan.html"
                if plan_page.is_file():
                    self._send(200, plan_page.read_bytes(), "text/html; charset=utf-8")
                else:
                    self.send_error(404, "No preview yet: press Preview channels.")
            elif url.path == "/api/plan-preview":
                self._json(200, {"running": preview["proc"] is not None
                                 and preview["proc"].poll() is None,
                                 "ready": (reports / "preview" / "channel-plan.html").is_file(),
                                 "log": _tail(reports / "preview" / "plan.log", 30)})  # fmt: skip
            elif url.path == "/api/reports":
                names = sorted(f.name for f in reports.glob("*.html")) if reports.exists() else []
                self._json(200, {"reports": names})
            elif url.path in ("/", "/index.html"):
                self._send(200, PAGE.encode(), "text/html; charset=utf-8")
            elif url.path in _STATIC:  # pages and assets beside this module
                file, ctype = _STATIC[url.path]
                self._send(200, file.read_bytes(), ctype)
            elif url.path in ("/api/stage", "/api/item"):
                self._detail(url.path, qs)
            else:
                self.send_error(404)

        def do_POST(self) -> None:  # noqa: N802
            if self._refused("POST"):
                return
            n = int(self.headers.get("Content-Length") or 0)
            try:
                body = json.loads(self.rfile.read(n) or b"{}")
            except ValueError:
                body = None
            if not isinstance(body, dict):
                self._json(400, {"error": "The request body must be a JSON object."})
                return
            if self.path == "/api/run":
                if _running_pid(db_path):
                    self._json(409, {"error": "A run is already in progress. Stop it first."})
                    return
                try:
                    args = _run_args(db_path, body, dizquetv_db, reports)
                except ValueError as e:
                    self._json(400, {"error": str(e)})
                    return
                env = {**os.environ, "PYTHONIOENCODING": "utf-8", "PYTHONUNBUFFERED": "1"}
                with log.open("w", encoding="utf-8") as fh:
                    subprocess.Popen(  # noqa: S603 — fixed argv built by _run_args
                        args, cwd=pipeline_dir, stdout=fh, stderr=subprocess.STDOUT, env=env
                    )
                self._json(202, {"started": True, "command": args[3:]})
            elif self.path == "/api/retry":
                stage, cause = body.get("stage"), body.get("cause")
                if not isinstance(stage, str) or not isinstance(cause, str) or not stage:
                    self._json(400, {"error": 'Send {"stage": "...", "cause": "..."}.'})
                    return
                try:
                    reset = retry_failures(db_path, stage, cause)
                except sqlite3.Error as e:
                    self._json(503, {"error": f"The database is busy: {e}"})
                    return
                with cache_lock:
                    cache["at"] = 0.0  # show the new counts on the next poll
                self._json(200, {"reset": reset})
            elif self.path == "/api/stop":
                pid = _running_pid(db_path)
                if not pid:
                    self._json(409, {"error": "No run is in progress."})
                    return
                os.kill(pid, signal.SIGTERM)  # TerminateProcess on Windows; finished work is saved
                self._json(200, {"stopped": pid})
            elif self.path == "/api/settings":
                from maktaba_pipeline import settings

                try:
                    self._json(200, save_settings(db_path, body))
                except settings.SettingsError as e:
                    self._json(400, {"error": str(e), "field": e.field})
            elif self.path == "/api/plan-preview":
                # one check-and-launch at a time: two clicks never start two planners
                with preview_lock:
                    if preview["proc"] is not None and preview["proc"].poll() is None:
                        self._json(409, {"error": "A preview is already being planned."})
                        return
                    out = reports / "preview"
                    out.mkdir(parents=True, exist_ok=True)
                    args = [sys.executable, "-m", "scripts.mediaintel", "--db", db_path,
                            "plan-channels", "--out", str(out)]  # fmt: skip
                    env = {**os.environ, "PYTHONIOENCODING": "utf-8", "PYTHONUNBUFFERED": "1"}
                    with (out / "plan.log").open("w", encoding="utf-8") as fh:
                        preview["proc"] = subprocess.Popen(  # noqa: S603 — fixed argv
                            args, cwd=pipeline_dir, stdout=fh, stderr=subprocess.STDOUT, env=env
                        )
                self._json(202, {"started": True})
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
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    server.daemon_threads = True  # open live streams must not keep the process alive
    server.serve_forever()
