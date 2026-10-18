"""scan-youtube: pull channel metadata from YouTube with yt-dlp.

For each channel a library folder was downloaded from (``folders`` table,
filled by scan-local), or given explicitly:

1. **Listing (original-language titles).** Channel tabs (videos, shorts,
   streams) and every playlist with its item order. YouTube auto-translates
   titles for the request locale, so titles are requested in ``lang``
   (default ``ar``) to match the downloaded filenames.
2. **Approximate dates.** yt-dlp derives ``timestamp`` from the English
   "3 years ago" text only, so a second, English listing of the tabs
   supplies month-level dates keyed by video id.
3. **Exact dates** (``exact=True``). A per-video fetch for videos that
   exist locally and still lack an exact upload date. Cached; parallel.

Channels that no longer exist are recorded as ``gone`` and skipped next
time (use ``refresh`` to retry). Nothing is downloaded.
"""

from __future__ import annotations

import random
import sqlite3
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime
from typing import Any

import yt_dlp  # type: ignore[import-untyped]

from maktaba_pipeline.discovery.ytdlp_sidecars import is_real_playlist, parse_upload_date
from maktaba_pipeline.integrations.youtube import BotCheck, YtDlpFetcher

TABS = ("videos", "shorts", "streams")


def _ydl(lang: str, *, flat: bool = True, approximate: bool = False) -> yt_dlp.YoutubeDL:
    ex: dict[str, dict[str, list[str]]] = {
        "youtube": {"lang": [lang], "skip": ["dash", "hls", "translated_subs"]}
    }
    if approximate:
        ex["youtubetab"] = {"approximate_date": [""]}
    opts: dict[str, Any] = {
        "quiet": True, "no_warnings": True, "skip_download": True, "ignoreerrors": False,
        "extractor_args": ex, "socket_timeout": 30, "retries": 5,
    }  # fmt: skip
    if flat:
        opts["extract_flat"] = "in_playlist"
    return yt_dlp.YoutubeDL(opts)


def _extract(ydl: yt_dlp.YoutubeDL, url: str) -> dict[str, Any] | None:
    try:
        info = ydl.extract_info(url, download=False)
        return info if isinstance(info, dict) else None
    except yt_dlp.utils.DownloadError as e:
        msg = str(e)
        if "does not have a" in msg and "tab" in msg:  # channel has no shorts/streams tab
            return None
        raise


def _store_listing(
    conn: sqlite3.Connection,
    entries: list[dict[str, Any]],
    channel_id: str,
    channel: str | None,
    tab: str,
) -> int:
    n = 0
    for e in entries:
        vid, title = e.get("id"), e.get("title")
        if not isinstance(vid, str) or not isinstance(title, str) or len(vid) != 11:
            continue
        dur = e.get("duration")
        conn.execute(
            """INSERT INTO yt_videos (youtube_id, title, duration_s, channel_id, channel, tab, source)
               VALUES (?,?,?,?,?,?, 'youtube')
               ON CONFLICT (youtube_id) DO UPDATE SET
                 duration_s=COALESCE(yt_videos.duration_s, excluded.duration_s),
                 channel_id=COALESCE(yt_videos.channel_id, excluded.channel_id),
                 channel=COALESCE(yt_videos.channel, excluded.channel),
                 tab=COALESCE(yt_videos.tab, excluded.tab),
                 title=CASE WHEN yt_videos.source='info_json' THEN yt_videos.title ELSE excluded.title END""",
            (
                vid,
                title,
                float(dur) if isinstance(dur, int | float) else None,
                channel_id,
                channel,
                tab,
            ),
        )
        n += 1
    return n


