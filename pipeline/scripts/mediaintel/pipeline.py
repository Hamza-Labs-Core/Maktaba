"""run: point at folders (or at Plex) and do every step for them.

scan-local → scan-plex → scan-youtube → link → detect → (tag, scan-visual when
those modules are present) → report. Each step is cached, so re-running only
does new work; nothing is written to Plex (``export-plex`` stays a separate,
reviewed step).
"""

from __future__ import annotations

import importlib
import os
import sqlite3
import threading
import time
import traceback
from pathlib import Path

from . import db, detect, link, report, scan_local, scan_plex

#: the steps ``run --from-step`` can start at, in order
STEPS = (
    "scan", "plex", "youtube", "link", "detect", "signatures", "exact", "tag", "group", "export-plex",
    "dizquetv-shows", "report", "scan-visual", "plan-channels", "export-dizquetv", "audit-channels",
)  # fmt: skip


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
    from_step: str = "scan",
    skip: tuple[str, ...] = (),
    clear_channels: bool = False,
) -> None:
    """``skip``: steps to leave out (e.g. ``scan-visual`` while the NAS is slow).
    ``from_step``: start at that step (see :data:`STEPS`); earlier steps are left as the
    cache holds them, e.g. to finish a stopped run without listing and detecting again.
    ``plex``: write the season collections (``export-plex --apply --replace-existing``;
    older collections are listed as superseded, never deleted). ``tv``: update dizqueTV
    custom shows (``dizquetv-shows --apply``). Both log every change with its diff.
    ``clear_channels``: the channel export also deletes every dizqueTV channel that is
    not in the plan (a fresh lineup), not only the planner's own."""
    t0 = time.monotonic()
    pid_file = Path(conn.execute("PRAGMA database_list").fetchone()[2] + ".run.pid")
    pid_file.write_text(str(os.getpid()), encoding="utf-8")  # lets `serve` show / stop it
    try:
        _run(conn, roots, server=server, path_map=path_map, youtube=youtube, exact=exact,
             lang=lang, delay_s=delay_s, report_dir=report_dir, workers=workers, plex=plex,
             tv=tv, tv_url=tv_url, backup_dir=backup_dir, t0=t0, from_step=from_step, skip=skip,
             clear_channels=clear_channels)  # fmt: skip
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
    from_step: str = "scan",
    skip: tuple[str, ...] = (),
    clear_channels: bool = False,
) -> None:
    if from_step not in STEPS:
        raise ValueError(f"--from-step must be one of {', '.join(STEPS)}")
    if bad := [s for s in skip if s not in STEPS]:
        raise ValueError(f"--skip {bad[0]!r}: must be one of {', '.join(STEPS)}")
    if skip:
        print(f"leaving out: {', '.join(skip)}")

    def want(step: str) -> bool:
        return step not in skip and STEPS.index(step) >= STEPS.index(from_step)

    folders = [r.as_posix() for r in roots]
    if from_step != "scan":
        print(f"starting at step {from_step!r}: earlier steps keep what the cache holds")
    if server is not None and want("scan"):
        # list files first, let Plex supply length/size, then probe only the rest
        _step(f"scan-local, listing ({len(roots)} folders)")
        scan_local.scan_roots(conn, roots, workers=workers, probe=False)
    if server is not None and want("plex"):
        # its own step: every Plex item's record is saved, so later steps don't ask Plex
        _step("plex-index (Plex items and their details)")
        scan_plex.scan_plex(conn, server, path_map, None)
    if want("scan"):
        _step(f"scan-local ({len(roots)} folders)")
        scan_local.scan_roots(
            conn, roots, workers=4, parallel=2
        )  # 8 reads at once: the NAS slows under more
    if youtube and want("youtube"):
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
    if want("link"):
        _step("link")
        link.link(conn)
    if want("detect"):
        _step("detect")
        for f in folders:
            detect.run(conn, f)
    from . import signatures

    if want("signatures"):
        _step("signatures (samples of seasons with unplaced videos, then those videos)")
    if want("signatures") and signatures.run(conn, folders, workers=2):
        _step("detect again with fingerprints: place leftover episodes")
        for f in folders:
            detect.run(conn, f)
    if youtube and exact and want("exact"):
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
    if want("tag"):
        try:
            tags = importlib.import_module(f"{__package__}.classify_tags")
        except ImportError:
            tags = None
        if tags is not None:
            _step("tag")
            for f in folders:
                tags.run(conn, f)
    if want("group"):
        from . import group_clips

        # what detection left over: clips and stand-alone videos into collections, promos
        # linked to the show they advertise
        _step("group (clips, stand-alone videos, promos)")
        group_clips.run(conn, folders=folders)
    libraries = sorted(
        {
            lib
            for f in folders
            for (lib,) in conn.execute(
                "SELECT DISTINCT library FROM series WHERE json_extract(metadata, '$.folder') LIKE ?"
                " UNION SELECT DISTINCT library FROM clip_groups WHERE folder LIKE ?",
                (f"%{f}%", f"%{f}%"),
            )
        }
    )
    if plex and server is not None and want("export-plex"):
        from . import export_plex

        _step(f"export-plex ({len(libraries)} libraries)")
        for lib in libraries:
            export_plex.export(
                conn, server, apply=True, library_filter=lib, include_clips=False,
                replace_existing=True, remove_superseded=False, diff=True,
            )  # fmt: skip
        print("Plex apps keep their old library view: restart the Plex app (or refresh "
              "the library) to see the new collections", flush=True)  # fmt: skip
    if tv and server is not None and want("dizquetv-shows"):
        from . import dizquetv_shows

        _step(f"dizquetv-shows ({len(libraries)} libraries)")
        for lib in libraries:
            print(f"=== {lib}", flush=True)
            dizquetv_shows.run(
                conn, server, url=tv_url, apply=True, library_filter=lib,
                backup_dir=backup_dir / "dizquetv-shows",
            )  # fmt: skip
    # review pages read thumbnails from the share (cached once): they run alongside the
    # remaining steps on their own connection, never holding them up
    side: threading.Thread | None = None
    if want("report"):
        _step("report (alongside the next steps)")
        db_file = next(r[2] for r in conn.execute("PRAGMA database_list") if r[1] == "main")

        def pages() -> None:
            c2 = db.connect(db_file)
            try:
                for f in folders:
                    report.run(c2, f, report_dir)
            except Exception:  # noqa: BLE001 — logged; the main steps go on
                traceback.print_exc()
            finally:
                c2.close()
                print(f"report: finished ({time.strftime('%H:%M:%S')})", flush=True)

        side = threading.Thread(target=pages, name="report", daemon=True)
        side.start()
    # production style feeds only channel planning, so Plex and dizqueTV don't wait for it
    if want("scan-visual"):
        try:
            sv = importlib.import_module(f"{__package__}.scan_visual")
        except ImportError:
            sv = None
        if sv is not None:
            _step("scan-visual")
            # all seasons in one pool; reads are capped inside, decoding uses the cores
            sv.run(conn, None, workers=os.cpu_count() or 8)
    if want("plan-channels"):
        _step("plan-channels (whole cache)")
        from . import plan_channels

        plan_channels.run(conn, report_dir)
    if tv and server is not None and want("export-dizquetv"):
        from . import export_dizquetv

        _step("export-dizquetv (channels)")
        # the planner's own channels and lists the new plan no longer has go (--prune)
        export_dizquetv.run(
            report_dir / "channel-plan.json",
            url=tv_url,
            apply=True,
            server=server,
            conn=conn,
            prune=True,
            clear_channels=clear_channels,
        )
    if tv and want("audit-channels"):
        from . import audit_channels

        _step("audit-channels (is each channel's content about the channel?)")
        try:
            audit_channels.run(conn, tv_url, report_dir)
        except OSError as e:  # dizqueTV not running: the audit is a report, never a failure
            print(f"  -  skipped: dizqueTV not reachable at {tv_url} ({e})")
    if side is not None and side.is_alive():
        print("waiting for the review pages to finish", flush=True)
        side.join()
    print(f"\ndone in {(time.monotonic() - t0) / 60:.1f} min")
