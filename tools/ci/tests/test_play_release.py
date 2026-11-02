"""What play-bundles.yml hands Google Play (tools/ci/play_release.py): each app's package from
its real Gradle build file, and the release notes from a tag's message."""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

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


PLAY_BUNDLES = pr.ROOT / ".github/workflows/play-bundles.yml"
# google-github-actions/auth v2.1.13
GOOGLE_AUTH = "google-github-actions/auth@c200f3691d83b41bf9bbd8638997a462592937ed"


def _play_bundles() -> dict[str, Any]:
    workflow: dict[str, Any] = yaml.safe_load(PLAY_BUNDLES.read_text(encoding="utf-8"))
    return workflow


def test_play_upload_is_keyless_no_json_secret_left() -> None:
    # Workload Identity Federation only: no service-account key, in any form
    text = PLAY_BUNDLES.read_text(encoding="utf-8")
    assert "PLAY_SERVICE_ACCOUNT_JSON" not in text
    assert "serviceAccountJsonPlainText" not in text
    for doc in ("docs/google-play.md", "tools/ci/play_release.py", "README.md"):
        assert "PLAY_SERVICE_ACCOUNT_JSON" not in (pr.ROOT / doc).read_text(encoding="utf-8")


def test_play_upload_is_gated_on_the_two_variables() -> None:
    bundles = _play_bundles()["jobs"]["bundles"]
    gate = bundles["env"]["HAS_PLAY_ACCOUNT"]
    assert gate == "${{ vars.PLAY_WIF_PROVIDER != '' && vars.PLAY_SERVICE_ACCOUNT != '' }}"
    play_step = next(s for s in bundles["steps"] if s.get("id") == "play")
    run = str(play_step["run"])
    assert '"$HAS_PLAY_ACCOUNT" != true' in run
    assert "Play upload skipped: PLAY_WIF_PROVIDER or PLAY_SERVICE_ACCOUNT not set" in run


def test_only_the_play_job_may_mint_an_oidc_token() -> None:
    workflow = _play_bundles()
    assert workflow["permissions"] == {"contents": "read"}
    for name, job in workflow["jobs"].items():
        perms = job.get("permissions", {})
        if name == "play":
            assert perms == {"id-token": "write"}
        else:
            assert "id-token" not in perms, name


def test_play_job_signs_in_with_pinned_auth_then_uploads_with_its_file() -> None:
    play = _play_bundles()["jobs"]["play"]
    assert play["if"] == "needs.bundles.outputs.play == 'true'"
    steps: list[dict[str, Any]] = play["steps"]
    # no checkout (it would wipe the credentials file) and no secret in this job
    assert not any(str(s.get("uses", "")).startswith("actions/checkout") for s in steps)
    assert "secrets." not in yaml.safe_dump(play)
    uses = [str(s.get("uses", "")) for s in steps]
    auth_at = uses.index(GOOGLE_AUTH)
    upload_at = next(i for i, u in enumerate(uses) if u.startswith("r0adkll/upload-google-play@"))
    assert auth_at < upload_at
    auth = steps[auth_at]
    assert auth["with"] == {
        "workload_identity_provider": "${{ vars.PLAY_WIF_PROVIDER }}",
        "service_account": "${{ vars.PLAY_SERVICE_ACCOUNT }}",
        "create_credentials_file": True,
    }
    text = PLAY_BUNDLES.read_text(encoding="utf-8")
    assert f"# google-github-actions/auth v2.1.13\n        uses: {GOOGLE_AUTH}\n" in text
    upload = steps[upload_at]["with"]
    credentials = f"${{{{ steps.{auth['id']}.outputs.credentials_file_path }}}}"
    assert upload["serviceAccountJson"] == credentials
    assert upload["track"] == "internal"
    assert upload["status"] == "completed"
    assert upload["whatsNewDirectory"] == "play/whatsnew"


def _mobile_release_android_steps() -> list[dict[str, object]]:
    text = (pr.ROOT / ".github/workflows/mobile-release.yml").read_text(encoding="utf-8")
    workflow = yaml.safe_load(text)
    steps: list[dict[str, object]] = workflow["jobs"]["android"]["steps"]
    return steps


def test_capacitor_app_takes_its_version_from_play_version_sh() -> None:
    # mobile-release.yml's APK and AAB get the versionCode rule play-bundles.yml's do (one
    # copy, tools/release/play-version.sh), before anything is built, so a bad tag fails first
    steps = _mobile_release_android_steps()
    ids = [s.get("id") for s in steps]
    assert ids.count("version") == 1
    at = ids.index("version")
    version = steps[at]
    assert 'tools/release/play-version.sh "$TAG"' in str(version["run"])
    assert '"$GITHUB_OUTPUT"' in str(version["run"])
    runs = [str(s.get("run", "")) for s in steps]
    built = [i for i, run in enumerate(runs) if "pnpm build" in run or "gradlew" in run]
    assert built and at < min(built)


def test_capacitor_stamp_uses_the_version_step_and_no_formula_of_its_own() -> None:
    steps = _mobile_release_android_steps()
    stamp = next(s for s in steps if s.get("name") == "Stamp Android version from tag")
    env = stamp["env"]
    assert isinstance(env, dict)
    assert env["CODE"] == "${{ steps.version.outputs.code }}"
    assert env["NAME"] == "${{ steps.version.outputs.name }}"
    run = str(stamp["run"])
    assert "versionCode ${CODE}" in run
    assert "1000000" not in run and "$((" not in run
    workflow = (pr.ROOT / ".github/workflows/mobile-release.yml").read_text(encoding="utf-8")
    assert "* 1000000" not in workflow


def test_capacitor_app_is_its_own_play_package() -> None:
    # its codes so far came only from mobile-release.yml (3055 at most, v0.3.55), never from
    # the native apps' bundles: tools/release/play-version-test.sh checks the new ones are above
    config = (pr.ROOT / "apps/mobile/capacitor.config.ts").read_text(encoding="utf-8")
    assert "appId: 'com.hamzalabs.maktaba'," in config
    assert "com.hamzalabs.maktaba" not in {pr.package(app) for app in pr.GRADLE}


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
