"""``maktaba-pipeline cluster …`` reading the files on the share."""

from __future__ import annotations

import io
import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.__main__ import _build_parser
from maktaba_pipeline.cluster import cli

pytestmark = pytest.mark.unit

DAY = "2026-10-19"


def _write(p: Path, records: list[dict[str, Any]]) -> None:
    p.parent.mkdir(parents=True, exist_ok=True)
    with p.open("a") as fh:
        for r in records:
            fh.write(json.dumps(r) + "\n")


@pytest.fixture
def root(tmp_path: Path) -> Path:
    m = tmp_path / "192.168.50.31"
    (m / "processes").mkdir(parents=True)
    (m / "processes" / "w1.json").write_text("{}")
    _write(
        m / "logs" / DAY / "w1.jsonl",
        [
            {"ts": f"{DAY}T10:00:00Z", "level": "info", "event": "a", "fields": {"n": 1}},
            {"ts": f"{DAY}T10:00:02Z", "level": "error", "event": "c", "fields": {}},
        ],
    )
    _write(
        m / "logs" / DAY / "w2.jsonl",
        [
            {"ts": f"{DAY}T10:00:01Z", "level": "warning", "event": "b", "fields": {}},
            {"ts": f"{DAY}T10:00:03Z", "level": "debug", "event": "d", "fields": {}},
        ],
    )
    _write(
        m / "jobs" / DAY / "w1.jsonl",
        [{"ts": f"{DAY}T10:00:00Z", "t": "job", "event": "done", "job_id": 4, "stage": "probe",
          "duration_ms": 120}],
    )  # fmt: skip
    (tmp_path / "10.0.0.9" / "logs" / "2026-10-18").mkdir(parents=True)
    return tmp_path


def test_merges_a_days_files_by_timestamp(root: Path) -> None:
    out = io.StringIO()
    assert cli.show(root, "logs", ip="192.168.50.31", date=DAY, as_json=True, out=out) == 0
    events = [json.loads(x)["event"] for x in out.getvalue().splitlines()]
    assert events == ["a", "b", "c", "d"]


def test_level_filter_and_human_format(root: Path) -> None:
    out = io.StringIO()
    cli.show(root, "logs", ip="192.168.50.31", date=DAY, level="warning", out=out)
    lines = out.getvalue().splitlines()
    assert len(lines) == 2
    assert "WARNING" in lines[0] and "[w2]" in lines[0] and " b" in lines[0]
    assert "ERROR" in lines[1]


def test_jobs_format(root: Path) -> None:
    out = io.StringIO()
    cli.show(root, "jobs", ip="192.168.50.31", date=DAY, out=out)
    assert "done" in out.getvalue() and "job 4 probe" in out.getvalue()
    assert "120 ms" in out.getvalue()


def test_lists_machines_without_ip(root: Path) -> None:
    out = io.StringIO()
    assert cli.show(root, "logs", ip=None, date=None, out=out) == 0
    text = out.getvalue()
    assert "192.168.50.31" in text and DAY in text and "1 process(es)" in text
    assert "10.0.0.9" in text and "2026-10-18" in text


def test_follow_picks_up_new_lines_and_new_files(
    root: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    day_dir = root / "192.168.50.31" / "logs" / DAY
    writes: list[tuple[Path, dict[str, Any]]] = [
        (day_dir / "w1.jsonl", {"ts": f"{DAY}T11:00:00Z", "level": "info", "event": "e"}),
        (day_dir / "w3.jsonl", {"ts": f"{DAY}T11:00:01Z", "level": "info", "event": "f"}),
    ]

    def fake_sleep(_s: float) -> None:
        if writes:
            path, rec = writes.pop(0)
            _write(path, [rec])

    monkeypatch.setattr("maktaba_pipeline.cluster.cli.time.sleep", fake_sleep)
    out = io.StringIO()
    cli.show(
        root, "logs", ip="192.168.50.31", date=DAY, follow=True, as_json=True, out=out,
        max_polls=3,
    )  # fmt: skip
    events = [json.loads(x)["event"] for x in out.getvalue().splitlines()]
    assert events == ["a", "b", "c", "d", "e", "f"]


def test_cli_wiring(root: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    args = _build_parser().parse_args(
        ["cluster", "logs", "--ip", "192.168.50.31", "--date", DAY, "--dir", str(root)]
    )
    assert args.command == "cluster" and args.what == "logs"
    assert cli.find_dir(args) == root
    monkeypatch.setenv("MAKTABA_CLUSTER_DIR", str(root / "x"))
    args = _build_parser().parse_args(["cluster", "events"])
    assert cli.find_dir(args) == root / "x"


def test_status_reads_local_status_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("maktaba_pipeline.cluster.share.local_dir", lambda: tmp_path)
    out = io.StringIO()
    assert cli.status(out) == 1
    (tmp_path / "status").mkdir()
    (tmp_path / "status" / "w1.json").write_text(
        json.dumps(
            {
                "worker_id": "w1",
                "pid": 1,
                "kind": "node",
                "state": "connected",
                "url": "ws://h/api/cluster/agent",
                "session": "s1",
                "files": {"dir": "/share/x/1.2.3.4", "on_share": True},
                "updated_at": "now",
            }
        )
    )
    out = io.StringIO()
    assert cli.status(out) == 0
    assert "w1 pid 1 node: connected" in out.getvalue() and "session s1" in out.getvalue()
