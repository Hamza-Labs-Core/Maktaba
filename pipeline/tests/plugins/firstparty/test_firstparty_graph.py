"""Maktaba's own stages as first-party plugins: the bundle, the trusted manifest and the
graph from manifests (Epic 54, stories 54.22, 54.25 §1-§2, 54.27)."""

from __future__ import annotations

import copy
import json
import subprocess
import sys

import pytest

from maktaba_pipeline.engine.graph import GRAPH, GraphError
from maktaba_pipeline.plugins import manifest as pm
from tests.plugins.firstparty.conftest import ROOT, TOY

sys.path.insert(0, str(ROOT / "tools" / "plugins"))


def test_a_first_party_stage_is_defined_by_its_manifest(toy: bytes) -> None:
    from maktaba_pipeline.plugins import firstparty

    (p,) = firstparty.plugins()
    assert p.id == "tv.maktaba.toy" and p.component == "classify"
    s = firstparty.stage_def("fp_toy")
    assert (s.name, s.pool, s.lane, s.lane_order, s.version, s.subject_kinds, s.placement) == (
        "fp_toy",
        "cpu",
        "seasons",
        99,
        1,
        ("library",),
        "any",
    )
    assert firstparty.impl_entry("fp_toy") == "fp_toy.stage:IMPL"
    assert firstparty.runner_group("fp_toy") == "light"
    assert set(firstparty.registry("tv.maktaba.toy").by_name) == {
        "rules.read",
        "stage.input_fingerprint",
        "stage.run",
        "stage.run.2",
        "stage.run.3",
    }


def test_its_trusted_fields_are_refused_in_any_other_manifest() -> None:
    text = (TOY / "manifest.toml").read_text().replace("tv.maktaba.toy", "dev.example.toy")
    with pytest.raises(pm.ManifestInvalid) as e:
        pm.validate(text.encode())
    assert e.value.has(pm.CODE_TRUSTED_FIELD)
    # the same manifest is read in full only as a built-in one
    assert (
        pm.validate((TOY / "manifest.toml").read_bytes(), trusted=True)["stage"][0]["pool"] == "cpu"
    )


def test_a_third_party_stage_named_like_a_core_stage_is_refused() -> None:
    corpus = (
        ROOT
        / "shared"
        / "plugins"
        / "testdata"
        / "manifests"
        / "invalid"
        / "trusted_reserved_stage.toml"
    )
    with pytest.raises(pm.ManifestInvalid) as e:
        pm.validate(corpus.read_bytes())
    assert e.value.has(pm.CODE_TRUSTED_FIELD)
    assert "transcribe" in pm.reserved_stages() and "promo_links" in pm.reserved_stages()


def test_the_assembled_graph_refuses_a_stage_defined_twice(toy: bytes) -> None:
    from dataclasses import replace

    from maktaba_pipeline.engine.assemble import check
    from maktaba_pipeline.plugins import firstparty

    fp = firstparty.stage_def("fp_toy")
    check([*GRAPH, fp], [fp])
    with pytest.raises(GraphError, match="does not name it"):
        check(GRAPH, [fp])
    with pytest.raises(GraphError, match="use _fp"):
        check([*GRAPH, replace(fp, pool="gpu")], [fp])


def test_the_graph_equals_the_golden_graph() -> None:
    import graph_golden

    golden = json.loads(graph_golden.GOLDEN.read_text(encoding="utf-8"))
    assert graph_golden.diff(golden, graph_golden.export()) == []


def test_the_golden_check_names_a_changed_field() -> None:
    import graph_golden

    golden = json.loads(graph_golden.GOLDEN.read_text(encoding="utf-8"))
    changed = copy.deepcopy(golden)
    changed["stages"]["promo_links"]["pool"] = "gpu"
    assert graph_golden.diff(golden, changed) == ['stage promo_links.pool: golden "cpu" != "gpu"']


def test_the_bundle_and_its_copy_are_current() -> None:
    out = subprocess.run(
        [sys.executable, str(ROOT / "tools" / "plugins" / "bundle.py")],
        capture_output=True,
        text=True,
    )
    assert out.returncode == 0, out.stderr
    a = (ROOT / "shared" / "plugins" / "firstparty.json").read_bytes()
    b = (
        ROOT / "pipeline" / "src" / "maktaba_pipeline" / "plugins" / "data" / "firstparty.json"
    ).read_bytes()
    assert a == b


def test_every_first_party_plugin_folder_is_in_the_wheel() -> None:
    sys.path.insert(0, str(ROOT / "pipeline"))
    import hatch_build

    dirs = hatch_build.package_dirs(ROOT / "pipeline")
    assert any(v == "maktaba_plugin_sdk" for v in dirs.values())
    first = ROOT / "plugins" / "first-party"
    for p in sorted(first.iterdir()) if first.is_dir() else []:
        for pkg in (p / "src").glob("*/__init__.py"):
            assert pkg.parent.name in dirs.values(), pkg


def test_the_roles_of_a_plugin_grant_only_its_tables() -> None:
    import tomllib

    import roles

    sql = roles.role_sql(tomllib.loads((TOY / "manifest.toml").read_text()))
    assert "CREATE ROLE maktaba_fp_toy NOLOGIN" in sql
    assert "GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO maktaba_fp_toy', t);" in sql
    assert "ARRAY['fp_toy_out']" in sql and "ARRAY['app_settings', 'videos']" in sql
    assert "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM maktaba_fp_toy" in sql


def test_parity_toml_refuses_unknown_keys() -> None:
    from maktaba_plugin_sdk.parity import ParityConfig, ParityError

    cfg = ParityConfig.parse(
        'tables = ["a"]\n[ignore]\na = ["updated_at"]\n[[cases]]\nname = "x"\n'
    )
    assert cfg.tables == ("a",) and cfg.ignore == {"a": ("updated_at",)}
    with pytest.raises(ParityError):
        ParityConfig.parse("tablez = []\n")
    with pytest.raises(ParityError):
        ParityConfig.parse("[[cases]]\nsetup = 1\n")


def test_the_statements_of_the_toy_are_what_its_code_runs() -> None:
    pytest.importorskip("pglast")
    out = subprocess.run(
        [
            sys.executable,
            str(ROOT / "tools" / "plugins" / "statements_build.py"),
            "check",
            "--dir",
            str(TOY),
        ],
        capture_output=True,
        text=True,
        env={**__import__("os").environ, "PYTHONPATH": str(TOY / "src")},
    )
    assert out.returncode == 0, out.stderr
