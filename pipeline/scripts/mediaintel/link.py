"""link: match local files to YouTube videos of the folder's channel.

Order of evidence (first hit wins, ``files.link_method`` records which):

1. ``info_json`` — set by scan-local from the video's own ``.info.json``.
2. ``exact_title`` — the file stem equals yt-dlp's sanitised filename for
   the YouTube title (:func:`ytdlp_filename`).
3. ``folded_title`` — equal after Arabic folding, unique in the channel.
4. ``title_duration`` — folded titles equal but ambiguous; the duration
   (±3 s) picks the one.
5. ``fuzzy_title_duration`` — the title changed after download (a renamed
   ``@handle``, a typo fix): duration within 1.5 s and ≥ 50 % shared words,
   with exactly one candidate in the channel.

A YouTube title edited after download won't match; those files stay
unlinked and still get grouped from local evidence.
"""

from __future__ import annotations

import sqlite3
from collections import defaultdict

from maktaba_pipeline.text.arabic import fold, ytdlp_filename


def _jaccard(a: set[str], b: set[str]) -> float:
    return len(a & b) / len(a | b) if a and b else 0.0


def link(conn: sqlite3.Connection) -> None:
    folders = conn.execute(
        "SELECT folder, channel_id FROM folders WHERE channel_id IS NOT NULL"
    ).fetchall()
    total = dict.fromkeys(
        ("exact_title", "folded_title", "title_duration", "fuzzy_title_duration", "unlinked"), 0
    )
    for f in folders:
        vids = conn.execute(
            "SELECT youtube_id, title, duration_s FROM yt_videos WHERE channel_id=?",
            (f["channel_id"],),
        ).fetchall()
        if not vids:
            continue
        taken = {
            r[0] for r in conn.execute("SELECT youtube_id FROM files WHERE youtube_id IS NOT NULL")
        }
        by_exact: dict[str, list[sqlite3.Row]] = defaultdict(list)
        by_fold: dict[str, list[sqlite3.Row]] = defaultdict(list)
        for v in vids:
            by_exact[ytdlp_filename(v["title"])].append(v)
            by_fold[fold(v["title"])].append(v)
        files = conn.execute(
            "SELECT path, stem, duration_s FROM files WHERE folder=? AND youtube_id IS NULL",
            (f["folder"],),
        ).fetchall()
        for row in files:
            method = None
            cands = [v for v in by_exact.get(row["stem"], []) if v["youtube_id"] not in taken]
            if len(cands) == 1:
                method = "exact_title"
            else:
                cands = [
                    v for v in by_fold.get(fold(row["stem"]), []) if v["youtube_id"] not in taken
                ]
                if len(cands) == 1:
                    method = "folded_title"
                elif len(cands) > 1 and row["duration_s"]:
                    cands = [
                        v
                        for v in cands
                        if v["duration_s"] and abs(v["duration_s"] - row["duration_s"]) <= 3
                    ]
                    method = "title_duration" if len(cands) == 1 else None
                elif row["duration_s"]:
                    # title edited after download (e.g. a renamed @handle): same length to the
                    # second and mostly the same words
                    mine = set(fold(row["stem"]).split())
                    cands = [
                        v
                        for v in vids
                        if v["youtube_id"] not in taken
                        and v["duration_s"]
                        and abs(v["duration_s"] - row["duration_s"]) <= 1.5
                        and _jaccard(mine, set(fold(v["title"]).split())) >= 0.5
                    ]
                    method = "fuzzy_title_duration" if len(cands) == 1 else None
            if method:
                vid = cands[0]["youtube_id"]
                taken.add(vid)
                conn.execute(
                    "UPDATE files SET youtube_id=?, link_method=? WHERE path=?",
                    (vid, method, row["path"]),
                )
                total[method] += 1
            else:
                total["unlinked"] += 1
        conn.commit()
    print(f"linked: {total}")
