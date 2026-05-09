"""Smoke test so pytest collects at least one test on the stub.

Replaced once real pipeline tests land.
"""

import pytest

from maktaba_pipeline import stub_status


@pytest.mark.unit
def test_stub_status_returns_stub() -> None:
    assert stub_status() == "stub"
