"""Epic 53 story 53.01: the pipeline's logger on the shared rules and line schema.

T01 (shared/redact/cases.json), T02 (the schema's required fields on an emitted line),
T03/T04 (user_h, the same hash as Go's for the same key and id).
"""

from __future__ import annotations

import io
import json
import sys
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import Any

import pytest
import structlog

from maktaba_pipeline.log import bind_component, bind_user_id, get_logger, init, redact

REPO = Path(__file__).resolve().parents[2]
CASES = json.loads((REPO / "shared/redact/cases.json").read_text(encoding="utf-8"))


def _emit(fn: Callable[[], None]) -> dict[str, Any]:
    buf = io.StringIO()
    structlog.configure(logger_factory=structlog.PrintLoggerFactory(file=buf))
    fn()
    rec: dict[str, Any] = json.loads(buf.getvalue().strip().splitlines()[-1])
    return rec


@pytest.fixture(autouse=True)
def _clean() -> Iterator[None]:
    structlog.contextvars.clear_contextvars()
    yield
    redact.set_user_hash_key(None)
    structlog.contextvars.clear_contextvars()


@pytest.mark.unit
@pytest.mark.parametrize("case", CASES["scrub"], ids=lambda c: c["in"][:40])
def test_shared_scrub_cases(case: dict[str, str]) -> None:
    assert redact.scrub(case["in"]) == case["out"]


@pytest.mark.unit
@pytest.mark.parametrize("case", CASES["keys"], ids=lambda c: c["key"])
def test_shared_key_cases(case: dict[str, Any]) -> None:
    assert redact.RULES.key_sensitive(case["key"], case["string"]) is case["masked"]


@pytest.mark.unit
@pytest.mark.parametrize("case", CASES["user_hash"], ids=lambda c: c["key_hex"][:8])
def test_user_hash_matches_go(case: dict[str, str]) -> None:
    assert redact.user_hash(bytes.fromhex(case["key_hex"]), case["id"]) == case["user_h"]


@pytest.mark.unit
def test_the_copy_is_the_shared_file() -> None:
    assert redact.RULES_PATH.read_bytes() == (REPO / "shared/redact/rules.json").read_bytes()


@pytest.mark.unit
def test_a_line_names_the_user_by_hash_and_carries_component_and_node() -> None:
    init(service="pipeline", env="prod", version="v0")
    key = bytes(range(32))
    redact.set_user_hash_key(key)
    log = get_logger()

    def go() -> None:
        with bind_user_id("42"), bind_component("transcription"):
            log.info("stage_done")

    rec = _emit(go)
    assert "user_id" not in rec
    assert rec["user_h"] == redact.user_hash(key, "42") == CASES["user_hash"][0]["user_h"]
    assert rec["component"] == "transcription"
    assert rec["node"] == "home"
    sys.path.insert(0, str(REPO / "tools/log-schema"))
    import check  # type: ignore[import-not-found]  # noqa: PLC0415

    assert check.validate_line(json.dumps(rec)) == []


@pytest.mark.unit
def test_no_key_drops_the_user_id() -> None:
    init(service="pipeline", env="prod", version="v0")
    redact.set_user_hash_key(None)
    rec = _emit(lambda: get_logger().info("x", user_id="42"))
    assert "user_id" not in rec and "user_h" not in rec


@pytest.mark.unit
def test_the_key_is_read_from_the_supervisors_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    p = tmp_path / "log.key"
    p.write_bytes(bytes(range(32)))
    monkeypatch.setenv(redact.LOG_KEY_ENV, str(p))
    monkeypatch.setattr(redact, "_user_key_loaded", False)
    assert redact.hash_user(42) == CASES["user_hash"][0]["user_h"]


@pytest.mark.unit
def test_a_stage_jobs_lines_carry_its_component() -> None:
    """T07: a stage's job logs with the component ByStage names; an unknown stage none."""
    from types import SimpleNamespace

    from maktaba_pipeline.components.catalog import default
    from maktaba_pipeline.pipeline.runner import _job_log_context

    job = SimpleNamespace(
        id=7, stage="transcribe", video_id=None, subject_id=None, subject_kind=None
    )
    with _job_log_context(job):  # type: ignore[arg-type]
        got = structlog.contextvars.get_contextvars().get("component")
    comp = default().by_stage("transcribe")
    assert comp is not None and got == comp.id
    job.stage = "no-such-stage"
    with _job_log_context(job):  # type: ignore[arg-type]
        assert "component" not in structlog.contextvars.get_contextvars()
