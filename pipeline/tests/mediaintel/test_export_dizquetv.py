"""export-dizquetv: what the plan sends to dizqueTV."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel.export_dizquetv import filler_items  # noqa: E402

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(("config", "expect"), [({"dz_filler_max_items": 10}, 10), ({}, 2000)])
def test_a_filler_list_holds_at_most_the_setting(config: dict[str, int], expect: int) -> None:
    items = [{"plex_rating_key": str(1000 + i)} for i in range(75_000)]
    got = filler_items({"config": config}, items)
    assert len(got) == expect
    assert got[0]["plex_rating_key"] == "1000"  # the first of the pool, in its order


def test_a_show_sends_only_what_its_slots_air_in_max_days() -> None:
    from scripts.mediaintel.export_dizquetv import airable, slot_lengths

    week, hour = 604_800_000, 3_600_000
    slots = [(0, "a"), (hour, "b"), (2 * hour, "a"), (3 * hour, "b")]
    sched = {"period": week, "maxDays": 14,
             "slots": [{"time": t, "showId": f"custom.{x}"} for t, x in slots]}  # fmt: skip
    lengths = slot_lengths(sched)
    assert lengths["custom.a"] == [hour, hour]
    assert lengths["custom.b"][-1] == week - 3 * hour  # the last slot runs to the period's end
    # 30-minute episodes, two 1-hour slots a week, two weeks: (2 + 1) per slot × 2 × 2 = 12
    assert airable(sched, lengths["custom.a"], [1_800_000] * 500) == 12
    assert airable(sched, [], []) == 0


def test_prune_deletes_only_this_tools_leftovers_and_clear_every_other_channel() -> None:
    from scripts.mediaintel.export_dizquetv import PREFIX, State, _removals

    plan = {"channels": [{"number": 101}]}
    state = State(
        channels={101: {"name": "Quran", "maktabaPlan": {}},
                  150: {"name": "old presenter", "maktabaPlan": {}},
                  7: {"name": "mine, by hand"}},
        shows={PREFIX + "kept": {"id": "s1"}, PREFIX + "old": {"id": "s2"},
               "a season show": {"id": "s3"}},
        fillers={PREFIX + "old list": {"id": "f1"}},
    )  # fmt: skip
    from scripts.mediaintel.export_dizquetv import Action

    planned = [Action("unchanged", "show", PREFIX + "kept")]
    pruned = {(a.kind, a.name) for a in _removals(plan, state, planned, clear_channels=False)}
    assert pruned == {("channel", "#150 old presenter"), ("show", PREFIX + "old"),
                      ("filler", PREFIX + "old list")}  # fmt: skip
    cleared = {a.name for a in _removals(plan, state, planned, clear_channels=True)
               if a.kind == "channel"}  # fmt: skip
    assert cleared == {"#150 old presenter", "#7 mine, by hand"}  # never #101: it is planned


def test_a_channel_gets_its_own_logo_else_the_default_and_the_corner_watermark(
    tmp_path: Path,
) -> None:
    from scripts.mediaintel.export_dizquetv import channel_art

    (tmp_path / "quran.png").write_bytes(b"png")
    (tmp_path / "default.png").write_bytes(b"png")
    (tmp_path / "wm.gif").write_bytes(b"gif")
    cfg = {"dz_logo_dir": str(tmp_path), "dz_watermark_file": str(tmp_path / "wm.gif"),
           "dz_watermark_position": "top-left", "dz_watermark_width": 10.0}  # fmt: skip
    art = channel_art({"config": cfg}, "quran")
    assert art["icon"].endswith("quran.png")
    assert channel_art({"config": cfg}, "fiqh")["icon"].endswith("default.png")
    wm = art["watermark"]
    assert wm["animated"] and wm["position"] == "top-left" and wm["width"] == 10.0
    assert channel_art({"config": {}}, "quran") == {}  # nothing set: no logos


def test_uploaded_logo_links_use_the_network_address() -> None:
    from scripts.mediaintel.export_dizquetv import art_link

    got = "http://localhost:8002/images/uploads/maktaba-quran.png"
    assert art_link({}, "http://localhost:8002", got) == got  # not set: as dizqueTV said
    cfg = {"dz_public_url": "http://192.168.50.24:8002/"}
    assert art_link(cfg, "http://localhost:8002", got) == (
        "http://192.168.50.24:8002/images/uploads/maktaba-quran.png"
    )
    assert art_link(cfg, "http://127.0.0.1:8002", got) == (
        "http://192.168.50.24:8002/images/uploads/maktaba-quran.png"
    )  # another host name


def _ch(start: str, durs: list[tuple[str, int]]) -> dict:
    return {"startTime": start, "programs": [
        {"ratingKey": k, "duration": d, "customShowId": "s"} if k else
        {"duration": d, "isOffline": True} for k, d in durs]}  # fmt: skip


T0 = 1_800_000_000_000  # ms
ISO0 = "2027-01-15T08:00:00Z"


def test_aired_finds_the_programme_on_air() -> None:
    from scripts.mediaintel.export_dizquetv import aired

    old = _ch(ISO0, [("a", 600_000), ("b", 600_000), ("c", 600_000)])
    so_far, s, e = aired(old, T0 + 700_000)
    assert [p["ratingKey"] for p in so_far] == ["a", "b"]
    assert (s, e) == (T0 + 600_000, T0 + 1_200_000)
    assert [p["ratingKey"] for p in aired(old, T0 + 1_900_000)[0]] == ["a"]  # it loops


def test_a_show_resumes_after_the_episode_it_aired_last() -> None:
    from scripts.mediaintel.export_dizquetv import resume

    content = [{"ratingKey": k} for k in ("e1", "e2", "e3", "e4")]
    so_far = [{"ratingKey": "e1", "customShowId": "s"}, {"ratingKey": "e2", "customShowId": "s"}]
    assert [p["ratingKey"] for p in resume(content, so_far, "s")] == ["e3", "e4", "e1", "e2"]
    assert resume(content, so_far, "other") == content  # never aired: from the start


def test_a_rewrite_lets_the_programme_on_air_finish() -> None:
    from scripts.mediaintel.export_dizquetv import splice

    old = _ch(ISO0, [("a", 600_000), ("b", 600_000)])
    new = _ch(ISO0, [("x", 300_000), ("y", 600_000), ("z", 600_000)])
    got = splice(old, new, T0 + 700_000, 0)
    assert got["startTime"] == "2027-01-15T08:10:00Z"  # b started at 08:10, it goes on
    seq = [(p.get("ratingKey"), p["duration"]) for p in got["programs"]]
    assert seq == [("b", 600_000)]  # b ends 08:20: x, y are over, z (08:15-08:25) is cut
    later = _ch(ISO0, [("x", 300_000), ("y", 600_000), ("z", 600_000), ("w", 600_000)])
    seq = [(p.get("ratingKey"), p["duration"])
           for p in splice(old, later, T0 + 700_000, 0)["programs"]]  # fmt: skip
    assert seq == [("b", 600_000), (None, 300_000), ("w", 600_000)]  # filler till w at 08:25
    off = _ch(ISO0, [(None, 900_000), ("b", 600_000)])
    assert splice(off, new, T0 + 100_000, 0) == new  # filler on air: the new one at once


def test_the_same_timeline_has_the_same_fingerprint() -> None:
    from scripts.mediaintel.export_dizquetv import fingerprint

    sched = {"period": 86_400_000, "slots": [{"time": 0, "showId": "custom.s"}]}
    assert fingerprint(sched, [("a", 5, "s")]) == fingerprint(dict(sched), [("a", 5, "s")])
    assert fingerprint(sched, [("a", 5, "s")]) != fingerprint(sched, [("a", 6, "s")])
