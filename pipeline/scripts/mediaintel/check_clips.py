"""Step ``check-clips``: a language model checks what each short video is about.

Runs after ``check-tags``. The keyword tagger reads a clip's title word by word, so a
fatwa that names a prophet («هل يجوز الدعاء بحق الأنبياء») was a prophets' story and
padded that channel. For every clip that airs as filler (not in a series, up to
``clip_max_minutes``, with a topic tag): the model's verdict on its title (cached per
input: library, title, model, prompt version) corrects ``video_tags``
(:func:`maktaba_pipeline.classify.tag_check.apply_clip`). Titles go to the model
``clip_batch`` at a time. A clip whose tags already carry its verdict is left alone, so
the step can run again (and resume after a stop) without asking twice.
"""

from __future__ import annotations

import json
import sqlite3
import time
import urllib.error
from pathlib import Path

from maktaba_pipeline.classify import tag_check as tc

SCHEMA = """
CREATE TABLE IF NOT EXISTS clip_checks (
    path TEXT PRIMARY KEY,
    input_hash TEXT NOT NULL,
    model TEXT NOT NULL,
    topics TEXT NOT NULL,
    applied_hash TEXT,
    checked_at TEXT NOT NULL
)
"""


_tags_hash = tc.clip_tags_hash


def _tags(conn: sqlite3.Connection, path: str) -> dict[str, float]:
    return {t: float(s) for t, s in conn.execute(
        "SELECT tag, score FROM video_tags WHERE path=?", (path,))}  # fmt: skip


def _apply(conn: sqlite3.Connection, path: str, keep: tuple[str, ...], applied: str | None,
           cfg: tc.TagCheckConfig) -> bool:  # fmt: skip
    tags = _tags(conn, path)
    if not tags or applied == _tags_hash(tags):
        return False
    new = tc.apply_clip(tags, keep, cfg)
    conn.execute("UPDATE clip_checks SET applied_hash=? WHERE path=?", (_tags_hash(new), path))
    if new == tags:
        return False  # the model kept every topic: nothing to correct
    conn.execute("DELETE FROM video_tags WHERE path=?", (path,))
    conn.executemany("INSERT INTO video_tags (path, tag, score) VALUES (?,?,?)",
                     [(path, t, s) for t, s in new.items()])  # fmt: skip
    return True


def run(conn: sqlite3.Connection, cfg: tc.TagCheckConfig | None = None) -> dict[str, int]:
    if cfg is None:
        from . import settings_store

        cfg = settings_store.get(conn, "tagcheck")
    counts = {"clips": 0, "asked": 0, "cached": 0, "applied": 0, "failed": 0}
    if not cfg.enabled or not cfg.clips:
        print("  -  off (settings: tagcheck.enabled / tagcheck.clips)")
        return counts
    conn.execute(SCHEMA)
    clips = conn.execute(
        """SELECT f.path, COALESCE(y.title, f.stem), COALESCE(f.plex_library, f.folder)
           FROM files f LEFT JOIN yt_videos y ON y.youtube_id = f.youtube_id
           WHERE f.duration_s <= ?
             AND f.path NOT IN (SELECT path FROM series_episodes)
             AND EXISTS (SELECT 1 FROM video_tags t WHERE t.path = f.path
                         AND t.tag LIKE 'topic:%' AND t.score >= 0.5)
           ORDER BY f.folder, f.path""",
        (cfg.clip_max_minutes * 60,),
    ).fetchall()
    counts["clips"] = len(clips)
    done = {r[0]: (r[1], r[2], r[3]) for r in conn.execute(
        "SELECT path, input_hash, topics, applied_hash FROM clip_checks")}  # fmt: skip
    # (path, title, library, its keyword topics, hash)
    todo: list[tuple[str, str, str, tuple[str, ...], str]] = []
    for path, title, lib in clips:
        lib = Path(lib).name if "/" in (lib or "") or "\\" in (lib or "") else (lib or "")
        tags = _tags(conn, path)
        cands = tc.clip_candidates(tags)
        h = tc.clip_input_hash(lib, title or "", cands, cfg)
        if path in done and done[path][2] == _tags_hash(tags):
            counts["cached"] += 1  # its tags carry the verdict: the topics it dropped are low now
        elif path in done and done[path][0] == h:
            counts["cached"] += 1
            if _apply(conn, path, tuple(json.loads(done[path][1])), done[path][2], cfg):
                counts["applied"] += 1
        else:
            todo.append((path, title or "", lib, cands, h))
    conn.commit()
    batches = [todo[i : i + cfg.clip_batch] for i in range(0, len(todo), cfg.clip_batch)]
    print(f"phase: {len(clips)} clips: {counts['cached']} answered before, "
          f"{len(todo)} to ask in {len(batches)} question(s)", flush=True)  # fmt: skip
    t0 = time.monotonic()
    in_a_row = 0  # unanswered questions in a row: the model server went away
    for b, batch in enumerate(batches, 1):
        try:
            answer = tc.ask(
                tc.clip_prompt([(lib, title, cands) for _, title, lib, cands, _ in batch], cfg), cfg
            )
        except (OSError, urllib.error.URLError, KeyError, ValueError) as e:
            counts["failed"] += len(batch)
            in_a_row += 1
            if in_a_row <= 3:
                print(f"  !  question {b}: the model did not answer ({e})", flush=True)
            if in_a_row >= 5:
                print("  !  the model is not answering: stopping (the answers so far are "
                      "kept; the rest next run)", flush=True)  # fmt: skip
                break
            continue
        in_a_row = 0
        got = tc.parse_clips(answer, [cands for _, _, _, cands, _ in batch])
        for i, (path, _title, _lib, _cands, h) in enumerate(batch, 1):
            if i not in got:
                counts["failed"] += 1  # left out of the answer: asked again next run
                continue
            counts["asked"] += 1
            conn.execute(
                "INSERT OR REPLACE INTO clip_checks VALUES (?,?,?,?,NULL,datetime('now'))",
                (path, h, cfg.model, json.dumps(got[i])),
            )
            if _apply(conn, path, got[i], None, cfg):
                counts["applied"] += 1
        conn.commit()
        if b % 10 == 0 or b == len(batches):
            per = (time.monotonic() - t0) / b
            print(f"  [{b}/{len(batches)}] questions: {counts['asked']} clips answered, "
                  f"{counts['applied']} corrected, ~{per * (len(batches) - b) / 60:.0f} min "
                  "left", flush=True)  # fmt: skip
    print(
        f"  ✓  {counts['clips']} clips: {counts['asked']} answered now, {counts['cached']} "
        f"before, {counts['applied']} corrected, {counts['failed']} without an answer"
    )
    return counts
