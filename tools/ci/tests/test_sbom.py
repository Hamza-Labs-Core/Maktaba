"""Story 23.7 AC1, TC1: the per-component SBOMs. The web SBOM holds every package the
lockfile resolves (the transitive ones included) with its version, the check fails an SBOM
that misses one, and the release attaches the SBOMs: one per shipped Go binary, the
pipeline and the web."""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "security"))

import sbom  # noqa: E402

LOCK = """lockfileVersion: '9.0'

settings:
  autoInstallPeers: true

importers:

  .:
    dependencies:
      react:
        specifier: ^18.3.1
        version: 18.3.1
      '@scope/ui':
        specifier: 1.0.0
        version: 1.0.0(react@18.3.1)
    devDependencies:
      vite:
        specifier: 5.0.0
        version: 5.0.0

packages:

  '@scope/ui@1.0.0':
    resolution: {integrity: sha512-a}

  js-tokens@4.0.0:
    resolution: {integrity: sha512-b}

  loose-envify@1.4.0:
    resolution: {integrity: sha512-c}

  react@18.3.1:
    resolution: {integrity: sha512-d}

  rollup@4.0.0:
    resolution: {integrity: sha512-e}

  vite@5.0.0:
    resolution: {integrity: sha512-f}

snapshots:

  '@scope/ui@1.0.0(react@18.3.1)':
    dependencies:
      react: 18.3.1

  js-tokens@4.0.0: {}

  loose-envify@1.4.0:
    dependencies:
      js-tokens: 4.0.0

  react@18.3.1:
    dependencies:
      loose-envify: 1.4.0

  rollup@4.0.0: {}

  vite@5.0.0:
    dependencies:
      rollup: 4.0.0
"""


def test_tc1_the_web_sbom_holds_every_transitive_package_with_its_version() -> None:
    doc = sbom.pnpm_sbom(LOCK, "web", "1.2.3")
    assert doc["bomFormat"] == "CycloneDX"
    assert doc["metadata"]["component"] == {"type": "application", "name": "maktaba-web", "version": "1.2.3"}
    comps = {c["name"]: c for c in doc["components"]}
    assert {f"{n}@{c['version']}" for n, c in comps.items()} == set(sbom.lock_package_keys(LOCK))
    # reached only through dev dependencies: in the SBOM, scoped out of the bundle
    assert comps["vite"]["scope"] == comps["rollup"]["scope"] == "excluded"
    # react's own transitive dependencies are production ones
    assert comps["js-tokens"]["scope"] == comps["loose-envify"]["scope"] == "required"
    assert comps["@scope/ui"]["purl"] == "pkg:npm/%40scope/ui@1.0.0"


def test_the_real_web_lockfile_reads_whole() -> None:
    text = (ROOT / "web" / "pnpm-lock.yaml").read_text()
    doc = sbom.pnpm_sbom(text, "web", "dev")
    keys = sbom.lock_package_keys(text)
    assert len(keys) > 100
    assert {f"{c['name']}@{c['version']}" for c in doc["components"]} == set(keys)
    assert any(c["name"] == "react" and c["scope"] == "required" for c in doc["components"])


def _run_check(tmp: Path, doc: dict, expect: str) -> subprocess.CompletedProcess[str]:
    (tmp / "s.json").write_text(json.dumps(doc))
    (tmp / "expect").write_text(expect)
    return subprocess.run(
        [sys.executable, str(ROOT / "tools/security/sbom.py"), "check", str(tmp / "s.json"), "--expect", str(tmp / "expect")],
        capture_output=True,
        text=True,
        check=False,
    )


GO_DOC = {
    "bomFormat": "CycloneDX",
    "components": [
        {"name": "github.com/BurntSushi/toml", "version": "v1.6.0"},
        {"name": "github.com/Hamza-Labs-Core/Maktaba/shared/log/go", "version": "v0.3.152-0.20261115190340-a4074b59df15"},
        {"name": "PyYAML", "version": "6.0.3"},
    ],
}


def test_the_check_passes_a_complete_sbom(tmp_path: Path) -> None:
    r = _run_check(tmp_path, GO_DOC, "github.com/BurntSushi/toml@v1.6.0\ngithub.com/Hamza-Labs-Core/Maktaba/shared/log/go@\npyyaml@6.0.3\n")
    assert r.returncode == 0, r.stderr


def test_the_check_fails_an_sbom_missing_a_dependency_or_a_version(tmp_path: Path) -> None:
    r = _run_check(tmp_path, GO_DOC, "github.com/BurntSushi/toml@v1.5.0\ngolang.org/x/net@v0.60.0\ngithub.com/Hamza-Labs-Core/Maktaba/shared/gone@\n")
    assert r.returncode == 1
    assert "missing github.com/BurntSushi/toml@v1.5.0" in r.stderr
    assert "missing golang.org/x/net@v0.60.0" in r.stderr
    assert "missing github.com/Hamza-Labs-Core/Maktaba/shared/gone" in r.stderr


def test_the_check_fails_an_empty_expectation_or_a_non_cyclonedx_file(tmp_path: Path) -> None:
    assert _run_check(tmp_path, GO_DOC, "").returncode == 1
    assert _run_check(tmp_path, {"bomFormat": "SPDX", "components": []}, "a@1\n").returncode == 1


