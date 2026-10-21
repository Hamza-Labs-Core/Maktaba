"""The TV-side rules: defaults are what the stand-alone tool does without its flags; what writes
to the share or asks YouTube is off until switched on; each has help for the settings page."""

from __future__ import annotations

import pytest

from maktaba_pipeline import rules, settings
from maktaba_pipeline.schedule.tv_settings import (
    ChannelAuditConfig,
    DizqueExportConfig,
    ExactDatesConfig,
    MaktabaTvConfig,
    PlexLiveConfig,
    RepairConfig,
    channel_numbers,
)

SECTIONS = {
    "dizquetv": DizqueExportConfig,
    "exactdates": ExactDatesConfig,
    "audit": ChannelAuditConfig,
    "plexlive": PlexLiveConfig,
    "repair": RepairConfig,
    "tv": MaktabaTvConfig,
}


def test_the_defaults_are_the_tools_behaviour_without_flags() -> None:
    d = DizqueExportConfig()
    assert not (d.prune or d.clear_channels or d.replace_existing or d.ramadan)
    assert d.keep_airing and d.only_channels == ""  # the exporter always kept what airs
    e = ExactDatesConfig()
    assert (e.enabled, e.scope, e.delay_s, e.lang) == (False, "series", 3.0, "ar")
    r = RepairConfig()
    assert (r.enabled, r.max_height, r.h264_first, r.keep_broken_copy) == (False, 1080, True, True)
    assert ChannelAuditConfig().window_days == 28  # audit_channels.WINDOW_DAYS


def test_maktaba_tv_defaults_are_what_the_dizquetv_channels_do() -> None:
    t = MaktabaTvConfig()
    assert t.max_filler_per_gap == 0 and t.ramadan_grids and t.hijri_adjust_days == 0
    assert t.live == () and t.loudnorm and t.loudnorm_target_lufs == -16.0
    # dizqueTV levels with speechnorm=e=6.25 (no look-ahead: a channel tunes fast)
    assert (t.loudnorm_mode, t.speechnorm_expansion) == ("speechnorm", 6.25)
    assert t.watermark_skip_filler  # dizqueTV's disableFillerOverlay


def test_what_writes_to_the_share_or_asks_youtube_is_off_by_default() -> None:
    assert not RepairConfig().enabled and not ExactDatesConfig().enabled


@pytest.mark.parametrize("section", SECTIONS)
def test_every_setting_has_a_label_and_help_on_the_settings_page(section: str) -> None:
    cls = rules.SECTIONS[section][0]
    assert cls is SECTIONS[section]
    for f in settings.describe(cls()):
        assert f["label"] != f["name"].replace("_", " "), f"{section}.{f['name']} has no label"
        assert f["group"] != "Other"
    assert section in {s["key"] for s in rules.schema()["sections"]}


def test_a_bad_value_is_skipped_and_the_default_applies() -> None:
    cfg, bad = rules.config("exactdates", {"scope": "everything", "delay_s": -1, "enabled": True})
    assert cfg.enabled and cfg.scope == "series" and cfg.delay_s == 3.0
    assert set(bad) == {"scope", "delay_s"}
    cfg, bad = rules.config("repair", {"max_height": 10, "max_per_run": 3})
    assert cfg.max_height == 1080 and cfg.max_per_run == 3 and set(bad) == {"max_height"}


def test_channel_numbers_are_read_from_text() -> None:
    assert channel_numbers("101, 105,x, ,7") == {101, 105, 7}
    assert channel_numbers("") == set()
