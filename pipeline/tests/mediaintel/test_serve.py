"""The control centre's pure helpers: failure causes and grouping."""

from __future__ import annotations

import json
import sqlite3
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel.serve import (  # noqa: E402
    attention_groups,
    kind_counts,
    normalize_cause,
    retry_failures,
)

pytestmark = pytest.mark.unit


def test_frame_counts_share_a_cause_but_tail_stays_apart() -> None:
    a = normalize_cause("read failed: 1 frames")
    assert a == normalize_cause("read failed: 2 frames") == "read failed: frames short"
    assert normalize_cause("read failed: tail") == "read failed: tail"
    assert normalize_cause("read failed: tail, 5 frames") == "read failed: tail, frames short"


def test_distinct_messages_stay_distinct() -> None:
    assert normalize_cause("no frames") != normalize_cause("no audio or frames decoded")


def test_file_names_and_numbers_are_stripped() -> None:
    assert normalize_cause("cannot open 'E:/x/Episode 12.mkv' after 3 tries") == (
        "cannot open after tries"
    )
    assert normalize_cause(None) == "unknown error"


def test_groups_merge_and_count_retries() -> None:
    rows: list[tuple[str, str | None, int, int | None]] = [
        ("signature", "read failed: 1 frames", 3, 2),
        ("signature", "read failed: 2 frames", 2, 0),
        ("probe", "ffprobe gave no duration", 1, 1),
    ]
    g = attention_groups(rows)
    assert g[0]["cause"] == "read failed: frames short"
    assert (g[0]["count"], g[0]["retrying"], g[0]["given_up"]) == (5, 2, 3)
    assert len(g) == 2


def test_retry_resets_only_matching_failed_rows(tmp_path: Path) -> None:
    path = tmp_path / "t.db"
    c = sqlite3.connect(path)
    c.execute(
        "CREATE TABLE index_jobs (path TEXT, stage TEXT, state TEXT, attempts INT, "
        "max_attempts INT, error TEXT)"
    )
    c.executemany(
        "INSERT INTO index_jobs VALUES (?,?,?,?,?,?)",
        [
            ("a", "signature", "failed", 3, 3, "read failed: 1 frames"),
            ("b", "signature", "failed", 2, 3, "read failed: 2 frames"),
            ("c", "signature", "failed", 2, 3, "read failed: tail"),
            ("d", "probe", "failed", 2, 3, "read failed: 1 frames"),
        ],
    )
    c.commit()
    assert retry_failures(str(path), "signature", "read failed: frames short") == 2
    left = c.execute("SELECT path FROM index_jobs WHERE attempts > 0 ORDER BY path").fetchall()
    assert [r[0] for r in left] == ["c", "d"]


def test_kind_counts_use_distinct_series_keys() -> None:
    c = sqlite3.connect(":memory:")
    c.execute("CREATE TABLE series (id INT, metadata TEXT)")
    meta = '{"folder": "F", "kind": "ongoing", "series_key": "%s"}'
    c.executemany(
        "INSERT INTO series VALUES (?,?)",
        [(1, meta % "x"), (2, meta % "x"), (3, meta % "y")],
    )
    assert kind_counts(c) == {"F": {"ongoing": 2}}


def test_bucket_keys_are_ten_minute_prefixes_oldest_first() -> None:
    from scripts.mediaintel.serve import bucket_keys, fill_buckets

    now = 1_700_000_000.0  # 2023-11-14T22:13:20Z
    keys = bucket_keys(now, 3)
    assert keys == ["2023-11-14T21:5", "2023-11-14T22:0", "2023-11-14T22:1"]
    filled = fill_buckets({("2023-11-14T22:1", "done"): 4, ("2023-11-14T22:1", "failed"): 1}, keys)
    assert filled[-1] == {"t": "2023-11-14T22:10", "done": 4, "failed": 1}
    assert filled[0]["done"] == 0


