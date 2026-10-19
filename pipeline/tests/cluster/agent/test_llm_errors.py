"""The door's error codes (``llm.done.error`` on the wire)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.llm import MachineGone, NoCapacity, NoModel
from maktaba_pipeline.llm.errors import RuntimeFailed, error_for

pytestmark = pytest.mark.unit


def test_error_codes() -> None:
    assert isinstance(error_for("no_model"), NoModel)
    assert isinstance(error_for("no_capacity: queue timeout"), NoCapacity)
    assert str(error_for("no_capacity: queue timeout")) == "queue timeout"
    assert isinstance(error_for("weird"), RuntimeFailed)
    assert isinstance(error_for("machine_gone"), MachineGone)
