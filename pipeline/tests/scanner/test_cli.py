"""End-to-end test for the ``maktaba-pipeline scan --dry-run`` CLI.

Exercises Story 1.4 AC #3: a CLI invocation against a fixture tree
prints one JSONL line per supported file to stdout and returns 0.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from maktaba_pipeline.scanner import cli as scan_cli


def _make_tree(root: Path, n: int) -> None:
    for i in range(n):
        (root / f"v{i:03d}.mp4").write_bytes(b"x" * (16 + i))
    # Walker rejects hidden files / sidecars; sprinkle a couple to
    # confirm the dry-run path filters them the same as a live scan.
    (root / ".hidden.mp4").write_bytes(b"hidden")
    (root / "notes.txt").write_text("not a media file")


def test_dry_run_emits_one_jsonl_line_per_file(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    _make_tree(tmp_path, n=4)

    rc = scan_cli.main(["--root", str(tmp_path), "--dry-run", "--library", "fixtures"])

    captured = capsys.readouterr()
    assert rc == 0
    assert captured.err == ""
    lines = [line for line in captured.out.splitlines() if line.strip()]
    assert len(lines) == 4
    decoded = [json.loads(line) for line in lines]
    for entry in decoded:
        assert entry["action"] == "would_insert"
        assert entry["filename"].startswith("v")
        assert entry["filename"].endswith(".mp4")
        assert entry["size_bytes"] >= 16


def test_cancel_flag_returns_deferral_exit_code(capsys: pytest.CaptureFixture[str]) -> None:
    rc = scan_cli.main(["--cancel", "--library", "fixtures"])
    captured = capsys.readouterr()
    assert rc == 64
    assert "Story 1.5" in captured.err


def test_dry_run_and_cancel_mutually_exclusive(capsys: pytest.CaptureFixture[str]) -> None:
    rc = scan_cli.main(["--dry-run", "--cancel", "--root", "/tmp"])
    captured = capsys.readouterr()
    assert rc == 2
    assert "mutually exclusive" in captured.err


def test_dry_run_requires_root(capsys: pytest.CaptureFixture[str]) -> None:
    rc = scan_cli.main(["--dry-run", "--library", "fixtures"])
    captured = capsys.readouterr()
    assert rc == 2
    assert "--root" in captured.err


def test_non_dry_run_returns_deferral(capsys: pytest.CaptureFixture[str]) -> None:
    rc = scan_cli.main(["--root", "/tmp", "--library", "fixtures"])
    captured = capsys.readouterr()
    assert rc == 64
    assert "API service" in captured.err
