"""log_event emits the required state-transition fields (Story 6.9)."""

from __future__ import annotations

import logging
from typing import Any

import pytest
import structlog
from structlog.testing import LogCapture

from maktaba_pipeline.observability import log_event


@pytest.fixture
def captured_logs() -> Any:
    cap = LogCapture()
    structlog.configure(
        processors=[cap],
        wrapper_class=structlog.make_filtering_bound_logger(logging.DEBUG),
        cache_logger_on_first_use=False,
    )
    yield cap
    structlog.reset_defaults()


def test_required_fields_are_always_present(captured_logs: LogCapture) -> None:
    log_event(
        "transition_to_running",
        job_id=42,
        video_id="0d8ef280-cafe-cafe-cafe-cafecafecafe",
        stage="transcribe",
        state="running",
        attempts=1,
    )
    assert len(captured_logs.entries) == 1
    entry = captured_logs.entries[0]
    for key in ("job_id", "video_id", "stage", "state", "attempts"):
        assert key in entry, f"missing required field: {key}"
    assert entry["event"] == "transition_to_running"
    assert entry["state"] == "running"
    assert entry["log_level"] == "info"


def test_extra_kwargs_are_passed_through(captured_logs: LogCapture) -> None:
    log_event(
        "job_failed_will_retry",
        job_id=42,
        video_id="vid",
        stage="extract",
        state="pending",
        attempts=2,
        retry_in_sec=120.0,
        error_kind="timeout",
        level="warning",
    )
    entry = captured_logs.entries[0]
    assert entry["log_level"] == "warning"
    assert entry["retry_in_sec"] == 120.0
    assert entry["error_kind"] == "timeout"


def test_full_lifecycle_events_appear(captured_logs: LogCapture) -> None:
    """A pending → claimed → running → paused → running → done lifecycle."""
    job = {"job_id": 7, "video_id": "v", "stage": "transcribe", "attempts": 1}
    log_event("transition_to_claimed", state="claimed", **job)
    log_event("transition_to_running", state="running", **job)
    log_event("paused_for_user", state="paused", **job)
    log_event("transition_to_running", state="running", **job)
    log_event("transition_to_done", state="done", **job)

    events = [e["event"] for e in captured_logs.entries]
    assert "transition_to_running" in events
    assert "paused_for_user" in events
    assert "transition_to_done" in events

    # Every captured event carries every required key.
    for entry in captured_logs.entries:
        assert {"job_id", "video_id", "stage", "state", "attempts"} <= entry.keys()


def test_debug_level_emits_at_debug(captured_logs: LogCapture) -> None:
    """Retry-storm debouncing: a retry that's still inside the backoff
    window logs at DEBUG so the operator's WARN feed stays readable
    (story edge case)."""
    log_event(
        "job_failed_will_retry",
        job_id=1,
        video_id="v",
        stage="extract",
        state="pending",
        attempts=3,
        level="debug",
        debounced_by_not_before=True,
    )
    entry = captured_logs.entries[0]
    assert entry["log_level"] == "debug"
