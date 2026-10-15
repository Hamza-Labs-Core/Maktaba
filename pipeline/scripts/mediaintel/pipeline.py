"""run: point at folders (or at Plex) and do every step for them.

scan-local → scan-plex → scan-youtube → link → detect → (tag, scan-visual when
those modules are present) → report. Each step is cached, so re-running only
does new work; nothing is written to Plex (``export-plex`` stays a separate,
reviewed step).
"""

from __future__ import annotations

import importlib
import sqlite3
import time
from pathlib import Path

from . import detect, link, report, scan_local, scan_plex


def _step(name: str) -> float:
    print(f"\n##### {name}  ({time.strftime('%H:%M:%S')})", flush=True)
    return time.monotonic()


def run(
    conn: sqlite3.Connection,
    roots: list[Path],
    *,
    server: scan_plex.PlexServer | None,
    path_map: list[tuple[str, str]],
    youtube: bool = True,
    exact: bool = False,
    lang: str = "ar",
    delay_s: float = 3.0,
    report_dir: Path = Path("reports"),
    workers: int = 8,
    plex: bool = False,
    tv: bool = False,
    tv_url: str = "http://localhost:8002",
    backup_dir: Path = Path("backups"),
) -> None:
    """``plex``: write the season collections (``export-plex --apply --replace-existing``;
    older collections are listed as superseded, never deleted). ``tv``: update dizqueTV
    custom shows (``dizquetv-shows --apply``). Both log every change with its diff."""
    t0 = time.monotonic()
    import os

    pid_file = Path(conn.execute("PRAGMA database_list").fetchone()[2] + ".run.pid")
    pid_file.write_text(str(os.getpid()), encoding="utf-8")  # lets `serve` show / stop it
    try:
        _run(conn, roots, server=server, path_map=path_map, youtube=youtube, exact=exact,
             lang=lang, delay_s=delay_s, report_dir=report_dir, workers=workers, plex=plex,
             tv=tv, tv_url=tv_url, backup_dir=backup_dir, t0=t0)  # fmt: skip
    finally:
        pid_file.unlink(missing_ok=True)


def _run(
    conn: sqlite3.Connection,
    roots: list[Path],
    *,
    server: scan_plex.PlexServer | None,
    path_map: list[tuple[str, str]],
    youtube: bool,
    exact: bool,
    lang: str,
    delay_s: float,
    report_dir: Path,
    workers: int,
    plex: bool,
    tv: bool,
    tv_url: str,
    backup_dir: Path,
    t0: float,
) -> None:
    if server is not None:
        # list files first, let Plex supply length/size, then probe only the rest
        _step(f"scan-local, listing ({len(roots)} folders)")
        scan_local.scan_roots(conn, roots, workers=workers, probe=False)
        _step("scan-plex")
        scan_plex.scan_plex(conn, server, path_map, None)
    _step(f"scan-local ({len(roots)} folders)")
    scan_local.scan_roots(
        conn, roots, workers=4, parallel=2
    )  # 8 reads at once: the NAS slows under more
    folders = [r.as_posix() for r in roots]
    if youtube:
        from . import scan_youtube  # optional yt-dlp extra

        _step("scan-youtube (3 channels at a time)")
        from concurrent.futures import ThreadPoolExecutor

        from .db import connect

        db_path = conn.execute("PRAGMA database_list").fetchone()[2]
        conn.commit()

        def listing(f: str) -> str | None:
            c = connect(db_path)
            try:
                scan_youtube.scan(
                    c, lang=lang, exact=False, refresh=False, only_folder=f, workers=1,
                    delay_s=delay_s, cookies_from_browser=None, exact_scope="series",
                )  # fmt: skip
                return None
            except scan_youtube.BotCheck as e:
                return str(e)
            except Exception as e:  # noqa: BLE001 — one channel must not end the run
                print(f"  ! {f}: YouTube listing failed ({type(e).__name__}: {e}); "
                      "continuing with local evidence", flush=True)  # fmt: skip
                return None
            finally:
                c.close()

        with ThreadPoolExecutor(3) as pool:
            stops = [r for r in pool.map(listing, folders) if r]
        if stops:
            print(f"youtube asked for a bot check ({stops[0]}); continuing with what was fetched")
            youtube = False
    _step("link")
    link.link(conn)
    _step("detect")
    for f in folders:
        detect.run(conn, f)
    from . import signatures

    _step("signatures (samples of seasons with unplaced videos, then those videos)")
    if signatures.run(conn, folders, workers=4):
        _step("detect again with fingerprints")
        for f in folders:
            detect.run(conn, f)
    if youtube and exact:
        from . import scan_youtube

        _step("scan-youtube --exact (series members), then detect again")
        for f in folders:
            try:
                scan_youtube.scan(
                    conn, lang=lang, exact=True, refresh=False, only_folder=f, workers=1,
                    delay_s=delay_s, cookies_from_browser=None, exact_scope="series",
                )  # fmt: skip
            except scan_youtube.BotCheck as e:
                print(f"youtube stopped ({e})")
                break
            detect.run(conn, f)
    for mod, label in (("classify_tags", "tag"), ("scan_visual", "scan-visual")):
        try:
            m = importlib.import_module(f"{__package__}.{mod}")
        except ImportError:
            continue
        _step(label)
        if mod == "scan_visual":
            m.run(conn, None, workers=8)  # all seasons in one pool (one folder at a time idles)
        else:
            for f in folders:
                m.run(conn, f)
    _step("report")
    for f in folders:
        report.run(conn, f, report_dir)
    libraries = sorted(
        {
            lib
            for f in folders
            for (lib,) in conn.execute(
                "SELECT DISTINCT library FROM series WHERE json_extract(metadata, '$.folder') LIKE ?",
                (f"%{f}%",),
            )
        }
    )
    if plex and server is not None:
        from . import export_plex

        _step(f"export-plex ({len(libraries)} libraries)")
        for lib in libraries:
            export_plex.export(
                conn, server, apply=True, library_filter=lib, include_clips=False,
                replace_existing=True, remove_superseded=False, diff=True,
            )  # fmt: skip
    if tv and server is not None:
        from . import dizquetv_shows

        _step("dizquetv-shows")
        for lib in libraries:
            dizquetv_shows.run(
                conn, server, url=tv_url, apply=True, library_filter=lib,
                backup_dir=backup_dir / "dizquetv-shows",
            )  # fmt: skip
    _step("plan-channels (whole cache)")
    from . import plan_channels

    plan_channels.run(conn, report_dir)
    if tv and server is not None:
        from . import export_dizquetv

        _step("export-dizquetv (channels)")
        export_dizquetv.run(report_dir / "channel-plan.json", url=tv_url, apply=True, server=server)
    print(f"\ndone in {(time.monotonic() - t0) / 60:.1f} min")