def scan_channel(
    conn: sqlite3.Connection, channel_id: str, url: str, *, lang: str, playlists: bool = True
) -> str:
    base = url.rstrip("/")
    ydl = _ydl(lang)
    try:
        about = _extract(ydl, base + "/videos")
    except yt_dlp.utils.DownloadError as e:
        status = (
            "gone"
            if any(s in str(e) for s in ("does not exist", "unavailable", "terminated", "404"))
            else "error"
        )
        conn.execute(
            "INSERT OR REPLACE INTO yt_channels (channel_id, url, status, error) VALUES (?,?,?,?)",
            (channel_id, base, status, str(e)[:500]),
        )
        conn.commit()
        return status
    channel = (about or {}).get("channel") or (about or {}).get("uploader")
    counts: dict[str, int] = {}
    for tab in TABS:
        info = about if tab == "videos" else _extract(ydl, f"{base}/{tab}")
        if info:
            counts[tab] = _store_listing(
                conn, list(info.get("entries") or []), channel_id, channel, tab
            )
            conn.commit()  # never hold the write lock across a network fetch

    # approximate dates (English listing)
    ydl_en = _ydl("en", approximate=True)
    for tab in TABS:
        info = _extract(ydl_en, f"{base}/{tab}")
        for ent in (info or {}).get("entries") or []:
            ts = ent.get("timestamp")
            if isinstance(ent.get("id"), str) and isinstance(ts, int | float):
                d = datetime.fromtimestamp(ts, UTC).date().isoformat()
                conn.execute(
                    "UPDATE yt_videos SET upload_date=?, date_quality='approximate' WHERE youtube_id=? AND date_quality IS NULL",
                    (d, ent["id"]),
                )
        conn.commit()

    if playlists:
        pl = _extract(ydl, base + "/playlists")
        pl_entries = (pl or {}).get("entries") or []
        for k, p in enumerate(pl_entries, 1):
            pid = p.get("id")
            print(f"    {channel}: playlist {k}/{len(pl_entries)}", flush=True)
            if not isinstance(pid, str):
                continue
            items = _extract(ydl, f"https://www.youtube.com/playlist?list={pid}")
            if not items:
                continue
            entries = list(items.get("entries") or [])
            conn.execute(
                """INSERT INTO yt_playlists (playlist_id, title, channel_id, channel, item_count, is_real, source)
                   VALUES (?,?,?,?,?,?, 'youtube')
                   ON CONFLICT (playlist_id) DO UPDATE SET title=excluded.title, item_count=excluded.item_count,
                     source='youtube'""",
                (
                    pid,
                    items.get("title") or p.get("title") or pid,
                    channel_id,
                    channel,
                    len(entries),
                    int(is_real_playlist(pid)),
                ),
            )
            conn.execute("DELETE FROM yt_playlist_items WHERE playlist_id=?", (pid,))
            for pos, ent in enumerate(entries, 1):
                if isinstance(ent.get("id"), str):
                    conn.execute(
                        "INSERT OR IGNORE INTO yt_playlist_items (playlist_id, youtube_id, position) VALUES (?,?,?)",
                        (pid, ent["id"], pos),
                    )
            _store_listing(conn, entries, channel_id, channel, "playlist")
            conn.commit()
        counts["playlists"] = len((pl or {}).get("entries") or [])

    conn.execute(
        "INSERT OR REPLACE INTO yt_channels (channel_id, url, title, status) VALUES (?,?,?, 'ok')",
        (channel_id, base, channel),
    )
    conn.commit()
    print(f"  {channel}: {counts}")
    return "ok"


def exact_dates(
    conn: sqlite3.Connection,
    video_ids: list[str],
    *,
    lang: str,
    workers: int = 1,
    delay_s: float = 3.0,
    cookies_from_browser: str | None = None,
) -> int:
    """Fetch exact upload dates (and descriptions), politely.

    Sequential by default with a randomised ``delay_s`` between requests;
    the first bot-check response stops the run (re-run later to resume —
    finished ids are cached). ``cookies_from_browser`` ("firefox", "chrome", …)
    lets yt-dlp use a signed-in session, which lifts most limits.
    """
    stop = threading.Event()

    fetcher = YtDlpFetcher()

    def one(vid: str) -> tuple[str, dict[str, Any] | None]:
        if stop.is_set():
            return vid, None
        time.sleep(delay_s * (0.5 + random.random()))
        try:
            return vid, fetcher.video_info(
                vid, lang=lang, cookies_from_browser=cookies_from_browser
            )
        except BotCheck:
            stop.set()
            return vid, None

    done = 0
    with ThreadPoolExecutor(max(1, workers)) as pool:
        for vid, info in pool.map(one, video_ids):
            if not info:
                continue
            d = parse_upload_date(info.get("upload_date"))
            conn.execute(
                """UPDATE yt_videos SET upload_date=COALESCE(?, upload_date),
                     date_quality=CASE WHEN ? IS NOT NULL THEN 'exact' ELSE date_quality END,
                     description=COALESCE(description, ?) WHERE youtube_id=?""",
                (
                    d.isoformat() if d else None,
                    d.isoformat() if d else None,
                    info.get("description"),
                    vid,
                ),
            )
            conn.commit()  # per video: keeps the write lock short for concurrent tools
            done += 1
            if done % 25 == 0:
                print(f"  exact dates: {done}/{len(video_ids)}")
    conn.commit()
    if stop.is_set():
        raise BotCheck(
            f"YouTube bot check after {done} videos; re-run later (cached progress is kept), "
            "or pass --cookies-from-browser <browser>"
        )
    return done


