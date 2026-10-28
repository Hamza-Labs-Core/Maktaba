"""Story 47.3: FileUtilities' Unicode open fallback (as given, NFC, NFD, then ``ditto``)."""

from __future__ import annotations

import os
import unicodedata
from pathlib import Path

import pytest

from maktaba_pipeline.library_mgmt.unicode_open import candidates, open_fallback

pytestmark = pytest.mark.unit

ARABIC = "محاضرة الأولى"  # has a hamza: NFC and NFD differ
NFC = unicodedata.normalize("NFC", ARABIC)
NFD = unicodedata.normalize("NFD", ARABIC)


def test_the_two_forms_differ() -> None:
    assert NFC != NFD


def test_a_path_that_opens_as_given(tmp_path: Path) -> None:
    f = tmp_path / "plain.mp4"
    f.write_bytes(b"x")
    got = open_fallback(str(f))
    assert (got.path, got.how, got.copied) == (str(f), "as_given", False)


def test_an_nfd_name_opens_from_its_nfc_spelling(tmp_path: Path) -> None:
    # the share stores NFD; the database asks in NFC. The opener only knows the NFD name
    on_disk = os.path.join(str(tmp_path), NFD + ".mp4")
    asked = os.path.join(str(tmp_path), NFC + ".mp4")
    Path(on_disk).write_bytes(b"x")
    got = open_fallback(asked, can_open=lambda p: p == on_disk)
    assert (got.path, got.how) == (on_disk, "nfd")


def test_an_nfc_name_opens_from_its_nfd_spelling(tmp_path: Path) -> None:
    on_disk = os.path.join(str(tmp_path), NFC + ".mp4")
    asked = os.path.join(str(tmp_path), NFD + ".mp4")
    got = open_fallback(asked, can_open=lambda p: p == on_disk)
    assert (got.path, got.how) == (on_disk, "nfc")


def test_a_real_nfd_file_opens_whatever_spelling_is_asked(tmp_path: Path) -> None:
    Path(os.path.join(str(tmp_path), NFD + ".mp4")).write_bytes(b"x")
    got = open_fallback(os.path.join(str(tmp_path), NFC + ".mp4"))
    with open(got.path, "rb") as f:
        assert f.read() == b"x"


def test_on_macos_ditto_copies_into_the_work_folder(tmp_path: Path) -> None:
    calls: list[tuple[str, str]] = []
    work = tmp_path / "work"

    def ditto(src: str, dest: str) -> bool:
        calls.append((src, dest))
        Path(dest).write_bytes(b"copied")
        return True

    asked = os.path.join(str(tmp_path), "share", NFC + ".mp4")

    def can_open(p: str) -> bool:
        return p.startswith(str(work))

    got = open_fallback(asked, str(work), can_open=can_open, ditto=ditto, platform="darwin")
    assert got.how == "ditto" and got.copied
    assert got.path.startswith(str(work))
    assert calls[0][0] == asked


def test_no_ditto_off_macos_and_nothing_opens(tmp_path: Path) -> None:
    def ditto(src: str, dest: str) -> bool:
        raise AssertionError("ditto is macOS only")

    with pytest.raises(FileNotFoundError):
        open_fallback(
            str(tmp_path / "gone.mp4"),
            str(tmp_path / "w"),
            can_open=lambda p: False,
            ditto=ditto,
            platform="linux",
        )


def test_candidates_skip_a_spelling_already_tried() -> None:
    assert [h for h, _ in candidates("/a/plain.mp4")] == ["as_given"]
    assert [h for h, _ in candidates("/a/" + NFD)] == ["as_given", "nfc"]
    assert [h for h, _ in candidates("/a/" + NFC)] == ["as_given", "nfd"]
