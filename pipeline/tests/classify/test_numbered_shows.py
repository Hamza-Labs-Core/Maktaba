"""Season and episode numbers read from a numbered show's titles."""

from __future__ import annotations

import pytest

from maktaba_pipeline.classify.series_detector import _numbered

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("title", "season", "episode"),
    [
        ("قصة وفكرة الموسم 2 الحلقة 1", 2, 1),  # the marker never joins the show's name
        ("قصة وفكرة2 - ح11", 2, 11),
        ("قصة وفكرة - الحلقة 3", 1, 3),
    ],
)
def test_season_and_episode(title: str, season: int, episode: int) -> None:
    got = _numbered(title, (), frozenset())
    assert got is not None
    key, _, s, e = got
    assert (key, s, e) == ("قصهوفكره", season, episode)
