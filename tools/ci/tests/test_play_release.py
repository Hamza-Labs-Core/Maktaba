"""What play-bundles.yml hands Google Play (tools/ci/play_release.py): each app's package from
its real Gradle build file, and the release notes from a tag's message."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import play_release as pr  # noqa: E402


@pytest.mark.parametrize(
    ("app", "pkg"),
    [
        ("player", "com.hamzalabs.maktaba.player"),
        ("remote", "com.hamzalabs.maktaba.remote"),
        ("tv", "com.hamzalabs.maktaba.tv"),
    ],
)
def test_package_is_the_gradle_application_id(app: str, pkg: str) -> None:
    assert pr.package(app) == pkg


def test_the_three_apps_are_three_packages() -> None:
    assert len({pr.package(app) for app in pr.GRADLE}) == 3


def test_play_bundles_uploads_every_app_by_its_gradle_package() -> None:
    workflow = (pr.ROOT / ".github/workflows/play-bundles.yml").read_text(encoding="utf-8")
    for app in pr.GRADLE:
        assert f"tools/ci/play_release.py package {app}" in workflow
        # the package is read, never written out a second time
        assert f"package: {pr.package(app)}" not in workflow


def test_unknown_app_is_refused() -> None:
    with pytest.raises(pr.PlayReleaseError, match="unknown app"):
        pr.package("desktop")


@pytest.mark.parametrize(
    ("text", "match"),
    [
        ("android { defaultConfig { } }", "found 0"),
        ('applicationId = "a.b"\napplicationId = "a.c"', "found 2"),
        ('applicationId = "not a package"', "not a package name"),
        ('applicationId = "single"', "not a package name"),
    ],
)
def test_build_file_without_one_package_is_refused(text: str, match: str) -> None:
    with pytest.raises(pr.PlayReleaseError, match=match):
        pr.package_in(text)


def test_package_in_reads_kotlin_dsl() -> None:
    text = 'android {\n    defaultConfig {\n        applicationId = "com.example.app"\n    }\n}\n'
    assert pr.package_in(text) == "com.example.app"


def test_tag_with_only_its_name_gets_the_standard_notes() -> None:
    notes = pr.whatsnew("v0.3.55", "v0.3.55\n")
    assert set(notes) == {"en-US", "ar"}
    assert notes["en-US"].startswith("Maktaba 0.3.55 for testing.")
    assert "v0.3.55" in notes["en-US"]
    assert notes["ar"].startswith("مكتبة 0.3.55")
    assert "v0.3.55" in notes["ar"]


def test_lightweight_tag_gets_the_standard_notes() -> None:
    notes = pr.whatsnew("v0.4.0-rc.1", "")
    assert "0.4.0-rc.1" in notes["en-US"]
    assert "0.4.0-rc.1" in notes["ar"]


def test_message_gives_english_and_arabic() -> None:
    msg = "Faster search.\nFixes the TV remote.\n--- ar ---\nبحث أسرع.\nإصلاح جهاز التحكم.\n"
    notes = pr.whatsnew("v0.3.55", msg)
    assert notes["en-US"] == "Faster search.\nFixes the TV remote."
    assert notes["ar"] == "بحث أسرع.\nإصلاح جهاز التحكم."


def test_english_only_message_keeps_standard_arabic() -> None:
    notes = pr.whatsnew("v0.3.55", "Faster search.\n")
    assert notes["en-US"] == "Faster search."
    assert notes["ar"].startswith("مكتبة 0.3.55")


def test_arabic_only_message_keeps_standard_english() -> None:
    notes = pr.whatsnew("v0.3.55", "--- ar ---\nبحث أسرع.\n")
    assert notes["en-US"].startswith("Maktaba 0.3.55")
    assert notes["ar"] == "بحث أسرع."


def test_signature_is_dropped() -> None:
    msg = "Faster search.\n-----BEGIN PGP SIGNATURE-----\nabc\n-----END PGP SIGNATURE-----\n"
    assert pr.whatsnew("v0.3.55", msg)["en-US"] == "Faster search."


def test_long_notes_are_cut_to_plays_limit_in_characters() -> None:
    en = " ".join(["word"] * 300)
    ar = "كلمة " * 300  # more than 500 characters, far more bytes
    notes = pr.whatsnew("v0.3.55", f"{en}\n--- ar ---\n{ar}")
    for note in notes.values():
        assert len(note) <= pr.MAX_NOTE
        assert note.endswith("…")
    assert len(notes["ar"]) > pr.MAX_NOTE // 2


def test_bad_tag_is_refused() -> None:
    with pytest.raises(pr.PlayReleaseError):
        pr.whatsnew("0.3.55", "")


def test_write_whatsnew_names_files_for_the_upload_action(tmp_path: Path) -> None:
    paths = pr.write_whatsnew("v0.3.55", "Faster search.\n", tmp_path / "whatsnew")
    assert sorted(p.name for p in paths) == ["whatsnew-ar", "whatsnew-en-US"]
    assert (tmp_path / "whatsnew" / "whatsnew-en-US").read_text(encoding="utf-8") == "Faster search.\n"


def test_main(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    assert pr.main(["package", "tv"]) == 0
    assert capsys.readouterr().out.strip() == "com.hamzalabs.maktaba.tv"
    msg = tmp_path / "msg"
    msg.write_text("v0.3.55\n", encoding="utf-8")
    assert pr.main(["whatsnew", "v0.3.55", str(msg), str(tmp_path / "out")]) == 0
    assert (tmp_path / "out" / "whatsnew-ar").exists()
    assert pr.main(["package", "nope"]) == 1
    assert pr.main([]) == 2
