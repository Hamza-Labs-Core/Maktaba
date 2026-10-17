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
