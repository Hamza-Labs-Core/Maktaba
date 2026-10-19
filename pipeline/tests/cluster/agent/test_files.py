"""This process's files on the share: layout, caps, fallback, retention."""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from maktaba_pipeline.cluster import safefs
from maktaba_pipeline.cluster.agent.files import ClusterFiles, safe_name

pytestmark = pytest.mark.unit


def _files(tmp_path: Path, **kw: object) -> ClusterFiles:
    return ClusterFiles(
        tmp_path / "share" / "maktaba" / "cluster",
        "192.168.50.31",
        "node-ab12cd34-4242",
        fallback_dir=tmp_path / "local",
        **kw,  # type: ignore[arg-type]
    )


def _lines(p: Path) -> list[dict[str, object]]:
    return [json.loads(x) for x in p.read_text().splitlines()]


def test_layout_one_writer_per_file(tmp_path: Path) -> None:
    f = _files(tmp_path)
    assert f.on_share
    f.set_identity({"worker_id": "node-ab12cd34-4242", "pid": 4242, "role": "node"})
    f.write("logs", {"ts": "2026-10-19T10:00:00Z", "level": "info", "event": "a"})
    f.write("logs", {"ts": "2026-10-20T00:00:01Z", "level": "info", "event": "b"})
    f.write("metrics", {"t": "metrics", "ts": "2026-10-19T10:00:02Z"})
    f.write("jobs", {"t": "job", "event": "done", "ts": "2026-10-19T10:00:03Z"})
    f.write("llm", {"ts": "2026-10-19T10:00:04Z", "id": "r1", "model": "m"})
    assert f.flush() == 5
    m = tmp_path / "share" / "maktaba" / "cluster" / "192.168.50.31"
    ident = json.loads((m / "processes" / "node-ab12cd34-4242.json").read_text())
    assert ident["pid"] == 4242 and ident["stopped_at"] is None
    assert _lines(m / "logs" / "2026-10-19" / "node-ab12cd34-4242.jsonl")[0]["event"] == "a"
    assert _lines(m / "logs" / "2026-10-20" / "node-ab12cd34-4242.jsonl")[0]["event"] == "b"
    for stream in ("metrics", "jobs", "llm"):
        assert len(_lines(m / stream / "2026-10-19" / "node-ab12cd34-4242.jsonl")) == 1
    # appends across flushes
    f.write("logs", {"ts": "2026-10-19T10:00:05Z", "event": "c"})
    f.stop()
    assert len(_lines(m / "logs" / "2026-10-19" / "node-ab12cd34-4242.jsonl")) == 2
    st = f.status()
    assert st == {
        "ip": "192.168.50.31",
        "dir": str(m),
        "on_share": True,
        "reason": None,
        "dropped": 0,
    }
    ident = json.loads((m / "processes" / "node-ab12cd34-4242.json").read_text())
    assert ident["started_at"] and ident["last_seen"] and ident["stopped_at"]  # a clean stop


def test_file_cap_counts_what_it_does_not_write(tmp_path: Path) -> None:
    f = _files(tmp_path, file_cap_bytes=120)
    for i in range(10):
        f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": f"record-{i}"})
    written = f.flush()
    assert 0 < written < 10
    assert f.capped["logs"] == 10 - written
    assert f.status()["dropped"] == 10 - written


def test_share_not_writable_falls_back_to_local(tmp_path: Path) -> None:
    blocker = tmp_path / "share"
    blocker.write_text("a file where the share should be")
    f = _files(tmp_path)
    assert not f.on_share
    st = f.status()
    assert st["on_share"] is False and "share not writable" in str(st["reason"])
    assert st["dir"] == str(tmp_path / "local" / "192.168.50.31")
    f.write("metrics", {"ts": "2026-10-19T10:00:00Z"})
    f.flush()
    assert (tmp_path / "local/192.168.50.31/metrics/2026-10-19/node-ab12cd34-4242.jsonl").exists()


def test_no_share_at_all(tmp_path: Path) -> None:
    f = ClusterFiles(None, "10.0.0.2", "w", reason="no library roots", fallback_dir=tmp_path)
    assert f.status()["reason"] == "no library roots" and not f.on_share