def test_group_series_folds_seasons_and_collects_plex_titles() -> None:
    from scripts.mediaintel.serve import group_series

    def meta(season: int, first: str, last: str) -> str:
        return json.dumps(
            {
                "series_key": "k",
                "kind": "ongoing",
                "season": season,
                "first_air": first,
                "last_air": last,
            }  # fmt: skip
        )

    rows = [
        ("a", "Show", meta(2, "2021-01-01", "2021-06-01")),
        ("b", "Show", meta(1, "2020-01-01", "2020-06-01")),
    ]
    out = group_series(rows, {"a": 5, "b": 3}, {"b": "Show S1"})
    assert len(out) == 1
    g = out[0]
    assert (g["episodes"], g["first_air"], g["last_air"]) == (8, "2020-01-01", "2021-06-01")
    assert [s["season"] for s in g["seasons"]] == [1, 2]
    assert g["seasons"][0]["collection"] == "Show S1"


def test_tv_names_finds_series_and_season_shows_by_folded_name() -> None:
    from scripts.mediaintel.serve import tv_names

    g = {"name": "قصة وآية", "seasons": [{"season": 1}, {"season": 2}]}
    shows = {  # dizqueTV keys by folded name; spelling variants (ة/ه, آ/ا) still match
        "الشيخ نبيل العوضي - قصه وايه": "الشيخ نبيل العوضي - قصه وايه",
        "الشيخ نبيل العوضي - قصه وايه - الموسم 2": "الشيخ نبيل العوضي - قصة وآية - الموسم 2",
    }
    from maktaba_pipeline.text.arabic import fold

    shows = {fold(k): v for k, v in shows.items()}
    assert tv_names("الشيخ نبيل العوضي", g, shows) == [
        "الشيخ نبيل العوضي - قصه وايه",
        "الشيخ نبيل العوضي - قصة وآية - الموسم 2",
    ]


def test_plex_export_removes_an_episode_only_on_evidence() -> None:
    from scripts.mediaintel.export_plex import removable

    placed, shorts = {"moved"}, {"short"}
    assert removable("moved", "أنوار الأنبياء ٠٩", placed, shorts)  # in another season now
    assert removable("short", "مقطع", placed, shorts)  # under 3 min
    for t in ("تشويقة سلسلة خير القرون", "تتر برنامج قصة الفتنة", "إعلان بدء ترجمة",
              "السيرة النبوية (( المرحلة المكية كاملة ))", "حلقة 28 ｜ آل سلول.f251"):  # fmt: skip
        assert removable("x", t, placed, shorts), t
    # detection failed to place a numbered episode: it stays in its collection
    assert not removable("x", "المنهج الإصلاحي ｜ الأنفال 01", placed, shorts)
    assert not removable("x", "فايز الكندري ｜ سورة فصلت من الآية ١٣ إلى الآية ١٤", placed, shorts)
    assert not removable("x", "شرح صحيح البخاري كاملا ٥١٢", placed, shorts)


def test_pick_copies_keeps_the_mp4_transcode_with_the_youtube_details() -> None:
    from scripts.mediaintel.detect import pick_copies

    base = {"title": "x", "dur": 979.0, "date_quality": None, "tab": None, "youtube_id": None}
    name = "كيف تكون محبة النبي"
    mkv = {**base, "path": f"f/{name}.mkv", "stem": name, "size": 55,
           "upload_date": "2020-05-01", "youtube_id": "y2vMJey7UJc", "title": name}  # fmt: skip
    mp4 = {**base, "path": "f/كيف تكون محبة النبي.mp4", "stem": "كيف تكون محبة النبي", "size": 70,
           "upload_date": None}  # fmt: skip
    other = {**base, "path": "f/b.mp4", "stem": "b", "size": 1, "upload_date": None}
    rows, copies = pick_copies([mkv, mp4, other])
    kept = {r["path"]: r for r in rows}
    assert set(kept) == {mp4["path"], other["path"]}
    assert copies == {mkv["path"]: mp4["path"]}
    assert kept[mp4["path"]]["upload_date"] == "2020-05-01"  # took the original's details
    assert kept[mp4["path"]]["youtube_id"] == "y2vMJey7UJc"
