"""Which wheel the venv bootstrap installs from a payload (Epic 35 review round 2, B-H1).

The Windows installer's update deletes nothing, so an app installed over an older one
may hold the older wheel too: the one the payload.json beside the pipeline folder names
is the payload's. A folder without one (a plain server's release) has its only wheel.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from maktaba_pipeline import venv_bootstrap as vb

pytestmark = pytest.mark.unit

OLD = "maktaba_pipeline-1.4.0-py3-none-any.whl"
NEW = "maktaba_pipeline-1.5.0rc1-py3-none-any.whl"


def _pipeline(tmp_path: Path, *wheels: str) -> Path:
    pipe = tmp_path / "pipeline"
    pipe.mkdir()
    for w in wheels:
        (pipe / w).write_bytes(b"w")
    (pipe / "constraints.txt").write_text("x", encoding="utf-8")
    return pipe


def _names(tmp_path: Path, wheel: str) -> None:
    doc = {"version": "v1.5.0-rc.1", "pipeline": {"wheel": wheel}}
    (tmp_path / "payload.json").write_text(json.dumps(doc), encoding="utf-8")


def test_the_wheel_payload_json_names_among_two(tmp_path: Path) -> None:
    pipe = _pipeline(tmp_path, OLD, NEW)
    with pytest.raises(vb.BootstrapError):
        vb.find_payload(pipe)  # two wheels, nothing says which
    _names(tmp_path, NEW)
    wheel, cons = vb.find_payload(pipe)
    assert wheel.name == NEW
    assert cons.name == "constraints.txt"


def test_a_named_wheel_that_is_gone_is_an_error(tmp_path: Path) -> None:
    pipe = _pipeline(tmp_path, OLD)
    _names(tmp_path, NEW)
    with pytest.raises(vb.BootstrapError, match="does not hold"):
        vb.find_payload(pipe)


def test_without_payload_json_the_only_wheel(tmp_path: Path) -> None:
    pipe = _pipeline(tmp_path, OLD)
    assert vb.find_payload(pipe)[0].name == OLD
    (tmp_path / "payload.json").write_text("{not json", encoding="utf-8")
    assert vb.find_payload(pipe)[0].name == OLD
