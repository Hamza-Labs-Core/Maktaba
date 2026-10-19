"""Links planted on the share never redirect a write, a read or a deletion."""

from __future__ import annotations

import io
import json
import os
from pathlib import Path

import pytest

from maktaba_pipeline.cluster import cli, safefs
from maktaba_pipeline.cluster.agent.files import ClusterFiles

pytestmark = [
    pytest.mark.unit,
    pytest.mark.skipif(os.name == "nt", reason="symlinks need privileges on Windows"),
]

IP = "192.168.50.31"
WID = "node-ab12cd34-1"
DAY = "2026-10-19"


def _files(root: Path, **kw: object) -> ClusterFiles:
    return ClusterFiles(root, IP, WID, fallback_dir=root.parent / "local", **kw)  # type: ignore[arg-type]


def _outside(tmp_path: Path) -> Path:
    out = tmp_path / "outside"
    out.mkdir()
    (out / "keep.txt").write_text("precious")
    return out


def _snapshot(d: Path) -> list[str]:
    return sorted(str(p.relative_to(d)) for p in d.rglob("*"))


def test_a_linked_ip_folder_is_refused(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    root.mkdir()
    (root / IP).symlink_to(outside, target_is_directory=True)
    f = _files(root)
    assert not f.on_share and "not a real directory" in str(f.reason)
    f.write("logs", {"ts": f"{DAY}T00:00:00Z", "event": "x"})
    f.flush()
    assert _snapshot(outside) == ["keep.txt"]  # nothing written through the link


def test_a_linked_kind_or_day_folder_is_refused(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    f = _files(root)
    assert f.on_share
    (root / IP / "logs").symlink_to(outside, target_is_directory=True)
    (root / IP / "metrics").mkdir()
    (root / IP / "metrics" / DAY).symlink_to(outside, target_is_directory=True)
    f.write("logs", {"ts": f"{DAY}T00:00:00Z", "event": "x"})
    f.write("metrics", {"ts": f"{DAY}T00:00:00Z"})
    f.flush()
    assert _snapshot(outside) == ["keep.txt"]
    assert f.failed >= 1


def test_a_linked_file_is_never_appended_to(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    f = _files(root)
    d = root / IP / "jobs" / DAY
    d.mkdir(parents=True)
    (d / f"{WID}.jsonl").symlink_to(outside / "keep.txt")
    f.write("jobs", {"ts": f"{DAY}T00:00:00Z", "event": "done"})
    f.flush()
    assert (outside / "keep.txt").read_text() == "precious"
    with pytest.raises(safefs.UnsafePath):
        safefs.open_append(d / f"{WID}.jsonl")


def test_a_linked_identity_file_is_replaced_not_followed(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    (root / IP / "processes").mkdir(parents=True)
    (root / IP / "processes" / f"{WID}.json").symlink_to(outside / "keep.txt")
    f = _files(root)
    f.set_identity({"worker_id": WID})
    assert (outside / "keep.txt").read_text() == "precious"
    target = root / IP / "processes" / f"{WID}.json"
    assert not target.is_symlink() and json.loads(target.read_text())["worker_id"] == WID


def test_retention_never_follows_links(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    f = _files(root, retention_days=1)
    # an old day folder that is a link to elsewhere holding a file named like ours
    (outside / f"{WID}.jsonl").write_text("{}\n")
    (root / IP / "logs").mkdir(parents=True)
    (root / IP / "logs" / "2026-01-01").symlink_to(outside, target_is_directory=True)
    # an old real day folder whose "own file" is a link to a file outside
    (root / IP / "logs" / "2026-01-02").mkdir()
    (root / IP / "logs" / "2026-01-02" / f"{WID}.jsonl").symlink_to(outside / "keep.txt")
    # a linked kind folder
    (root / IP / "metrics").symlink_to(outside, target_is_directory=True)
    assert f.prune(today=DAY) == 0
    assert _snapshot(outside) == ["keep.txt", f"{WID}.jsonl"]
    assert (outside / "keep.txt").read_text() == "precious"


def test_the_reader_refuses_links(tmp_path: Path) -> None:
    root, outside = tmp_path / "cluster", _outside(tmp_path)
    (outside / "w.jsonl").write_text(json.dumps({"ts": "t", "event": "secret"}) + "\n")
    day = root / IP / "logs" / DAY
    day.mkdir(parents=True)
    (day / "w.jsonl").symlink_to(outside / "w.jsonl")
    (day / "real.jsonl").write_text(json.dumps({"ts": "t", "event": "fine"}) + "\n")
    out = io.StringIO()
    cli.show(root, "logs", ip=IP, date=DAY, as_json=True, out=out)
    assert "secret" not in out.getvalue() and "fine" in out.getvalue()
    (root / "10.0.0.1").symlink_to(outside, target_is_directory=True)
    linked = root / "10.0.0.2" / "logs"
    linked.mkdir(parents=True)
    (linked / DAY).symlink_to(outside, target_is_directory=True)
    out = io.StringIO()
    assert cli.show(root, "logs", ip="10.0.0.2", date=DAY, out=out) == 1
    out = io.StringIO()
    cli.ls(root, out)
    assert "10.0.0.1" not in out.getvalue() and IP in out.getvalue()


def test_bad_components() -> None:
    for bad in ("..", "", "a/b", "a\\b", "."):
        with pytest.raises(safefs.UnsafePath):
            safefs.safe_dir(Path("/tmp"), [bad], create=False)


def test_new_folders_are_group_writable_whatever_the_umask(tmp_path: Path) -> None:
    """The hub and the home's workers run as different users of one group: a folder one
    creates must take the other's files. Existing folders and files keep their modes."""
    root = tmp_path / "cluster"
    root.mkdir()
    (root / IP).mkdir(mode=0o700)  # made by someone else, before us
    os.chmod(root / IP, 0o700)
    old = os.umask(0o022)
    try:
        f = _files(root)
        f.write("logs", {"ts": f"{DAY}T00:00:00Z", "event": "x"})
        f.flush()
    finally:
        os.umask(old)

    def mode(p: Path) -> int:
        return os.lstat(p).st_mode & 0o777

    assert mode(root / IP) == 0o700  # not ours: untouched
    for d in (root / IP / "processes", root / IP / "logs", root / IP / "logs" / DAY):
        assert mode(d) == 0o775, d
    assert mode(root / IP / "logs" / DAY / f"{WID}.jsonl") == 0o644
