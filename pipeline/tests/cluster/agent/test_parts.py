"""Small pieces: the jobs file sink, the hub's settings for the files, log record limits,
``cluster where``."""

from __future__ import annotations

import argparse
import io
import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import cli
from maktaba_pipeline.cluster.agent.files import ClusterFiles
from maktaba_pipeline.cluster.agent.logship import LogShipper, make_record
from maktaba_pipeline.cluster.agent.service import JobsFileSink

pytestmark = pytest.mark.unit


class Clock:
    t = 0.0

    def __call__(self) -> float:
        return self.t


def _job(event: str, job_id: int = 1, **kw: Any) -> dict[str, Any]:
    return {"t": "job", "event": event, "job_id": job_id, "ts": "2026-10-19T00:00:00Z", **kw}


def test_jobs_file_progress_at_most_every_10_s(tmp_path: Path) -> None:
    files = ClusterFiles(tmp_path, "10.0.0.1", "w", fallback_dir=tmp_path / "l")
    sink = JobsFileSink(files)
    clock = Clock()
    sink.clock = clock
    sink(_job("claimed"))
    for _ in range(5):
        sink(_job("progress"))
        clock.t += 3
    sink(_job("progress", job_id=2))
    sink(_job("done"))
    files.flush()
    path = tmp_path / "10.0.0.1" / "jobs" / "2026-10-19" / "w.jsonl"
    lines = [json.loads(x) for x in path.read_text().splitlines()]
    assert [(x["event"], x["job_id"]) for x in lines] == [
        ("claimed", 1),
        ("progress", 1),  # t=0
        ("progress", 1),  # t=12
        ("progress", 2),
        ("done", 1),
    ]
    assert all("t" not in x for x in lines)  # the frame without t


def test_the_hubs_settings_for_the_files(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_CLUSTER_RETENTION_DAYS", raising=False)
    monkeypatch.setenv("MAKTABA_CLUSTER_FILE_MB", "5")
    files = ClusterFiles(None, "10.0.0.1", "w", reason="no library roots",
                         fallback_dir=tmp_path / "l")  # fmt: skip
    assert not files.on_share
    files.apply_settings(retention_days=3, file_mb=999)
    assert files.retention_days == 3  # the hub's
    assert files.file_cap == 5 * 1024 * 1024  # this machine's environment wins
    files.set_share_dir(tmp_path / "share", "share root /x")
    assert files.on_share and files.status()["dir"] == str(tmp_path / "share" / "10.0.0.1")


def test_log_record_limits() -> None:
    fields = {f"f{i}": "x" * 1_900 for i in range(60)}  # each value fits, together over 64 KiB
    big = make_record({"level": "info", "event": "e", **fields})
    assert big["truncated"] is True and len(json.dumps(big)) < 64 * 1024
    assert "truncated" not in make_record({"level": "info", "event": "e", "n": 1})
    s = LogShipper("info", capacity=1)
    s.sink({"level": "info", "event": "a"})
    s.sink({"level": "info", "event": "b"})
    n, since = s.take_dropped_span()
    assert n == 1 and since is not None and since.endswith("Z")
    assert s.take_dropped_span() == (0, None)


def test_cluster_where(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("maktaba_pipeline.cluster.share.local_dir", lambda: tmp_path / "local")
    args = argparse.Namespace(dir=str(tmp_path), database_url=None, path_map=None)
    out = io.StringIO()
    assert cli.where(args, out) == 0
    assert f"cluster directory: {tmp_path}" in out.getvalue() and "--dir" in out.getvalue()
