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
