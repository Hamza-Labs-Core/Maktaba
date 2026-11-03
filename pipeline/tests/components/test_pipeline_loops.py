"""T11 (Epic 51, story 51.06): the pipeline's loops and the catalog name the same loops, of
the same components (the pipeline half of F1; the catalog job runs it)."""

from __future__ import annotations

import re
from pathlib import Path

import pytest

from maktaba_pipeline.components.catalog import default
from maktaba_pipeline.components.runner import PIPELINE_LOOPS

pytestmark = pytest.mark.unit

RUNTIME = Path(__file__).resolve().parents[2] / "src" / "maktaba_pipeline" / "runtime.py"


def test_the_loop_table_is_the_catalogs() -> None:
    cat = {name: c.id for name, c in default().loops_of("pipeline").items()}
    assert cat == PIPELINE_LOOPS


def test_every_loop_the_runtime_declares_is_in_the_table() -> None:
    src = RUNTIME.read_text(encoding="utf-8")
    declared = set(re.findall(r'components\.declare\(\s*"([^"]+)"', src))
    assert declared, "runtime.py declares no loop"
    assert declared <= set(PIPELINE_LOOPS)
    # the switchable components' loops are all the runner's to start and stop
    cat = default()
    switchable = {n for n, cid in PIPELINE_LOOPS.items() if (c := cat.get(cid)) and c.switchable}
    assert switchable <= declared


def test_every_named_task_is_a_catalog_loop() -> None:
    src = RUNTIME.read_text(encoding="utf-8")
    # the core loops the runtime starts itself, by task name or writer name
    started = set(re.findall(r'name="([a-z-]+)"', src)) | set(
        re.findall(r'writer\(\s*"([a-z-]+)"', src)
    )
    core = {n for n, cid in PIPELINE_LOOPS.items() if cid == "core.pipeline"}
    assert core <= started
