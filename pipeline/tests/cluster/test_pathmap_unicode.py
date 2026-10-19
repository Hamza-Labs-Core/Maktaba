"""PathMap.to_local finds a file whatever Unicode form its path was stored in.

The Mac home stores names NFD (``أ`` = alef + U+0654); a Windows node opens a name on
the share only in the form the share holds (NFC). These are two real names from the
library that failed on the Windows node.
"""

from __future__ import annotations

import unicodedata
from pathlib import Path

import pytest

from maktaba_pipeline.engine import paths
from maktaba_pipeline.engine.paths import PathMap, find_existing

pytestmark = pytest.mark.unit

NAMES = [
    "اخسأ، فلن تعدو قدرك 🔥🔥🔥.mp4",
    "أبو عمر الباحث يسحق صاحب النكسة الفكرية من جديد.mp4",
]


def _nfd(s: str) -> str:
    return unicodedata.normalize("NFD", s)


def _nfc(s: str) -> str:
    return unicodedata.normalize("NFC", s)


@pytest.fixture(autouse=True)
def _fresh_listings() -> None:
    paths.forget_listings()


def _share(tmp_path: Path) -> Path:
    d = tmp_path / "Originals" / "Backup" / "Youtube" / "AntiShubohat"
    d.mkdir(parents=True)
    return tmp_path / "Originals"


def _nfd_differs() -> None:
    assert all(_nfd(n) != _nfc(n) for n in NAMES)


@pytest.mark.parametrize("name", NAMES)
def test_nfd_stored_path_opens_the_nfc_file(tmp_path: Path, name: str) -> None:
    _nfd_differs()
    share = _share(tmp_path)
    f = share / "Backup" / "Youtube" / "AntiShubohat" / _nfc(name)
    f.write_bytes(b"x")
    pm = PathMap.parse(f"/Volumes/Originals/={share.as_posix()}/")
    stored = "/Volumes/Originals/Backup/Youtube/AntiShubohat/" + _nfd(name)
    local = pm.to_local(stored)
    assert Path(local).read_bytes() == b"x"
    assert _nfc(local) == _nfc(f.as_posix())


def test_mixed_forms_found_component_by_component(tmp_path: Path) -> None:
    share = _share(tmp_path)
    folder = share / "Backup" / _nfc("قناة أ")  # the folder is NFC on the share
    folder.mkdir()
    f = folder / _nfd(NAMES[1])  # the file NFD
    f.write_bytes(b"y")
    stored = (folder.parent / _nfc("قناة أ") / _nfc(NAMES[1])).as_posix()
    if Path(stored).exists():  # a normalising file system (macOS): nothing to find
        assert find_existing(stored) == stored
        return
    assert Path(find_existing(stored)).read_bytes() == b"y"


def test_ascii_existing_and_missing_paths_are_untouched(tmp_path: Path) -> None:
    assert find_existing("/nope/a.mp4") == "/nope/a.mp4"
    missing = (tmp_path / _nfd(NAMES[0])).as_posix()
    assert find_existing(missing) == missing  # a file still to be written
    ok = tmp_path / _nfc(NAMES[0])
    ok.write_bytes(b"z")
    assert find_existing(ok.as_posix()) == ok.as_posix()
    # unmapped paths get the same help
    assert Path(PathMap().to_local((tmp_path / _nfd(NAMES[0])).as_posix())).read_bytes() == b"z"