def scan(
    conn: sqlite3.Connection,
    *,
    lang: str,
    exact: bool,
    refresh: bool,
    only_folder: str | None,
    workers: int,
    delay_s: float = 3.0,
    cookies_from_browser: str | None = None,
    exact_scope: str = "series",
) -> None:
    rows = conn.execute(
        "SELECT DISTINCT channel_id, channel_url, channel FROM folders WHERE channel_id IS NOT NULL"
        + (" AND folder LIKE ?" if only_folder else ""),
        ((f"%{only_folder}%",) if only_folder else ()),
    ).fetchall()
    for r in rows:
        cid = r["channel_id"]
        prev = conn.execute("SELECT status FROM yt_channels WHERE channel_id=?", (cid,)).fetchone()
        if prev and not refresh:
            print(f"{r['channel'] or cid}: cached ({prev['status']})")
        else:
            print(f"{r['channel'] or cid}: scanning")
            status = scan_channel(
                conn, cid, r["channel_url"] or f"https://www.youtube.com/channel/{cid}", lang=lang
            )
            if status != "ok":
                print(f"  {status}")
        if exact:
            ids = [
                x[0]
                for x in conn.execute(
                    """SELECT f.youtube_id FROM files f JOIN yt_videos y ON y.youtube_id=f.youtube_id
                       JOIN folders d ON d.folder=f.folder
                       WHERE d.channel_id=? AND (y.date_quality IS NULL OR y.date_quality!='exact')
                       AND (? = 'all' OR f.path IN (SELECT path FROM series_episodes)
                            OR f.youtube_id IN (SELECT youtube_id FROM yt_playlist_items))
                       ORDER BY (f.path IN (SELECT path FROM series_episodes)) DESC""",
                    (cid, exact_scope),
                )
            ]
            if ids:
                print(f"  fetching exact dates for {len(ids)} local videos")
                exact_dates(
                    conn,
                    ids,
                    lang=lang,
                    workers=workers,
                    delay_s=delay_s,
                    cookies_from_browser=cookies_from_browser,
                )


def list_source(url: str, *, match: str | None = None, lang: str = "ar") -> None:
    """Print a channel's tabs / playlists, or one playlist, without downloading anything.

    For finding where a season lives (a broadcaster's channel rather than the
    presenter's). ``match`` filters titles by folded substring.
    """
    from maktaba_pipeline.text.arabic import fold

    want = fold(match) if match else None
    ydl = _ydl(lang)
    base = url.rstrip("/")
    targets = [base] if "list=" in base or base.rsplit("/", 1)[-1] in TABS + ("playlists",) else [
        f"{base}/{t}" for t in (*TABS, "playlists")
    ]  # fmt: skip
    for t in targets:
        info = _extract(ydl, t)
        if not info:
            continue
        entries = [e for e in info.get("entries") or [] if e]
        hits = [e for e in entries if not want or want in fold(e.get("title") or "")]
        print(f"\n{t}: {len(entries)} entries, {len(hits)} matching")
        for i, e in enumerate(hits, 1):
            mins = f"{e['duration'] / 60:.0f} min" if e.get("duration") else ""
            kind = (
                "playlist"
                if e.get("ie_key") == "YoutubeTab" or "list=" in (e.get("url") or "")
                else ""
            )
            print(f"  {i:4} {mins:>7} {kind:8} {e.get('title')}   {e.get('url') or e.get('id')}")
