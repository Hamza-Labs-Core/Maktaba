"""``last_success`` is one fixed text (a first-party plugin runs it as a named statement,
story 54.31): its outcome list is the success outcomes the module names."""

from __future__ import annotations

import inspect

from maktaba_pipeline.engine import propagate


def test_the_fixed_text_lists_the_success_outcomes() -> None:
    src = inspect.getsource(propagate.last_success)
    listed = ",".join(f"'{o}'" for o in propagate._SUCCESS_OUTCOMES)
    assert f"outcome IN ({listed})" in src
