"""``maktaba-pipeline cluster … --archive``: the home's long-term archive (design-agent.md
section 7.10), read like the share — merged by ``ts``, the same filters — and link-safely."""

from __future__ import annotations

import argparse
import gzip
import io
import json
import os
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.__main__ import _build_parser
from maktaba_pipeline.cluster import cli

pytestmark = pytest.mark.unit

IP = "192.168.50.31"
DAY = "2026-10-01"


def _gz(path: Path, records: list[dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with gzip.open(path, "wt", encoding="utf-8") as fh:
        for r in records:
            fh.write(json.dumps(r) + "\n")


@pytest.fixture
def archive(tmp_path: Path) -> Path:
    root = tmp_path / "archive"
    day = root / IP / "logs" / DAY
    _gz(day / "node-ab12-1.jsonl.gz", [
        {"ts": f"{DAY}T10:00:00Z", "level": "info", "event": "a", "fields": {}},
        {"ts": f"{DAY}T10:00:02Z", "level": "error", "event": "c", "fields": {}},
    ])  # fmt: skip
    _gz(day / "node-ab12-2.jsonl.gz", [
        {"ts": f"{DAY}T10:00:01Z", "level": "warning", "event": "b", "fields": {}},
        {"ts": f"{DAY}T10:00:03Z", "level": "debug", "event": "d", "fields": {}},
    ])  # fmt: skip
    (day / ".manifest.json").write_text(json.dumps({"node-ab12-1.jsonl": 120}))
    _gz(root / IP / "logs" / "2026-09-30" / "node-ab12-1.jsonl.gz", [])
    _gz(root / "192.168.50.20" / "events" / DAY / "hub.jsonl.gz", [
        {"ts": f"{DAY}T09:00:00Z", "event": "connect", "worker_id": "w"},
    ])  # fmt: skip
    return root


def _show(root: Path, what: str, **kw: Any) -> list[str]:
    out = io.StringIO()
    assert cli.show(root, what, archive=True, as_json=True, out=out, **kw) == 0
    return [json.loads(x)["event"] for x in out.getvalue().splitlines()]


def test_reads_and_merges_the_gzip_day_files(archive: Path) -> None:
    assert _show(archive, "logs", ip=IP, date=DAY) == ["a", "b", "c", "d"]
    assert _show(archive, "logs", ip=IP, date=DAY, level="warning") == ["b", "c"]
    assert _show(archive, "events", ip="192.168.50.20", date=DAY) == ["connect"]
    out = io.StringIO()
    cli.show(archive, "logs", ip=IP, date=DAY, archive=True, out=out)
    assert "[node-ab12-2]" in out.getvalue()  # the worker id, without .jsonl.gz


def test_lists_archived_machines_and_days(archive: Path) -> None:
    out = io.StringIO()
    assert cli.show(archive, "logs", ip=None, date=None, archive=True, out=out) == 0
    text = out.getvalue()
    assert IP in text and DAY in text and "2026-09-30" in text and "process" not in text
    out = io.StringIO()
    assert cli.ls(archive, out, archive=True) == 0
    text = out.getvalue()
    assert "2 archived machine folder(s)" in text
    assert f"logs: 2026-09-30, {DAY}" in text and f"events: {DAY}" in text


def test_no_follow_and_only_the_archived_kinds(archive: Path) -> None:
    with pytest.raises(ValueError, match="--follow"):
        cli.show(archive, "logs", ip=IP, date=DAY, archive=True, follow=True)
    with pytest.raises(ValueError, match="keeps logs, jobs, llm, events"):
        cli.show(archive, "metrics", ip=IP, date=DAY, archive=True)


@pytest.mark.skipif(os.name == "nt", reason="symlinks need privileges on Windows")
def test_links_in_the_archive_are_refused(archive: Path, tmp_path: Path) -> None:
    outside = tmp_path / "outside"
    _gz(outside / "secret.jsonl.gz", [{"ts": f"{DAY}T10:00:00Z", "event": "secret"}])
    day = archive / IP / "logs" / DAY
    (day / "planted.jsonl.gz").symlink_to(outside / "secret.jsonl.gz")
    assert "secret" not in _show(archive, "logs", ip=IP, date=DAY)
    (archive / IP / "jobs").mkdir()
    (archive / IP / "jobs" / DAY).symlink_to(outside, target_is_directory=True)
    err = io.StringIO()
    assert cli.show(archive, "jobs", ip=IP, date=DAY, archive=True, out=err) == 1
    (archive / "10.6.6.6").symlink_to(outside, target_is_directory=True)
    out = io.StringIO()
    cli.ls(archive, out, archive=True)
    assert "10.6.6.6" not in out.getvalue()


def test_a_damaged_file_does_not_stop_the_others(
    archive: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    day = archive / IP / "logs" / DAY
    good = gzip.compress(b'{"ts": "2026-10-01T10:00:05Z", "event": "e"}\n' * 50)
    (day / "node-ab12-3.jsonl.gz").write_bytes(good[: len(good) // 2])  # cut short
    events = _show(archive, "logs", ip=IP, date=DAY)
    assert events[:4] == ["a", "b", "c", "d"]
    assert "stopped reading" in capsys.readouterr().err


def test_the_cli(
    archive: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    parse = _build_parser().parse_args
    args = parse(
        ["cluster", "logs", "--archive", str(archive), "--ip", IP, "--date", DAY, "--json"]
    )
    assert args.archive == str(archive)
    assert cli.main(args) == 0
    assert [json.loads(x)["event"] for x in capsys.readouterr().out.splitlines()] == list("abcd")
    # no DIR: MAKTABA_ARCHIVE_DIR, else ~/.maktaba/archive
    monkeypatch.setenv("MAKTABA_ARCHIVE_DIR", str(archive))
    args = parse(["cluster", "ls", "--archive"])
    assert args.archive == "" and cli.main(args) == 0
    assert IP in capsys.readouterr().out
    assert cli.main(parse(["cluster", "logs", "--archive", "--ip", IP, "--follow"])) == 2
    monkeypatch.delenv("MAKTABA_ARCHIVE_DIR")
    assert cli.archive_dir(None) == Path.home() / ".maktaba" / "archive"


def test_where_names_the_archive(archive: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("maktaba_pipeline.cluster.share.local_dir", lambda: archive / "none")
    monkeypatch.setenv("MAKTABA_ARCHIVE_DIR", str(archive))
    out = io.StringIO()
    args = argparse.Namespace(dir=str(archive), database_url=None, path_map=None, archive=None)
    cli.where(args, out)
    assert f"archive (the home's): {archive} (present)" in out.getvalue()
    monkeypatch.setenv("MAKTABA_ARCHIVE_DIR", str(archive / "missing"))
    out = io.StringIO()
    cli.where(args, out)
    assert "(not here)" in out.getvalue()
