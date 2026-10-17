"""mediaintel — gather and organise metadata for a YouTube-archive video library.

    uv run --extra mediaintel python -m scripts.mediaintel <command> ...

Commands (each reads/writes the same SQLite cache, ``--db``):

  run          [FOLDER...] [--from-plex]   every step below for those folders, then the report
  scan-local   ROOT...        files, durations, thumbnails (dHash), yt-dlp sidecars
  scan-plex                   attach Plex items (ratingKey, library) to files
  scan-youtube                channel tabs, playlists, dates from YouTube (yt-dlp)
  youtube-list URL            list a channel / playlist without downloading (--match TEXT)
  link                        match files ↔ YouTube videos
  detect                      group videos into series → seasons → episodes
  set-style LIB SERIES STYLE  pin a series' style (static/talk/produced/cinematic)
  scan-visual                 production style per season: static / talk / produced / cinematic
  tag                         topic / format / audience / occasion tags per series and loose video
  group                       clips and stand-alone videos into collections; promos to their show
  report                      HTML page per folder to review the grouping visually
  export-plex                 one Plex collection per season (dry run unless --apply)
  plan-channels               suggest live TV channels + time-slot grids + filler pools
  dizquetv-shows              dizqueTV custom shows per series and per season (dry run unless --apply)
  export-dizquetv  PLAN.json  what a channel plan would write to dizqueTV (dry run)
  status                      what the cache knows, per folder
  serve [--log RUN.log]       live status page: current step, jobs per stage and library
  explain      TEXT           facts behind matching videos and where they ended up
"""

from __future__ import annotations

import argparse
import sqlite3
import sys
from pathlib import Path

from . import (
    classify_tags,
    db,
    detect,
    dizquetv_shows,
    explain,
    export_dizquetv,
    export_plex,
    link,
    pipeline,
    plan_channels,
    report,
    scan_local,
    scan_plex,
)


