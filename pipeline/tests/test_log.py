"""Tests for the maktaba_pipeline.log structlog wrapper (Story 21.1)."""

from __future__ import annotations

import io
import json
import logging
from collections.abc import Callable, Iterator
from contextlib import redirect_stdout
from typing import Any

import pytest
import structlog

from maktaba_pipeline.log import (
    DEFAULT_REDACTED_FIELDS,
    MAX_MSG_BYTES,
    REDACTED_VALUE,
    TRUNCATION_SUFFIX,
    bind_request_id,
    bind_session_id,
    get_logger,
    init,
    set_level,
    truncate_msg,
)


def _emit_one(callable_: Callable[[], None], /) -> dict[str, Any]:
    """Capture stdout from a single emission and return the parsed JSON."""
    buf = io.StringIO()
    with redirect_stdout(buf):
        callable_()
    line = buf.getvalue().splitlines()[-1]
    parsed: dict[str, Any] = json.loads(line)
    return parsed


@pytest.fixture(autouse=True)
def _reset_structlog() -> Iterator[None]:
    """Restore structlog defaults between tests."""
    yield
    structlog.reset_defaults()
    structlog.contextvars.clear_contextvars()


@pytest.mark.unit
def test_required_fields_present_in_prod() -> None:
    """AC2: every line carries ts, level, service, msg, version, env."""
    init(service="pipeline", env="prod", version="v1.2.3")
    log = get_logger()

    rec = _emit_one(lambda: log.info("hello", k="v"))

    for field in ("ts", "level", "service", "msg", "version", "env"):
        assert field in rec, f"missing required field {field!r}: {rec}"
    assert rec["service"] == "pipeline"
    assert rec["version"] == "v1.2.3"
    assert rec["env"] == "prod"
    assert rec["msg"] == "hello"
    assert rec["level"] == "info"


@pytest.mark.unit
def test_context_fields_injected() -> None:
    """Bound context vars appear on emitted records."""
    init(service="pipeline", env="prod", version="v0")
    log = get_logger()

    def emit() -> None:
        with bind_request_id("req-1"), bind_session_id("sess-2"):
            log.info("ping")

    rec = _emit_one(emit)
    assert rec["request_id"] == "req-1"
    assert rec["session_id"] == "sess-2"


@pytest.mark.unit
def test_redaction_masks_sensitive_fields() -> None:
    """Default redaction list masks sensitive keys (case-insensitive)."""
    init(service="pipeline", env="prod", version="v0")
    log = get_logger()

    rec = _emit_one(
        lambda: log.info(
            "auth",
            username="alice",
            password="hunter2",
            Authorization="Bearer xyz",
            api_key="k1",
            request_id="ok-to-log",
        )
    )

    assert rec["password"] == REDACTED_VALUE
    assert rec["Authorization"] == REDACTED_VALUE
    assert rec["api_key"] == REDACTED_VALUE
    assert rec["username"] == "alice"
    assert rec["request_id"] == "ok-to-log"


@pytest.mark.unit
def test_default_redact_list_covers_common_secrets() -> None:
    """Sanity check: the default list catches passwords, tokens, keys."""
    must_include = {"password", "token", "api_key", "authorization"}
    actual = {f.lower() for f in DEFAULT_REDACTED_FIELDS}
    missing = must_include - actual
    assert not missing, f"redact list missing {missing}"


@pytest.mark.unit
def test_set_level_filters_debug() -> None:
    """set_level toggles the debug threshold at runtime."""
    init(service="pipeline", env="prod", version="v0", level=logging.INFO)
    log = get_logger()

    buf = io.StringIO()
    with redirect_stdout(buf):
        log.debug("first-debug")
    assert "first-debug" not in buf.getvalue()

    set_level(logging.DEBUG)
    log = get_logger()  # re-fetch under the new wrapper class
    buf = io.StringIO()
    with redirect_stdout(buf):
        log.debug("second-debug")
    assert "second-debug" in buf.getvalue()


@pytest.mark.unit
def test_rtl_unicode_round_trip() -> None:
    """EC3: RTL Arabic content survives the JSON encode/decode cycle."""
    init(service="pipeline", env="prod", version="v0")
    log = get_logger()
    title = "كتاب الفهرست"

    rec = _emit_one(lambda: log.info("rtl test", title=title))
    assert rec["title"] == title


@pytest.mark.unit
def test_large_msg_truncated() -> None:
    """EC1: oversized event strings are clipped with a marker."""
    init(service="pipeline", env="prod", version="v0")
    log = get_logger()
    big = "x" * 70_000

    rec = _emit_one(lambda: log.info(big))
    msg = rec["msg"]
    assert isinstance(msg, str)
    assert len(msg) <= MAX_MSG_BYTES
    assert msg.endswith(TRUNCATION_SUFFIX)


@pytest.mark.unit
def test_truncate_msg_processor_pure() -> None:
    """truncate_msg is a pure processor; no logger configuration needed."""
    out = truncate_msg(None, "info", {"event": "x" * 70_000})
    assert isinstance(out["event"], str)
    assert len(out["event"]) <= MAX_MSG_BYTES
    assert out["event"].endswith(TRUNCATION_SUFFIX)
