"""The first-party packages' build (Epic 54, story 54.27 §2, edge cases):
``tools/plugins/build.py``. The release's trust check over its output is
``api/internal/plugins/firstparty/release_test.go`` (T01)."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tomllib
import zipfile
from pathlib import Path

import pytest

from tests.plugins.firstparty.conftest import ROOT

sys.path.insert(0, str(ROOT / "tools" / "plugins"))

import build  # noqa: E402


@pytest.mark.parametrize(
    ("release", "wheel"),
    [("0.3.152", "0.3.152"), ("1.2.3-rc.1", "1.2.3rc1"), ("1.0.0-beta.2", "1.0.0b2")],
)
def test_the_wheel_version_follows_the_release(release: str, wheel: str) -> None:
    assert build.pep440(release) == wheel


@pytest.mark.parametrize("bad", ["0.3", "v0.3.1", "0.3.1+build", "0.3.1-nightly"])
def test_a_version_with_no_wheel_form_is_refused(bad: str) -> None:
    with pytest.raises(build.BuildError):
        build.pep440(bad)


def test_the_manifest_is_stamped_with_the_release() -> None:
    text = (ROOT / "plugins" / "first-party" / "classify" / "manifest.toml").read_text()
    out = build.stamp(text, "0.3.152")
    m = tomllib.loads(out)
    assert (m["version"], m["maktaba"]) == ("0.3.152", "=0.3.152")
    # only the two lines change
    before = [line for line in text.splitlines() if not line.startswith(("version ", "maktaba "))]
    after = [line for line in out.splitlines() if not line.startswith(("version ", "maktaba "))]
    assert before == after


def test_a_manifest_without_a_version_line_is_refused() -> None:
    with pytest.raises(build.BuildError, match="version"):
        build.stamp('schema = 1\nmaktaba = ">=0"\n', "1.0.0")


def _copy_plugin(tmp_path: Path, name: str = "convert") -> Path:
    dst = tmp_path / name
    src = ROOT / "plugins" / "first-party" / name
    shutil.copytree(src, dst, ignore=shutil.ignore_patterns("__pycache__", "tests"))
    return dst


def test_a_plugin_with_no_stage_is_refused(tmp_path: Path) -> None:
    folder = _copy_plugin(tmp_path)
    text = (folder / "manifest.toml").read_text()
    (folder / "manifest.toml").write_text(text.split("\n[[stage]]", 1)[0] + "\n")
    with pytest.raises(build.BuildError, match="no stage"):
        build.contents(folder, "1.0.0")


def test_a_third_party_id_is_refused(tmp_path: Path) -> None:
    folder = _copy_plugin(tmp_path)
    text = (folder / "manifest.toml").read_text()
    (folder / "manifest.toml").write_text(
        text.replace('"tv.maktaba.convert"', '"dev.example.convert"')
    )
    with pytest.raises(build.BuildError, match="not a Maktaba id"):
        build.contents(folder, "1.0.0")


def test_a_package_holds_the_manifest_strings_statements_and_wheel() -> None:
    folder = ROOT / "plugins" / "first-party" / "convert"
    plugin_id, files = build.contents(folder, "1.0.0")
    assert plugin_id == "tv.maktaba.convert"
    assert sorted(files) == [
        "i18n/ar.json",
        "i18n/en.json",
        "manifest.toml",
        "statements.json",
        "wheels/maktaba_fp_convert-1.0.0-py3-none-any.whl",
    ]
    bundled = json.loads((ROOT / "shared" / "plugins" / "firstparty.json").read_text())
    (entry,) = [p for p in bundled["plugins"] if p["id"] == plugin_id]
    # the statements the server embeds, byte for byte in canonical form
    assert json.loads(files["statements.json"]) == entry["statements"]
    assert json.loads(files["i18n/en.json"]) == entry["strings"]["en"]


def test_the_wheel_is_reproducible_and_its_record_holds(tmp_path: Path) -> None:
    folder = ROOT / "plugins" / "first-party" / "convert"
    name, a = build.wheel(folder, "tv.maktaba.convert", "1.0.0")
    _, b = build.wheel(folder, "tv.maktaba.convert", "1.0.0")
    assert a == b
    p = tmp_path / name
    p.write_bytes(a)
    with zipfile.ZipFile(p) as z:
        names = z.namelist()
        info = "maktaba_fp_convert-1.0.0.dist-info"
        assert "maktaba_fp_convert/__init__.py" in names
        assert not [n for n in names if "__pycache__" in n or n.endswith(".pyc")]
        meta = z.read(f"{info}/METADATA").decode()
        assert "Requires-Dist: maktaba-pipeline==1.0.0" in meta
        rows = [r.split(",") for r in z.read(f"{info}/RECORD").decode().splitlines()]
        listed = {r[0] for r in rows}
        assert listed == set(names)
        for path, digest, size in rows:
            if path.endswith("RECORD"):
                assert (digest, size) == ("", "")
                continue
            data = z.read(path)
            want = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
            assert (digest, int(size)) == (f"sha256={want}", len(data))


def _packer(tmp_path: Path) -> Path:
    """The home's Go writer, built once for the test. A job that set GOPROXY=off without
    componentpack's modules cannot build it (the first-party job downloads them)."""
    if shutil.which("go") is None:
        pytest.skip("the packer is the home's Go writer")
    try:
        return Path(build.mkplugin_binary(tmp_path))
    except subprocess.CalledProcessError:
        if os.environ.get("GOPROXY") == "off":
            pytest.skip("componentpack's Go modules are not in this job's offline cache")
        raise


def test_build_writes_each_package_and_its_files_json(tmp_path: Path) -> None:
    exe = _packer(tmp_path)
    out = tmp_path / "out"
    args = ["convert", "dedup", "--version", "1.0.0", "--out", str(out), "--mkplugin", str(exe)]
    assert build.main(args) == 0
    assert sorted(p.name for p in out.iterdir()) == [
        "tv.maktaba.convert-1.0.0.files.json",
        "tv.maktaba.convert-1.0.0.mkplugin",
        "tv.maktaba.dedup-1.0.0.files.json",
        "tv.maktaba.dedup-1.0.0.mkplugin",
    ]
    fj = json.loads((out / "tv.maktaba.convert-1.0.0.files.json").read_text())
    _, files = build.contents(ROOT / "plugins" / "first-party" / "convert", "1.0.0")
    assert {f["path"]: f["sha256"] for f in fj} == {
        k: hashlib.sha256(v).hexdigest() for k, v in files.items()
    }


def test_an_unknown_plugin_name_is_refused(tmp_path: Path) -> None:
    assert build.main(["nope", "--version", "1.0.0", "--out", str(tmp_path)]) == 1
