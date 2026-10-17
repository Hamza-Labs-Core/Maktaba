"""Small thumbnail copies, cached with the app (``thumbs/`` next to the cache database).

Review pages, the control centre and later Maktaba read the same files, so a thumbnail
is read from the share once. A copy is ``<sha1(source)[:16]>.jpg``, 320 px wide; each
one is an ``index_jobs`` row (stage ``thumb_small``) keyed to the video's size and
mtime, so a changed video gets a new copy and an unchanged one never does.
"""

from __future__ import annotations

import hashlib
import sqlite3
import subprocess
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

from . import jobs
from .bins import FFMPEG

STAGE = "thumb_small"
VERSION = 1


def name(src: str) -> str:
    return hashlib.sha1(src.encode()).hexdigest()[:16] + ".jpg"  # noqa: S324 — a file name


def cache_dir(conn: sqlite3.Connection) -> Path:
    db = next(r[2] for r in conn.execute("PRAGMA database_list") if r[1] == "main")
    return Path(db).parent / "thumbs"


def _copy(src: str, dst: Path) -> str | None:
    tmp = dst.with_suffix(".part.jpg")
    try:
        r = subprocess.run(
            [FFMPEG, "-v", "error", "-nostdin", "-y", "-i", src, "-vf", "scale=320:-2",
             "-frames:v", "1", "-q:v", "5", str(tmp)],
            capture_output=True, timeout=60, check=False,
        )  # fmt: skip
    except subprocess.TimeoutExpired:  # a stalled share: recorded as failed, retried next run
        tmp.unlink(missing_ok=True)
        return "read timed out (60 s)"
    if r.returncode or not tmp.exists() or tmp.stat().st_size == 0:
        tmp.unlink(missing_ok=True)
        return (r.stderr.decode(errors="replace").strip().splitlines() or ["no image"])[-1][:200]
    tmp.replace(dst)  # never a half-written copy under the real name
    return None


def ensure(
    conn: sqlite3.Connection, paths: list[str], *, workers: int = 2, label: str = ""
) -> Path:
    """Make the missing copies for these videos' sidecar thumbnails (at most ``workers``
    share reads at once: the NAS limit). Returns the cache folder."""
    jobs.ensure(conn)
    out = cache_dir(conn)
    out.mkdir(parents=True, exist_ok=True)
    todo: list[tuple[str, str, int, float]] = []
    adopted = 0
    for i in range(0, len(paths), 500):
        part = paths[i : i + 500]
        for path, src, size, mtime in conn.execute(
            "SELECT path, thumb_path, size, mtime FROM files WHERE thumb_path IS NOT NULL"
            f" AND path IN ({','.join('?' * len(part))})",  # noqa: S608
            part,
        ):
            if not jobs.needed(conn, path, STAGE, VERSION, size, mtime):
                continue
            dst = out / name(src)
            if dst.exists() and dst.stat().st_size > 0:  # made before jobs were tracked
                jobs.record(conn, path, STAGE, VERSION, size, mtime)
                adopted += 1
            else:
                todo.append((path, src, size, mtime))
    conn.commit()
    if not todo:
        return out
    print(f"  {label}: {len(todo)} thumbnails to copy ({adopted} already cached)", flush=True)
    with ThreadPoolExecutor(max(1, workers)) as pool:
        futs = {pool.submit(_copy, src, out / name(src)): (p, sz, mt)
                for p, src, sz, mt in todo}  # fmt: skip
        for n, fut in enumerate(as_completed(futs), 1):
            p, sz, mt = futs[fut]
            jobs.record(conn, p, STAGE, VERSION, sz, mt, fut.result())
            if n % 100 == 0 or n == len(todo):
                conn.commit()
                print(f"  [{n}/{len(todo)}] {label}: thumbnails", flush=True)
    conn.commit()
    return out
