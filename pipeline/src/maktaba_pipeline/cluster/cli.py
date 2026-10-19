"""``maktaba-pipeline cluster …``: the cluster's files and this machine's agents.

- ``cluster where``: the cluster directory here, how it was found, on the share or not.
- ``cluster ls``: the machines' folders, their processes and the days present.
- ``cluster status``: the agent of every worker on this machine (from their local
  status files): connected or not, to which home, where their files go.
- ``cluster logs|metrics|llm|jobs|events|queues|library [--ip IP] [--date D] [--level L]
  [--follow] [--json]``: reads the files on the share directly (design-agent.md §4),
  merging a day's per-process files by timestamp; without ``--ip`` it lists the machines
  and the days they have. ``events``, ``queues`` and ``library`` are the home hub's files.

The cluster directory: ``--dir``, else ``MAKTABA_CLUSTER_DIR``, else the one this
machine's running workers use (their status files), else the share found from the
database (``--database-url`` / ``MAKTABA_DATABASE_URL`` / the node config) with this
machine's path map (``--path-map`` / ``MAKTABA_WORKER_PATH_MAP``).
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import heapq
import json
import os
import sys
import time
from collections.abc import Iterable, Iterator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, TextIO

from ..engine.paths import PathMap
from . import safefs, share

__all__ = ["READERS", "add_parser", "main", "merged_records"]

READERS = ("logs", "metrics", "llm", "jobs", "events", "queues", "library")
_LEVEL_RANK = {"debug": 10, "info": 20, "warning": 30, "warn": 30, "error": 40, "critical": 50}


def add_parser(subparsers: Any) -> None:
    p = subparsers.add_parser("cluster", help="The cluster's files and this machine's agents.")
    p.add_argument(
        "what",
        choices=("where", "ls", "status", *READERS),
        help="where: the cluster directory; ls: machines, processes, days; status: this "
        "machine's agents; or the records of one kind.",
    )
    p.add_argument("--ip", default=None, help="The machine (its folder name).")
    p.add_argument("--date", default=None, help="YYYY-MM-DD (UTC); default today.")
    p.add_argument("--level", default=None, help="logs: this level and above.")
    p.add_argument("--follow", "-f", action="store_true", help="Keep reading new records.")
    p.add_argument("--json", action="store_true", help="Print the records as JSON lines.")
    p.add_argument("--dir", default=None, help="The cluster directory (a local path).")
    p.add_argument("--database-url", default=os.getenv("MAKTABA_DATABASE_URL") or None)
    p.add_argument("--path-map", default=os.getenv("MAKTABA_WORKER_PATH_MAP") or None)


# --- finding the directory --------------------------------------------------------------


def _status_files() -> list[dict[str, Any]]:
    from .agent.service import status_dir

    out: list[dict[str, Any]] = []
    try:
        paths = sorted(status_dir().glob("*.json"))
    except OSError:
        return out
    for p in paths:
        try:
            data = json.loads(p.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            continue
        if isinstance(data, dict):
            data["_path"] = str(p)
            out.append(data)
    return out


async def _from_database(url: str | None, path_map: str | None) -> Path | None:
    from .config import load as load_node

    node = None if url else load_node()
    if not url and node is not None and node.joined:
        url = node.database_url
    if not url:
        return None
    from ..runtime import Database

    db = await Database.connect(url)
    try:
        pm = PathMap.parse(path_map)
        if not pm.entries and node is not None:
            row = await db.fetchrow(
                "SELECT path_map FROM cluster_nodes WHERE id = $1", node.node_id
            )
            if row is not None and row["path_map"]:
                raw = row["path_map"]
                pm = PathMap.parse(raw if isinstance(raw, str) else json.dumps(raw))
        found, _ = await share.resolve_cluster_dir(db, pm)
        return found
    finally:
        await db.close()


def find_dir(args: argparse.Namespace) -> Path | None:
    return locate(args)[0]


def locate(args: argparse.Namespace) -> tuple[Path | None, str]:
    """``(the cluster directory, how it was found)``."""
    if args.dir:
        return Path(args.dir).expanduser(), "--dir"
    env = os.environ.get(share.ENV_CLUSTER_DIR)
    if env:
        return Path(env).expanduser(), share.ENV_CLUSTER_DIR
    for st in sorted(_status_files(), key=lambda s: str(s.get("updated_at")), reverse=True):
        files = st.get("files") or {}
        d = files.get("dir") if isinstance(files, dict) else None
        if d and files.get("on_share"):
            # the status names the machine's folder
            return Path(d).parent, f"the agent of {st.get('worker_id')} writes there"
    try:
        found = asyncio.run(_from_database(args.database_url, args.path_map))
    except Exception as exc:  # noqa: BLE001 - reported to the user
        return None, f"cannot find the share from the database: {exc}"
    return found, "the library roots in the database, through this machine's path map"


def where(args: argparse.Namespace, out: TextIO | None = None) -> int:
    out = out or sys.stdout
    root, how = locate(args)
    if root is None:
        out.write(f"no cluster directory ({how})\n")
        return 1
    out.write(f"cluster directory: {root}\n  found by: {how}\n")
    out.write(f"  readable: {'yes' if root.is_dir() else 'no'}\n")
    for st in _status_files():
        files = st.get("files") or {}
        if isinstance(files, dict):
            note = (
                "on the share"
                if files.get("on_share")
                else (f"LOCAL ({files.get('reason') or 'share not reachable'})")
            )
            out.write(f"  {st.get('worker_id')} writes to {files.get('dir')}: {note}\n")
    return 0


def ls(root: Path, out: TextIO | None = None) -> int:
    """The IP folders, their processes and the days present per kind."""
    out = out or sys.stdout
    if not root.is_dir():
        sys.stderr.write(f"cannot read {root}: not a directory\n")
        return 1
    machines = list(safefs.list_real_dirs(root))
    out.write(f"{root}: {len(machines)} machine folder(s)\n")
    for m in machines:
        out.write(f"  {m.name}\n")
        for p in safefs.list_real_files(m / "processes", ".json"):
            try:
                with safefs.open_read(p) as fh:
                    ident = json.load(fh)
            except (OSError, ValueError):
                ident = {}
            state = "stopped" if ident.get("stopped_at") else f"last seen {ident.get('last_seen')}"
            out.write(
                f"    process {p.stem}: {ident.get('kind', '?')} pid {ident.get('pid', '?')}"
                f" on {ident.get('host', '?')} ({state})\n"
            )
        for kind in READERS:
            days = [d.name for d in safefs.list_real_dirs(m / kind)]
            if days:
                more = f" (+{len(days) - 5} earlier)" if len(days) > 5 else ""
                out.write(f"    {kind}: {', '.join(days[-5:])}{more}\n")
    return 0


# --- reading ---------------------------------------------------------------------------------


def _day(date: str | None) -> str:
    if date:
        datetime.strptime(date, "%Y-%m-%d")
        return date
    return datetime.now(UTC).strftime("%Y-%m-%d")


def _read_lines(path: Path) -> Iterator[dict[str, Any]]:
    try:
        fh = safefs.open_read(path)  # never through a link
    except OSError:
        return
    with fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if isinstance(rec, dict):
                rec.setdefault("_file", path.stem)
                yield rec


def merged_records(day_dir: Path) -> Iterator[dict[str, Any]]:
    """A day's per-process files, merged by ``ts`` (each file is in order already);
    links are skipped, never followed."""
    streams = [_read_lines(f) for f in safefs.list_real_files(day_dir)]
    return heapq.merge(*streams, key=lambda r: str(r.get("ts", "")))


def _keep(rec: dict[str, Any], level: str | None) -> bool:
    if not level:
        return True
    want = _LEVEL_RANK.get(level.lower(), 0)
    return _LEVEL_RANK.get(str(rec.get("level", "info")).lower(), 20) >= want


def _fmt(what: str, rec: dict[str, Any], as_json: bool) -> str:
    if as_json:
        return json.dumps(rec, separators=(",", ":"), ensure_ascii=False, default=str)
    src = rec.pop("_file", "")
    ts = str(rec.get("ts", ""))
    if what == "logs":
        fields = rec.get("fields") or {}
        tail = " ".join(f"{k}={v}" for k, v in fields.items()) if isinstance(fields, dict) else ""
        level = str(rec.get("level", "")).upper()
        return f"{ts} {level:<7} [{src}] {rec.get('event', '')} {tail}".rstrip()
    if what == "jobs":
        extra = rec.get("progress") or rec.get("outcome") or rec.get("output") or ""
        dur = f" {rec['duration_ms']} ms" if rec.get("duration_ms") is not None else ""
        return (
            f"{ts} [{src}] {rec.get('event', ''):<8} job {rec.get('job_id')} "
            f"{rec.get('stage', '')} {rec.get('title') or rec.get('subject_id') or ''}"
            f"{dur} {json.dumps(extra, default=str) if extra else ''}"
        ).rstrip()
    body = {k: v for k, v in rec.items() if k not in ("ts", "t")}
    return f"{ts} [{src}] {json.dumps(body, separators=(',', ':'), default=str)}"


def _list(root: Path, what: str, out: TextIO) -> int:
    if not root.is_dir():
        sys.stderr.write(f"cannot read {root}: not a directory\n")
        return 1
    machines = list(safefs.list_real_dirs(root))
    if not machines:
        out.write(f"no machines in {root}\n")
        return 0
    out.write(f"{root}\n")
    for m in machines:
        days = [p.name for p in safefs.list_real_dirs(m / what)]
        procs = len(list(safefs.list_real_files(m / "processes", ".json")))
        shown = ", ".join(days[-7:]) if days else "-"
        more = f" (+{len(days) - 7} earlier)" if len(days) > 7 else ""
        out.write(f"  {m.name:<18} {procs} process(es)  {what}: {shown}{more}\n")
    out.write("pick one with --ip\n")
    return 0


def show(
    root: Path,
    what: str,
    *,
    ip: str | None,
    date: str | None,
    level: str | None = None,
    follow: bool = False,
    as_json: bool = False,
    out: TextIO | None = None,
    poll_sec: float = 1.0,
    max_polls: int | None = None,
) -> int:
    out = out or sys.stdout
    if ip is None:
        return _list(root, what, out)
    day = _day(date)
    day_dir = root / share.folder_name(ip) / what / day
    chain = (root / share.folder_name(ip), root / share.folder_name(ip) / what, day_dir)
    if any(p.exists() or p.is_symlink() for p in chain) and not all(
        safefs.real_dir(p) for p in chain if p.exists() or p.is_symlink()
    ):
        sys.stderr.write(f"refusing {day_dir}: a link or not a directory\n")
        return 1
    printed = 0
    for rec in merged_records(day_dir):
        if _keep(rec, level):
            out.write(_fmt(what, rec, as_json) + "\n")
            printed += 1
    if not follow:
        if printed == 0:
            sys.stderr.write(f"nothing in {day_dir}\n")
        return 0
    out.flush()
    return _follow(day_dir, what, level, as_json, out, poll_sec, max_polls)


def _follow(
    day_dir: Path,
    what: str,
    level: str | None,
    as_json: bool,
    out: TextIO,
    poll_sec: float,
    max_polls: int | None,
) -> int:
    """Tail every file of the day, new files included."""
    offsets: dict[Path, int] = {}
    for f in _glob(day_dir):
        with contextlib.suppress(OSError):
            offsets[f] = f.stat().st_size
    polls = 0
    try:
        while max_polls is None or polls < max_polls:
            polls += 1
            time.sleep(poll_sec)
            fresh: list[dict[str, Any]] = []
            for f in _glob(day_dir):
                start = offsets.get(f, 0)
                try:
                    with safefs.open_read(f) as fh:
                        fh.seek(start)
                        chunk = fh.read()
                except OSError:
                    continue
                complete, _, _rest = chunk.rpartition("\n")
                if not complete and not chunk.endswith("\n"):
                    continue  # a half-written line: next time
                offsets[f] = start + len(complete.encode("utf-8")) + 1
                for line in complete.splitlines():
                    try:
                        rec = json.loads(line)
                    except ValueError:
                        continue
                    if isinstance(rec, dict):
                        rec.setdefault("_file", f.stem)
                        fresh.append(rec)
            fresh.sort(key=lambda r: str(r.get("ts", "")))
            for rec in fresh:
                if _keep(rec, level):
                    out.write(_fmt(what, rec, as_json) + "\n")
            out.flush()
    except KeyboardInterrupt:
        return 0
    return 0


def _glob(d: Path) -> Iterable[Path]:
    return list(safefs.list_real_files(d))


def status(out: TextIO | None = None) -> int:
    out = out or sys.stdout
    found = _status_files()
    if not found:
        out.write("no agent has run on this machine (no status files)\n")
        return 1
    for st in found:
        files = st.get("files") or {}
        where = files.get("dir") if isinstance(files, dict) else None
        share_note = ""
        if isinstance(files, dict) and not files.get("on_share"):
            share_note = f" (local: {files.get('reason') or 'no share'})"
        out.write(
            f"{st.get('worker_id')} pid {st.get('pid')} {st.get('kind')}: {st.get('state')}"
            f" -> {st.get('url')}"
            + (f" session {st['session']}" if st.get("session") else "")
            + (f"\n    last error: {st['last_error']}" if st.get("last_error") else "")
            + (f"\n    files: {where}{share_note}" if where else "")
            + f"\n    updated {st.get('updated_at')}\n"
        )
    return 0


def main(args: argparse.Namespace) -> int:
    if args.what == "status":
        return status()
    if args.what == "where":
        return where(args)
    root = find_dir(args)
    if root is None:
        sys.stderr.write(
            "no cluster directory: pass --dir, set MAKTABA_CLUSTER_DIR, or give a database\n"
        )
        return 2
    if args.what == "ls":
        return ls(root)
    try:
        return show(
            root,
            args.what,
            ip=args.ip,
            date=args.date,
            level=args.level,
            follow=args.follow,
            as_json=args.json,
        )
    except ValueError as exc:
        sys.stderr.write(f"{exc}\n")
        return 2
