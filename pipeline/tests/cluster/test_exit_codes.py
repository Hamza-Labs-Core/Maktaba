"""Exit codes services act on: 78 for ``run --node`` without a joined config, 77 when the
home refused this machine for good."""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path

import pytest
import structlog

from maktaba_pipeline.__main__ import main
from maktaba_pipeline.cluster import exitcodes

pytestmark = pytest.mark.integration  # main() runs an event loop


@pytest.fixture(autouse=True)
def _structlog_defaults() -> Iterator[None]:
    """main() configures logging onto the captured stdout: undo it for later tests."""
    yield
    structlog.reset_defaults()
    structlog.contextvars.clear_contextvars()


@pytest.mark.parametrize("how", ["flag", "env"])
def test_run_node_without_a_joined_config_exits_78(
    how: str,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    monkeypatch.setenv("MAKTABA_NODE_CONFIG", str(tmp_path / "node.json"))
    monkeypatch.setenv("MAKTABA_DATABASE_URL", "sqlite:///:memory:")
    argv = ["run"]
    if how == "flag":
        argv.append("--node")
    else:
        monkeypatch.setenv("MAKTABA_NODE", "1")
    assert main(argv) == exitcodes.EX_CONFIG == 78
    assert "has not joined a home: run maktaba-pipeline join" in capsys.readouterr().err


def test_run_node_with_a_partial_config_exits_78(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    """node.json without its identity crashed ``run`` with a KeyError on every restart."""
    path = tmp_path / "node.json"
    path.write_text(
        '{"home": {"url": "http://h:8080"}, "database_url": "postgres://n:x@h/maktaba"}',
        encoding="utf-8",
    )
    monkeypatch.setenv("MAKTABA_NODE_CONFIG", str(path))
    monkeypatch.setenv("MAKTABA_DATABASE_URL", "postgres://n:x@h/maktaba")
    monkeypatch.setenv("MAKTABA_NODE", "1")
    assert main(["run"]) == exitcodes.EX_CONFIG
    err = capsys.readouterr().err
    assert str(path) in err and "no node_id or private_key" in err


def test_refusal_is_remembered_once() -> None:
    assert exitcodes.exit_code(0) == 0 and exitcodes.refused() is None
    exitcodes.mark_refused("revoked")
    exitcodes.mark_refused("later reason")
    assert exitcodes.refused() == "revoked" and exitcodes.exit_code(0) == exitcodes.EX_NOPERM
    exitcodes.reset()
    assert exitcodes.exit_code(3) == 3


def test_the_help_names_the_codes(capsys: pytest.CaptureFixture[str]) -> None:
    with pytest.raises(SystemExit):
        main(["run", "--help"])
    text = capsys.readouterr().out
    assert "--node" in text and "77" in text and "78" in text
