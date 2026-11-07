"""The first-party bundle imports first in a fresh process (Epic 54, story 54.23 §2).

The plugin runner's ``main`` imports ``maktaba_pipeline.plugins.firstparty`` before
anything has loaded the stage graph. The graph builds its first-party stages from that
module as it loads (``engine.graph._fp``), so a top-level import of the graph there made
a cycle: every runner exited 1 about 0.2 s after its start (``cannot import name
'stage_def' from partially initialized module``), was marked failed after five exits, and
the worker ran each built-in plugin's stages in-process. The update rehearsal's scenario
16.1 found it on an installed release. A test process that imported the graph first (any
conftest) never saw it, so each case here runs in a new interpreter.
"""

from __future__ import annotations

import subprocess
import sys

import pytest

FIRST = [
    # the runner's own order: its main's imports, then the stages its group serves
    """
from maktaba_pipeline.plugins import firstparty
from maktaba_plugin_runner import sandbox, trust
from maktaba_plugin_runner.server import Runner, load_impl
from maktaba_plugin_runner.__main__ import members
assert firstparty.stage_names(), "the bundle has no stage"
for group in ("light", "fp", "ml", "index"):
    members(group)
""",
    # the bundle alone, then the graph after it
    """
from maktaba_pipeline.plugins import firstparty
from maktaba_pipeline.engine.graph import get_stage
for name in firstparty.stage_names():
    assert get_stage(name).name == name
""",
    # the graph first (the worker's order) still works
    """
from maktaba_pipeline.engine.graph import get_stage
from maktaba_pipeline.plugins import firstparty
assert firstparty.stage_def(sorted(firstparty.stage_names())[0])
""",
]


@pytest.mark.parametrize("code", FIRST, ids=["runner", "bundle-then-graph", "graph-then-bundle"])
def test_imports_in_a_fresh_process(code: str) -> None:
    r = subprocess.run(
        [sys.executable, "-c", code], capture_output=True, text=True, timeout=120, check=False
    )
    assert r.returncode == 0, r.stderr[-2000:]