def test_sbom_sh_covers_the_spec_components_plus_plugind_and_requires_its_tools() -> None:
    text = (ROOT / "tools" / "sbom.sh").read_text()
    for call in ("emit_go api ", "emit_go streaming ", "emit_rust plugind ", "emit_python", "emit_web"):
        assert f"\n{call}" in text
    assert "skipping" not in text  # no best effort: a missing tool fails
    assert "need cyclonedx-gomod" in text
    assert "need cargo-cyclonedx" in text
    assert "--all-extras" not in text
    assert "sbom.py\" pipeline-extras" in text


def test_cargo_expect_walks_normal_transitive_deps_and_skips_dev_and_build() -> None:
    meta = {
        "packages": [
            {"id": "p-root", "name": "maktaba-plugind", "version": "1.0.0"},
            {"id": "p-core", "name": "plugind-core", "version": "1.0.0"},
            {"id": "p-wt", "name": "wasmtime", "version": "50.0.3"},
            {"id": "p-dev", "name": "tempfile", "version": "3.0.0"},
            {"id": "p-build", "name": "cc", "version": "1.11.1"},
            {"id": "p-other", "name": "mkp", "version": "1.0.0"},
        ],
        "resolve": {
            "nodes": [
                {
                    "id": "p-root",
                    "deps": [
                        {"pkg": "p-core", "dep_kinds": [{"kind": None}]},
                        {"pkg": "p-dev", "dep_kinds": [{"kind": "dev"}]},
                    ],
                },
                {
                    "id": "p-core",
                    "deps": [
                        {"pkg": "p-wt", "dep_kinds": [{"kind": None}]},
                        {"pkg": "p-build", "dep_kinds": [{"kind": "build"}]},
                    ],
                },
                {"id": "p-wt", "deps": []},
                {"id": "p-dev", "deps": []},
                {"id": "p-build", "deps": []},
                {"id": "p-other", "deps": []},
            ]
        },
    }
    assert sbom.cargo_expect(meta, "maktaba-plugind") == ["plugind-core@1.0.0", "wasmtime@50.0.3"]


def test_pipeline_extras_excludes_dev_and_matches_shipped_extras() -> None:
    extras = sbom.pipeline_extras(ROOT / "pipeline" / "pyproject.toml")
    assert "dev" not in extras
    assert {"lang", "mediaintel", "search", "stt"} <= set(extras)


# every Go binary a release ships: the archives' and packages' (api, streaming, the server,
# the cloud relay, the launcher), the desktop apps' cloudlink and the api and streaming
# images' healthcheck
SHIPPED_GO = {
    "api": "api",
    "streaming": "streaming",
    "server": "cmd/maktaba-server",
    "cloud": "cloud",
    "cloudlink": "cloud",
    "launcher": "cmd/maktaba-launcher",
    "healthcheck": "tools/healthcheck",
}


def test_every_shipped_go_binary_has_an_sbom_and_the_release_downloads_its_modules() -> None:
    text = (ROOT / "tools" / "sbom.sh").read_text()
    for name, mod in SHIPPED_GO.items():
        assert f"\nemit_go {name} {mod} " in text, name
    # built with the tags the release sets (release.yml's STREAMING_TAGS, the server's web)
    assert "\nemit_go streaming streaming . tvstaging\n" in text
    assert "\nemit_go server cmd/maktaba-server . embed_web\n" in text
    wf = yaml.safe_load((ROOT / ".github/workflows/release.yml").read_text())
    steps = wf["jobs"]["sbom"]["steps"]
    mods = next(s["with"]["modules"].split() for s in steps if str(s.get("uses", "")).endswith("/go-mod-download"))
    assert set(SHIPPED_GO.values()) <= set(mods)
    for docker in ("api/Dockerfile", "streaming/Dockerfile"):
        assert "COPY tools/healthcheck" in (ROOT / docker).read_text(), docker


def test_sbom_sh_writes_a_relative_out_dir_from_inside_each_module() -> None:
    # release.yml passes `sbom-out`; emit_go runs cyclonedx-gomod from the module's directory,
    # where a relative -output does not exist
    text = (ROOT / "tools" / "sbom.sh").read_text()
    assert text.index('OUT=$(cd "${OUT}" && pwd)') < text.index("\nemit_go api ")


def test_the_release_attaches_the_sboms() -> None:
    wf = yaml.safe_load((ROOT / ".github/workflows/release.yml").read_text())
    jobs = wf["jobs"]
    job = jobs["sbom"]
    assert any("tools/sbom.sh" in str(s.get("run", "")) for s in job["steps"])
    assert any(str(s.get("uses", "")).startswith("dtolnay/rust-toolchain@") for s in job["steps"])
    assert any("install-rust-tool.sh cargo-cyclonedx" in str(s.get("run", "")) for s in job["steps"])
    assert any(str(s.get("uses", "")).startswith("actions/upload-artifact@") and s["with"]["name"] == "release-sbom" for s in job["steps"])
    # a re-run of a published tag builds nothing, like every other build job
    assert job["if"] == "needs.guard.outputs.published != 'true'"
    publish = jobs["sign-and-release"]
    assert "sbom" in publish["needs"]
    assert "needs.sbom.result == 'success'" in publish["if"]
    runs = "\n".join(str(s.get("run", "")) for s in publish["steps"])
    assert "staged/sbom/" in runs
    downloads = [s for s in publish["steps"] if str(s.get("uses", "")).startswith("actions/download-artifact@") and s["with"].get("name") == "release-sbom"]
    assert len(downloads) == 3  # the 3-attempt chain
