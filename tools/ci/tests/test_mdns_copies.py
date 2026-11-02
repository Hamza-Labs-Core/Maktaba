"""The two copies of the mDNS announcement stay the same (tools/ci/mdns_copies.py): on the
repository itself, and on a copy of it with one file changed, added or removed."""

from __future__ import annotations

import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import mdns_copies  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]


def _copy(tmp_path: Path) -> Path:
    for c in mdns_copies.COPIES:
        shutil.copytree(ROOT / c, tmp_path / c)
    return tmp_path


def test_the_repository_copies_are_the_same() -> None:
    assert mdns_copies.problems(ROOT) == []
    assert mdns_copies.main(["mdns_copies.py", str(ROOT)]) == 0


def test_a_change_in_one_copy_is_found(tmp_path: Path) -> None:
    root = _copy(tmp_path)
    f = root / mdns_copies.COPIES[1] / "announce_resolved_linux.go"
    f.write_text(f.read_text().replace("10*time.Second", "11*time.Second"))
    assert mdns_copies.problems(root) == [
        "api/internal/discovery/announce_resolved_linux.go and "
        "cmd/maktaba-server/internal/mdns/announce_resolved_linux.go differ"
    ]
    assert mdns_copies.main(["mdns_copies.py", str(root)]) == 1


def test_a_file_in_one_copy_only_is_found(tmp_path: Path) -> None:
    root = _copy(tmp_path)
    (root / mdns_copies.COPIES[0] / "announce_new.go").write_text("package discovery\n")
    (root / mdns_copies.COPIES[0] / "host_test.go").unlink()
    got = mdns_copies.problems(root)
    assert len(got) == 2
    assert got[0].startswith("api/internal/discovery/announce_new.go has no copy in cmd/")
    assert got[1].startswith("cmd/maktaba-server/internal/mdns/host_test.go has no copy in api/")


def test_only_the_package_line_may_differ(tmp_path: Path) -> None:
    root = _copy(tmp_path)
    a = root / mdns_copies.COPIES[0] / "host.go"
    assert "package discovery" in a.read_text()
    # each module's own files differ freely
    (root / mdns_copies.COPIES[1] / "mdns.go").write_text("package mdns\n// changed\n")
    assert mdns_copies.problems(root) == []
    a.write_text(a.read_text().replace("package discovery", "package discovery // x"))
    assert mdns_copies.problems(root) != []