def test_write_error_moves_to_local(tmp_path: Path) -> None:
    f = _files(tmp_path)
    assert f.on_share
    (tmp_path / "share/maktaba/cluster/192.168.50.31/logs").write_text("not a directory")
    f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": "first"})
    f.flush()
    assert not f.on_share and "write failed" in str(f.status()["reason"])
    assert f.failed == 0  # the failed write's records went to the local folder
    f.write("logs", {"ts": "2026-10-19T10:00:01Z", "event": "kept"})
    f.flush()
    p = tmp_path / "local/192.168.50.31/logs/2026-10-19/node-ab12cd34-4242.jsonl"
    assert [r["event"] for r in _lines(p)] == ["first", "kept"]


def test_a_failed_share_write_tries_the_share_again_soon(tmp_path: Path) -> None:
    now = [1000.0]
    f = _files(tmp_path, clock=lambda: now[0])
    logs = tmp_path / "share/maktaba/cluster/192.168.50.31/logs"
    logs.write_text("not a directory")
    f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": "a"})
    f.flush()
    assert not f.on_share
    logs.unlink()  # the share is back
    now[0] += 20  # well before the 5-minute retry
    f.tick()
    assert f.on_share


def test_a_dead_handle_is_reopened_and_the_share_kept(tmp_path: Path) -> None:
    """An SMB session that drops and reconnects (a NAS's I/O timeout) leaves the open
    handles dead: on Windows the next write fails with "[Errno 22] Invalid argument".
    The write is tried again on a fresh handle, the share kept, nothing lost."""
    f = _files(tmp_path)
    f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": "a"})
    f.write("metrics", {"ts": "2026-10-19T10:00:00Z"})
    assert f.flush() == 2
    for fh in list(f._files.values()):
        os.close(fh.fileno())  # the handles die under the writer
    f.write("logs", {"ts": "2026-10-19T10:00:01Z", "event": "b"})
    assert f.flush() == 1
    assert f.on_share and f.failed == 0 and f.status()["reason"] is None
    p = tmp_path / "share/maktaba/cluster/192.168.50.31/logs/2026-10-19/node-ab12cd34-4242.jsonl"
    assert [r["event"] for r in _lines(p)] == ["a", "b"]
    f.stop()


def test_a_line_cut_short_is_ended_before_the_next(tmp_path: Path) -> None:
    d = tmp_path / "share/maktaba/cluster/192.168.50.31/logs/2026-10-19"
    d.mkdir(parents=True)
    (d / "node-ab12cd34-4242.jsonl").write_bytes(b'{"event":"whole"}\n{"event":"cu')
    f = _files(tmp_path)
    f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": "next"})
    f.flush()
    f.stop()
    lines = (d / "node-ab12cd34-4242.jsonl").read_bytes().split(b"\n")
    assert lines[:2] == [b'{"event":"whole"}', b'{"event":"cu'] and lines[3:] == [b""]
    assert json.loads(lines[2])["event"] == "next"


def test_lines_end_in_lf_and_the_cap_counts_the_bytes_written(tmp_path: Path) -> None:
    """Windows' text mode would write CRLF: files unlike every other OS's, each line a
    byte longer than the cap counts."""
    f = _files(tmp_path)
    for i in range(50):
        f.write("logs", {"ts": "2026-10-19T10:00:00Z", "event": f"e{i}", "s": "ü"})
    f.flush()
    counted = f._sizes[("logs", "2026-10-19")]
    f.stop()
    p = tmp_path / "share/maktaba/cluster/192.168.50.31/logs/2026-10-19/node-ab12cd34-4242.jsonl"
    raw = p.read_bytes()
    assert b"\r" not in raw and raw.count(b"\n") == 50
    assert len(raw) == counted


