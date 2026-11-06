"""tools/lint_log.py (Epic 53 story 53.01, T05): runtime data in a log event name fails."""

from __future__ import annotations

import importlib.util
from pathlib import Path
from typing import Any

import pytest

_spec = importlib.util.spec_from_file_location(
    "lint_log", Path(__file__).resolve().parents[1] / "tools" / "lint_log.py"
)
assert _spec and _spec.loader
lint_log: Any = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(lint_log)


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [
        'log.info(f"imported {n}")',
        '_log.warning("failed %s" % x)',
        'logger.error("failed {}".format(x))',
        'self._log.info("a " + b)',
        'get_logger().error(f"x{y}")',
    ],
)
def test_dynamic_event_names_fail(src: str) -> None:
    assert list(lint_log.check_source(src))


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [
        'log.info("video_imported", duration_s=12.4)',
        '_log.warning("stage_retry", error=f"{e}")',
        'print(f"not a log {x}")',
        'parser.error(f"bad {x}")',
    ],
)
def test_constant_event_names_pass(src: str) -> None:
    assert not list(lint_log.check_source(src))
