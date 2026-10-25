"""A share directory that fails to list is logged (rate-limited), not silently empty."""

from __future__ import annotations

import errno
import os

import pytest
from structlog.testing import capture_logs

from maktaba_pipeline.engine import paths


@pytest.fixture(autouse=True)
def _fresh_rate_limit() -> None:
    paths._list_fail_logged.clear()


@pytest.mark.unit
def test_listing_failure_is_logged_once_per_errno(monkeypatch: pytest.MonkeyPatch) -> None:
    def eio(_d: str) -> list[str]:
        raise OSError(errno.EIO, "Input/output error")

    monkeypatch.setattr(os, "listdir", eio)
    with capture_logs() as logs:
        assert paths._bounded_listdir("/share/a") == []
        assert paths._bounded_listdir("/share/a") == []  # inside the window: quiet
        assert paths._bounded_listdir("/share/b") == []  # another directory: its own line
    failed = [e for e in logs if e["event"] == "share_listing_failed"]
    assert [e["directory"] for e in failed] == ["/share/a", "/share/b"]
    assert failed[0]["log_level"] == "warning"
    assert failed[0]["errno"] == "EIO"
    assert "Input/output error" in failed[0]["error"]


@pytest.mark.unit
def test_missing_directory_is_not_a_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    def enoent(_d: str) -> list[str]:
        raise FileNotFoundError(errno.ENOENT, "No such file or directory")

    monkeypatch.setattr(os, "listdir", enoent)
    with capture_logs() as logs:
        assert paths._bounded_listdir("/share/gone") == []
    assert not [e for e in logs if e["event"] == "share_listing_failed"]
