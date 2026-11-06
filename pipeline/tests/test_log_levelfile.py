"""Epic 53 story 53.11: the level file's level lasts until its time, then the start level."""

import logging
from datetime import UTC, datetime, timedelta

from maktaba_pipeline.log.levelfile import level_from, watch


def test_level_file(tmp_path):
    p = tmp_path / "pipeline"
    now = datetime(2026, 11, 6, 10, 0, tzinfo=UTC)
    assert level_from(str(p), logging.INFO, now) == logging.INFO
    p.write_text("debug " + (now + timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ") + "\n")
    assert level_from(str(p), logging.INFO, now) == logging.DEBUG
    assert level_from(str(p), logging.INFO, now + timedelta(hours=1)) == logging.INFO
    for bad in [
        "debug",
        "loud 2026-11-06T11:00:00Z",
        "debug tomorrow",
        "debug 2026-11-06T11:00:00",
    ]:
        p.write_text(bad)
        assert level_from(str(p), logging.WARNING, now) == logging.WARNING, bad


def test_no_file_no_watcher(monkeypatch):
    monkeypatch.delenv("MAKTABA_LOG_LEVEL_FILE", raising=False)
    assert watch(logging.INFO, lambda _l: None) is None