def main(argv: list[str] | None = None) -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser(
        prog="mediaintel", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--db", default="mediaintel.db", help="SQLite cache file (default: ./mediaintel.db)"
    )
    sub = ap.add_subparsers(dest="cmd", required=True)

    def plex_args(p: argparse.ArgumentParser) -> None:
        p.add_argument(
            "--dizquetv-db", type=Path, help=".dizquetv folder (reads plex-servers.json)"
        )
        p.add_argument("--plex-url")
        p.add_argument("--plex-token")
        p.add_argument("--path-map", action="append", default=[],
                       metavar="PLEX_PREFIX=LOCAL_PREFIX")  # fmt: skip

    def root_args(p: argparse.ArgumentParser) -> None:
        p.add_argument("roots", nargs="*", type=Path, help="folders to scan")
        p.add_argument("--from-plex", action="store_true",
                       help="scan the folders of the Plex libraries (instead of / besides FOLDER)")  # fmt: skip
        p.add_argument(
            "--library", help="with --from-plex: only libraries whose name contains this"
        )
        p.add_argument("--exclude-library", action="append", default=[],
                       help="with --from-plex: skip this library (repeatable)")  # fmt: skip
        p.add_argument("--workers", type=int, default=8)
        plex_args(p)

    p = sub.add_parser("scan-local", help="scan folders of video files")
    root_args(p)

    p = sub.add_parser("run", help="every step for the given folders / Plex libraries")
    root_args(p)
    p.add_argument("--no-youtube", action="store_true", help="local + Plex evidence only")
    p.add_argument("--from-step", default="scan", metavar="STEP",
                   help="start at this step, keeping what earlier steps cached: scan, plex, "
                        "youtube, link, detect, signatures, exact, tag, group, export-plex, "
                        "dizquetv-shows, report, scan-visual, plan-channels, export-dizquetv")  # fmt: skip
    p.add_argument("--skip", action="append", default=[], metavar="STEP",
                   help="leave this step out (repeatable), e.g. scan-visual while the NAS is slow")  # fmt: skip
    p.add_argument("--exact", action="store_true",
                   help="also fetch exact upload dates for series members (slow, polite)")  # fmt: skip
    p.add_argument("--lang", default="ar")
    p.add_argument("--delay", type=float, default=3.0)
    p.add_argument("--out", type=Path, default=Path("reports"), help="report directory")
    p.add_argument("--plex", action="store_true",
                   help="write season collections to Plex (replaces same-named ones, deletes none)")  # fmt: skip
    p.add_argument("--tv", action="store_true", help="update/create dizqueTV custom shows")
    p.add_argument("--tv-url", default="http://localhost:8002")
    p.add_argument("--backup-dir", type=Path, default=Path("backups"))

    p = sub.add_parser("explain", help="why a video is where it is")
    p.add_argument("text", nargs="?", default="", help="part of the title or file name")
    p.add_argument("--folder", help="only folders whose path contains this")
    p.add_argument("--limit", type=int, default=50)
    p.add_argument("--since", help="aired on/after this ISO date")
    p.add_argument("--until", help="aired on/before this ISO date")
    p.add_argument("--min-minutes", type=float)
    p.add_argument("--max-minutes", type=float)
    p.add_argument("--verdict", help="only these verdicts, e.g. unassigned, short, S2, extra")

    p = sub.add_parser("scan-plex", help="attach Plex items to scanned files")
    plex_args(p)
    p.add_argument("--library", help="only libraries whose name contains this")

    p = sub.add_parser("scan-youtube", help="fetch channel metadata from YouTube")
    p.add_argument(
        "--lang", default="ar", help="title language to request (YouTube auto-translates)"
    )
    p.add_argument(
        "--exact", action="store_true", help="also fetch exact upload dates for local videos"
    )
    p.add_argument("--refresh", action="store_true", help="re-scan channels already cached")
    p.add_argument("--folder", help="only folders whose path contains this")
    p.add_argument(
        "--workers", type=int, default=1, help="parallel requests for --exact (keep low)"
    )
    p.add_argument(
        "--delay", type=float, default=3.0, help="seconds between --exact requests (randomised)"
    )
    p.add_argument(
        "--cookies-from-browser", help="use a signed-in browser session for --exact (e.g. firefox)"
    )
    p.add_argument(
        "--exact-scope",
        choices=("series", "all"),
        default="series",
        help="series: only videos in detected series/playlists (default); all: every local video",
    )

    p = sub.add_parser(
        "youtube-list", help="list a channel/playlist (no download), e.g. to find a season"
    )
    p.add_argument("url", help="channel (https://www.youtube.com/@name) or playlist URL")
    p.add_argument("--match", help="only titles containing this")
    p.add_argument("--lang", default="ar")

    sub.add_parser("link", help="match files to YouTube videos")
    p = sub.add_parser("serve", help="live status page (http://localhost:8770)")
    p.add_argument("--log", type=Path, help="the run's log file, for the current step")
    p.add_argument("--port", type=int, default=8770)
    p.add_argument("--dizquetv-db", type=Path, help=".dizquetv folder, passed to runs started here")
    p.add_argument("--reports", type=Path, default=Path("reports"), help="review pages to serve")

    p = sub.add_parser("status", help="summary per folder")
    p.add_argument("--jobs", action="store_true", help="indexing jobs per folder and stage")
    p.add_argument("--folder", help="only folders whose path contains this")

    p = sub.add_parser("detect", help="group videos into series and seasons")
    p.add_argument("--folder", help="only folders whose path contains this")
    p.add_argument(
        "--min-episode-minutes", type=float, default=None,
        help="shorter videos are shorts: never episodes, left out of series (default: the "
             "detect setting min_episode_s, 180 s)",
    )  # fmt: skip

    p = sub.add_parser("set-style", help="pin a series' production style (overrides scan-visual)")
    p.add_argument("library", help="Plex library name, as in the report")
    p.add_argument("series", help="series name, as in the report")
    p.add_argument("style", nargs="?", choices=("static", "talk", "produced", "cinematic"))
    p.add_argument("--clear", action="store_true", help="remove the pinned style")

    p = sub.add_parser(
        "scan-visual", help="production style per season (static/talk/produced/cinematic)"
    )
    p.add_argument("--folder", help="only folders whose path contains this")
    p.add_argument("--per-season", type=int, default=3, help="episodes sampled per season")
    p.add_argument("--workers", type=int, default=4, help="parallel ffmpeg reads")

    p = sub.add_parser(
        "tag", help="topic/format/audience/occasion tags for series and loose videos"
    )
    p.add_argument("--folder", help="only folders whose path contains this")

    p = sub.add_parser(
        "group", help="clips / stand-alone videos into collections, promos linked to shows"
    )
    p.add_argument("--folder", help="only folders whose path contains this")

    p = sub.add_parser("dizquetv-shows", help="update/create dizqueTV custom shows from seasons")
    plex_args(p)
    p.add_argument("--url", default="http://localhost:8002", help="dizqueTV")
    p.add_argument("--library", help="only libraries whose name contains this")
    p.add_argument("--apply", action="store_true", help="write (default: dry run)")
    p.add_argument("--create-series", action="store_true",
                   help="also create a whole-series show where none exists")  # fmt: skip
    p.add_argument("--backup-dir", type=Path, default=Path("backups/dizquetv-shows"))

    p = sub.add_parser("plan-channels", help="suggest live TV channels + time-slot grids")
    p.add_argument("--out", type=Path, default=Path("reports"), help="output directory")
    p.add_argument("--review", type=Path, help="also write a plain-text review here")
    p.add_argument("--focus", action="append", default=[],
                   help="detail channels whose name contains this (repeatable)")  # fmt: skip

    p = sub.add_parser(
        "export-dizquetv", help="what a channel plan would write to dizqueTV (dry run)"
    )
    p.add_argument("plan", type=Path, help="channel-plan.json")
    p.add_argument("--url", default="http://localhost:8002")
    p.add_argument("--offline", action="store_true", help="don't read dizqueTV's current state")
    p.add_argument("--channel", type=int, action="append", help="only this channel number")
    p.add_argument("--ramadan", action="store_true", help="use the Ramadan grids")
    p.add_argument("--tz-offset", type=int, help="minutes, UTC - local (default: this machine)")
    p.add_argument("--replace-existing", action="store_true")
    p.add_argument("--dump", type=Path, help="write the request bodies to this folder")
    p.add_argument(
        "--apply", action="store_true", help="create/update the channels (default: dry run)"
    )
    plex_args(p)

    p = sub.add_parser("export-plex", help="write seasons to Plex as collections")
    p.add_argument("--dizquetv-db", type=Path, help=".dizquetv folder (reads plex-servers.json)")
    p.add_argument("--plex-url")
    p.add_argument("--plex-token")
    p.add_argument("--library", help="only libraries whose name contains this")
    p.add_argument("--apply", action="store_true", help="write (default: dry run)")
    p.add_argument("--include-clips", action="store_true", help="also export highlight-clip series")
    p.add_argument("--replace-existing", action="store_true",
                   help="replace a same-named collection this tool didn't create")  # fmt: skip
    p.add_argument("--remove-superseded", action="store_true",
                   help="delete older collections fully covered by a new season")  # fmt: skip
    p.add_argument("--diff", action="store_true",
                   help="list the episodes each change adds or takes out")  # fmt: skip

    p = sub.add_parser("report", help="HTML review page per folder")
    p.add_argument("--folder", help="only folders whose path contains this")
    p.add_argument("--out", type=Path, default=Path("reports"), help="output directory")

    a = ap.parse_args(argv)
    # paths are stored with '/' on every OS: accept either separator on the command line
    if getattr(a, "folder", None):
        a.folder = a.folder.replace("\\", "/")
    if getattr(a, "roots", None):
        a.roots = [Path(str(r).replace("\\", "/")) for r in a.roots]
    conn = db.connect(a.db)
    maps: list[tuple[str, str]] = [
        (s, d) for s, d in (m.split("=", 1) for m in getattr(a, "path_map", []))
    ] or scan_plex.load_path_map(a.db)
    if a.cmd in ("scan-local", "run"):
        roots = list(a.roots)
        server = None
        if a.from_plex or a.dizquetv_db or a.plex_url:
            server = scan_plex.load_server(a.dizquetv_db, a.plex_url, a.plex_token)
        if a.from_plex:
            assert server is not None
            libs = scan_plex.plex_roots(
                server, maps, library=a.library, exclude=tuple(a.exclude_library)
            )
            print(f"{len(libs)} Plex library folders: " + ", ".join(t for t, _ in libs))
            roots += [p for _, p in libs]
        if not roots:
            ap.error("give FOLDER(s) or --from-plex")
        if a.cmd == "scan-local":
            scan_local.scan_roots(conn, roots, workers=a.workers)
        else:
            pipeline.run(
                conn, roots, server=server, path_map=maps, youtube=not a.no_youtube,
                exact=a.exact, lang=a.lang, delay_s=a.delay, report_dir=a.out, workers=a.workers,
                plex=a.plex, tv=a.tv, tv_url=a.tv_url, backup_dir=a.backup_dir,
                from_step=a.from_step,
                skip=tuple(a.skip),
            )  # fmt: skip
    elif a.cmd == "explain":
        explain.explain(
            conn, a.text, a.folder, a.limit, since=a.since, until=a.until,
            min_minutes=a.min_minutes, max_minutes=a.max_minutes, verdict=a.verdict,
        )  # fmt: skip
    elif a.cmd == "scan-plex":
        server = scan_plex.load_server(a.dizquetv_db, a.plex_url, a.plex_token)
        scan_plex.scan_plex(conn, server, maps, a.library)
    elif a.cmd == "scan-youtube":
        from . import scan_youtube  # needs the optional yt-dlp extra

        try:
            scan_youtube.scan(
                conn, lang=a.lang, exact=a.exact, refresh=a.refresh, only_folder=a.folder,
                workers=a.workers, delay_s=a.delay, cookies_from_browser=a.cookies_from_browser, exact_scope=a.exact_scope,
            )  # fmt: skip
        except scan_youtube.BotCheck as e:
            print(f"stopped: {e}")
            return 2
    elif a.cmd == "youtube-list":
        from . import scan_youtube

        scan_youtube.list_source(a.url, match=a.match, lang=a.lang)
    elif a.cmd == "link":
        link.link(conn)
    elif a.cmd == "dizquetv-shows":
        dizquetv_shows.run(
            conn, scan_plex.load_server(a.dizquetv_db, a.plex_url, a.plex_token), url=a.url,
            apply=a.apply, library_filter=a.library, create_series=a.create_series,
            backup_dir=a.backup_dir,
        )  # fmt: skip
    elif a.cmd == "group":
        from . import group_clips

        group_clips.run(conn, a.folder)
    elif a.cmd == "plan-channels":
        plan_channels.run(conn, a.out, review=a.review, focus=a.focus)
    elif a.cmd == "export-dizquetv":
        export_dizquetv.run(
            a.plan, url=a.url, offline=a.offline, only=set(a.channel or []) or None,
            ramadan=a.ramadan, tz_offset=a.tz_offset, replace_existing=a.replace_existing,
            dump=a.dump, apply=a.apply,
            server=scan_plex.load_server(a.dizquetv_db, a.plex_url, a.plex_token) if a.apply else None,
            conn=conn,
        )  # fmt: skip
    elif a.cmd == "set-style":
        from . import scan_visual

        if not a.clear and not a.style:
            ap.error("set-style needs STYLE, or --clear to remove the override")
        scan_visual.set_override(conn, a.library, a.series, None if a.clear else a.style)
        print(f"{a.library} / {a.series}: " + ("cleared" if a.clear else a.style))
    elif a.cmd == "scan-visual":
        from . import scan_visual

        scan_visual.run(conn, a.folder, per_season=a.per_season, workers=a.workers)
    elif a.cmd == "tag":
        classify_tags.run(conn, a.folder)
    elif a.cmd == "detect":
        detect.run(conn, a.folder, a.min_episode_minutes)
    elif a.cmd == "export-plex":
        server = scan_plex.load_server(a.dizquetv_db, a.plex_url, a.plex_token)
        export_plex.export(
            conn, server, apply=a.apply, library_filter=a.library, include_clips=a.include_clips,
            replace_existing=a.replace_existing, remove_superseded=a.remove_superseded, diff=a.diff,
        )  # fmt: skip
    elif a.cmd == "report":
        report.run(conn, a.folder, a.out)
    elif a.cmd == "serve":
        from . import serve

        serve.serve(str(a.db), a.log, a.port, dizquetv_db=a.dizquetv_db, reports=a.reports)
        return 0
    elif a.cmd == "status":
        if a.jobs:
            from . import jobs

            jobs.summary(conn, a.folder)
        else:
            status(conn)
    return 0


def status(conn: sqlite3.Connection) -> None:
    q = """
    SELECT f.folder, COUNT(*) n, SUM(f.duration_s IS NOT NULL) dur, SUM(f.thumb_dhash IS NOT NULL) thumbs,
           SUM(f.youtube_id IS NOT NULL) linked, SUM(y.date_quality='exact') exact,
           SUM(y.date_quality='approximate') approx, SUM(f.plex_rating_key IS NOT NULL) plex,
           (SELECT status FROM yt_channels c WHERE c.channel_id=d.channel_id) yt
    FROM files f LEFT JOIN folders d ON d.folder=f.folder LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id
    GROUP BY f.folder ORDER BY f.folder"""
    print(
        f"{'folder':50} {'files':>6} {'dur':>5} {'thumb':>5} {'yt':>5} {'exact':>5} {'approx':>6} {'plex':>5}  channel"
    )
    for r in conn.execute(q):
        name = Path(r["folder"]).name[:50]
        print(
            f"{name:50} {r['n']:6} {r['dur']:5} {r['thumbs']:5} {r['linked']:5} {r['exact'] or 0:5} {r['approx'] or 0:6} {r['plex']:5}  {r['yt'] or '-'}"
        )


if __name__ == "__main__":
    raise SystemExit(main())
