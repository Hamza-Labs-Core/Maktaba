"""The TrueNAS app (Epic 59 story 59.10, tools/release/truenas_app.py): the source is whole, a
release's copy pins its image everywhere, the catalog version grows with every release, and the
app keeps the shape the story asks for (568, host network, the self-update on, no D-Bus)."""

from __future__ import annotations

import shutil
import sys
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "release"))

import truenas_app as ta  # noqa: E402

DIGEST = "sha256:" + "ab" * 32


def test_the_source_is_whole() -> None:
    assert ta.check() == []


def test_catalog_version_grows_with_maktaba() -> None:
    assert ta.catalog_version("v0.3.121") == "1.3.121"
    assert ta.catalog_version("v1.0.0") == "2.0.0"
    order = ["v0.3.121", "v0.3.122", "v0.4.0", "v1.0.0"]
    versions = [tuple(int(p) for p in ta.catalog_version(t).split(".")) for t in order]
    assert versions == sorted(versions)
    with pytest.raises(ta.Broken):
        ta.catalog_version("0.3.121")
    with pytest.raises(ta.Broken):
        ta.image_ref("v0.3.1", "sha256:nothex")


def test_render_pins_the_release_everywhere(tmp_path: Path) -> None:
    dst = ta.render("v0.3.130", DIGEST, tmp_path)
    ix = yaml.safe_load((dst / "ix_values.yaml").read_text(encoding="utf-8"))
    assert ix["images"]["image"] == {"repository": ta.IMAGE, "tag": f"v0.3.130@{DIGEST}"}
    # the catalog's helper image is untouched
    assert ix["images"]["container_utils_image"]["repository"] == "ghcr.io/truenas/containers/container-utils"
    app = yaml.safe_load((dst / "app.yaml").read_text(encoding="utf-8"))
    assert (str(app["app_version"]), str(app["version"])) == ("0.3.130", "1.3.130")
    custom = yaml.safe_load((tmp_path / "custom-app.yaml").read_text(encoding="utf-8"))
    assert custom["services"]["maktaba"]["image"] == f"{ta.IMAGE}:v0.3.130@{DIGEST}"
    # the copy keeps everything else, and the source is unchanged
    assert (dst / "questions.yaml").read_bytes() == (ta.APP / "questions.yaml").read_bytes()
    assert ta.check() == []
    # without a digest: the tag alone
    ta.render("v0.3.131", "", tmp_path)
    ix = yaml.safe_load((dst / "ix_values.yaml").read_text(encoding="utf-8"))
    assert ix["images"]["image"]["tag"] == "v0.3.131"


def test_check_names_a_drift(tmp_path: Path) -> None:
    app = tmp_path / "maktaba"
    shutil.copytree(ta.APP, app)
    custom = tmp_path / "custom-app.yaml"
    custom.write_text(ta.CUSTOM_APP.read_text(encoding="utf-8").replace(":v0.", ":v9."), encoding="utf-8")
    errs = ta.check(app, custom)
    assert any("custom-app.yaml's image" in e for e in errs), errs
    p = app / "app.yaml"
    p.write_text(p.read_text(encoding="utf-8").replace("version: 1.", "version: 7."), encoding="utf-8")
    assert any("app.yaml's version" in e for e in ta.check(app, custom))
    c = app / "templates" / "docker-compose.yaml"
    c.write_text(c.read_text(encoding="utf-8") + '\n{# add_env("MAKTABA_UPDATE_DISABLE", "1") #}\n', encoding="utf-8")
    assert any("MAKTABA_UPDATE_DISABLE" in e for e in ta.check(app, custom))


def test_the_apps_shape() -> None:
    """What story 59.10 asks of the app: 568, host network by default, 8 GB, the data on a host
    path (an SSD dataset with the Apps preset), MAKTABA_PLATFORM, no D-Bus, no ARC mask."""
    q = yaml.safe_load((ta.APP / "questions.yaml").read_text(encoding="utf-8"))
    by = {x["variable"]: x for x in q["questions"]}
    attrs = lambda v: {a["variable"]: a for a in by[v]["schema"]["attrs"]}  # noqa: E731
    run_as = attrs("run_as")
    assert run_as["user"]["schema"]["default"] == 568 and run_as["group"]["schema"]["default"] == 568
    assert attrs("network")["host_network"]["schema"]["default"] is True
    assert attrs("network")["web_port"]["schema"]["attrs"][1]["schema"]["default"] == 8088
    storage = attrs("storage")
    data_type = storage["data"]["schema"]["attrs"][0]["schema"]
    assert data_type["default"] == "host_path"
    assert {e["value"] for e in data_type["enum"]} == {"host_path", "ix_volume"}
    media = storage["media"]["schema"]["items"][0]["schema"]["attrs"]
    assert [a["variable"] for a in media] == ["path", "read_only"]
    limits = {a["variable"]: a for a in attrs("resources")["limits"]["schema"]["attrs"]}
    assert limits["memory"]["schema"]["default"] == 8192
    assert attrs("resources")["gpus"]["schema"]["$ref"] == ["definitions/gpu_configuration"]

    tpl = (ta.APP / "templates" / "docker-compose.yaml").read_text(encoding="utf-8")
    assert 'add_env("MAKTABA_PLATFORM", "truenas")' in tpl
    assert "set_grace_period(90)" in tpl
    for absent in ("add_dbus", "MAKTABA_UPDATE_DISABLE", "arcstats", "set_privileged"):
        assert absent not in tpl, absent

    app = yaml.safe_load((ta.APP / "app.yaml").read_text(encoding="utf-8"))
    assert app["name"] == "maktaba" and app["train"] == "community"
    assert app["annotations"]["min_scale_version"].startswith("25.04")
    assert app["run_as_context"][0]["uid"] == 568

    custom = yaml.safe_load(ta.CUSTOM_APP.read_text(encoding="utf-8"))["services"]["maktaba"]
    assert custom["user"] == "568:568" and custom["network_mode"] == "host"
    assert custom["environment"]["MAKTABA_PLATFORM"] == "truenas"
    assert custom["stop_grace_period"] == "90s"
    assert custom["healthcheck"]["test"][-1] == "healthcheck"