def test_retention_removes_every_process_file_of_expired_days(tmp_path: Path) -> None:
    """Worker ids are new at every start: the files of earlier processes of this machine
    must expire too, or a folder the hub never sweeps (the local fallback) grows for ever."""
    f = _files(tmp_path, retention_days=14)
    m = tmp_path / "share/maktaba/cluster/192.168.50.31"
    old_ids = ("node-ab12cd34-1111", "node-ab12cd34-2222")  # two earlier processes
    for day in ("2026-09-01", "2026-10-04", "2026-10-05", "2026-10-19"):
        for stream in ("logs", "metrics"):
            d = m / stream / day
            d.mkdir(parents=True)
            for wid in (*old_ids, "node-ab12cd34-4242"):
                (d / f"{wid}.jsonl").write_text("{}\n")
    (m / "logs/2026-09-01/notes.txt").write_text("not a record file")
    assert f.prune(today="2026-10-19") == 2 * 2 * 3  # two expired days, two kinds, three ids
    assert not (m / "metrics/2026-09-01").exists() and not (m / "logs/2026-10-04").exists()
    assert [p.name for p in (m / "logs/2026-09-01").iterdir()] == ["notes.txt"]  # kept
    for wid in (*old_ids, "node-ab12cd34-4242"):
        assert (m / f"logs/2026-10-05/{wid}.jsonl").exists()  # not expired yet
        assert (m / f"metrics/2026-10-19/{wid}.jsonl").exists()


def test_retention_in_the_local_fallback_drops_gone_processes(tmp_path: Path) -> None:
    f = ClusterFiles(None, "10.0.0.7", "node-ab12cd34-4242", fallback_dir=tmp_path / "local",
                     retention_days=14)  # fmt: skip
    f.set_identity({"worker_id": "node-ab12cd34-4242"})
    procs = tmp_path / "local/10.0.0.7/processes"
    (procs / "node-ab12cd34-1111.json").write_text(
        json.dumps({"last_seen": "2026-09-01T00:00:00Z"})
    )
    (procs / "node-ab12cd34-2222.json").write_text(
        json.dumps({"last_seen": "2026-10-18T00:00:00Z"})
    )
    assert f.prune(today="2026-10-19") == 1
    names = sorted(p.name for p in procs.iterdir())
    assert names == ["node-ab12cd34-2222.json", "node-ab12cd34-4242.json"]


def test_safe_name() -> None:
    assert safe_name("node-ab12/x:y 1") == "node-ab12_x_y_1"
    assert safe_name("...") == "worker"
    assert os.sep not in safe_name("a/b\\c")


def test_macos_sidecars_are_not_records_and_expire_with_their_files(tmp_path: Path) -> None:
    """macOS writes a "._<name>" sidecar beside every file on a share that cannot keep
    extended attributes: readers skip it, and retention removes it with its file."""
    f = ClusterFiles(None, "10.0.0.7", "node-ab12cd34-4242", fallback_dir=tmp_path / "local",
                     retention_days=14)  # fmt: skip
    f.set_identity({"worker_id": "node-ab12cd34-4242"})
    m = tmp_path / "local/10.0.0.7"
    old = m / "logs/2026-09-01"
    old.mkdir(parents=True)
    (old / "w.jsonl").write_text("{}\n")
    (old / "._w.jsonl").write_bytes(b"\x00\x05\x16\x07Mac OS X")
    assert [p.name for p in safefs.list_real_files(old)] == ["w.jsonl"]
    assert sorted(p.name for p in safefs.list_real_files(old, hidden=True)) == [
        "._w.jsonl",
        "w.jsonl",
    ]
    procs = m / "processes"
    (procs / "node-ab12cd34-1111.json").write_text(
        json.dumps({"last_seen": "2026-09-01T00:00:00Z"})
    )
    (procs / "._node-ab12cd34-1111.json").write_bytes(b"\x00\x05\x16\x07")
    assert [p.name for p in safefs.list_real_files(procs, ".json")] == [
        "node-ab12cd34-1111.json",
        "node-ab12cd34-4242.json",
    ]
    f.prune(today="2026-10-19")
    assert not old.exists()  # the day folder goes, its sidecars with it
    assert sorted(p.name for p in procs.iterdir()) == ["node-ab12cd34-4242.json"]
