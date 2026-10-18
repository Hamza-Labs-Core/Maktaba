"""Step ``check-tags``: a language model checks each series' topic tags.

Runs after ``tag``. For every series: the model's verdict (cached per input: name,
library, sampled titles, model, prompt version) corrects ``series_tags``
(:func:`maktaba_pipeline.classify.tag_check.apply`). A series whose tags already carry
this verdict is left alone, so running the step twice changes nothing; a new ``tag`` run
rewrites the tags and the cached verdict is applied again without asking the model.
"""

from __future__ import annotations

import hashlib
import json
import sqlite3
import time
import urllib.error

from maktaba_pipeline.classify import tag_check as tc

SCHEMA = """
CREATE TABLE IF NOT EXISTS tag_checks (
    series_id TEXT PRIMARY KEY,
    input_hash TEXT NOT NULL,
    model TEXT NOT NULL,
    answer TEXT NOT NULL,
    applied_hash TEXT,
    checked_at TEXT NOT NULL
)
"""


def _tags_hash(tags: dict[str, tuple[float, str]]) -> str:
    src = json.dumps(sorted(tags.items()), ensure_ascii=False)
    return hashlib.sha1(src.encode()).hexdigest()


def run(conn: sqlite3.Connection, cfg: tc.TagCheckConfig | None = None) -> dict[str, int]:
    if cfg is None:
        from . import settings_store

        cfg = settings_store.get(conn, "tagcheck")
    counts = {"asked": 0, "cached": 0, "applied": 0, "unchanged": 0, "failed": 0}
    if not cfg.enabled:
        print("  -  off (settings: tagcheck.enabled)")
        return counts
    conn.execute(SCHEMA)
    series = conn.execute("SELECT id, name, library FROM series ORDER BY library, name").fetchall()
    done = {
        r[0]: (r[1], r[2], r[3])
        for r in conn.execute("SELECT series_id, input_hash, answer, applied_hash FROM tag_checks")
    }
    t0 = time.monotonic()
    for n, (sid, name, library) in enumerate(series, 1):
        titles = [
            r[0]
            for r in conn.execute(
                """SELECT COALESCE(y.title, f.stem) FROM series_episodes e
                   JOIN files f ON f.path=e.path
                   LEFT JOIN yt_videos y ON y.youtube_id=f.youtube_id
                   WHERE e.series_id=? ORDER BY e.season, e.episode""",
                (sid,),
            )
        ]
        h = tc.input_hash(name, library or "", titles, cfg)
        applied: str | None
        if sid in done and done[sid][0] == h:
            answer, applied = done[sid][1], done[sid][2]
            counts["cached"] += 1
        else:
            try:
                answer = tc.ask(tc.prompt(name, library or "", titles, cfg), cfg)
            except (OSError, urllib.error.URLError, KeyError, ValueError) as e:
                counts["failed"] += 1
                if counts["failed"] <= 3:
                    print(f"  !  {name[:50]}: the model did not answer ({e})", flush=True)
                if counts["failed"] >= 25 and counts["asked"] == 0:
                    print("  !  the model is not answering: stopping (tags stay as they are)")
                    break
                continue
            applied = None
            counts["asked"] += 1
            conn.execute(
                "INSERT OR REPLACE INTO tag_checks VALUES (?,?,?,?,NULL,datetime('now'))",
                (sid, h, cfg.model, answer),
            )
        verdict = tc.parse(answer, cfg)
        tags = {
            t: (float(s), e or "")
            for t, s, e in conn.execute(
                "SELECT tag, score, evidence FROM series_tags WHERE series_id=?", (sid,)
            )
        }
        if verdict is None or not tags or applied == _tags_hash(tags):
            counts["unchanged"] += 1
        else:
            new = tc.apply(tags, verdict, cfg)
            conn.execute("DELETE FROM series_tags WHERE series_id=?", (sid,))
            conn.executemany(
                "INSERT INTO series_tags (series_id, tag, score, evidence) VALUES (?,?,?,?)",
                [(sid, t, s, e) for t, (s, e) in new.items()],
            )
            conn.execute(
                "UPDATE tag_checks SET applied_hash=? WHERE series_id=?", (_tags_hash(new), sid)
            )
            counts["applied"] += 1
        if n % 100 == 0:
            conn.commit()
            per = (time.monotonic() - t0) / counts["asked"] if counts["asked"] else 0.0
            left = sum(1 for s in series[n:] if s[0] not in done) * per
            eta = f", ~{left / 60:.0f} min left" if left else ""
            print(f"  …  {n}/{len(series)} series: {counts}{eta}", flush=True)
    conn.commit()
    print(
        f"  ✓  {len(series)} series: {counts['asked']} asked, {counts['cached']} cached, "
        f"{counts['applied']} corrected, {counts['unchanged']} unchanged, "
        f"{counts['failed']} without an answer"
    )
    return counts
