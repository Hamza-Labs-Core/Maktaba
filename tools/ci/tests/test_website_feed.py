"""The release feed goes live with every maktaba.tv deploy (docs/updates.md, "How a home finds a
release"): website-deploy.yml runs tools/release/website-feed.sh before it deploys, which puts the
newest update-feed artifact of main into the site, or carries over the feed maktaba.tv serves now,
so no deploy of the site drops the feed. update-feed.yml writes, signs and verifies the feed, and
starts the deploy. The script is run here against a fake `gh` and a live feed on file://.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/website-feed.sh"


def workflow(name: str) -> dict:
    return yaml.safe_load((ROOT / ".github/workflows" / name).read_text())


def fake_gh(tmp: Path, run_id: str, artifact: Path | None) -> Path:
    """A `gh` whose artifacts listing names run_id (empty: none) and whose `run download`
    copies artifact's files (None: fails)."""
    bin_dir = tmp / "bin"
    bin_dir.mkdir()
    copy = f'cp -R "{artifact}/." "$dest"' if artifact else "exit 1"
    (bin_dir / "gh").write_text(
        "#!/usr/bin/env bash\n"
        'echo "gh $*" >>"$GH_LOG"\n'
        'if [ "$1" = api ]; then echo "' + run_id + '"; exit 0; fi\n'
        'if [ "$1 $2" = "run download" ]; then\n'
        '  while [ $# -gt 0 ]; do [ "$1" = -D ] && dest="$2"; shift; done\n'
        f"  mkdir -p \"$dest\"; {copy}\n"
        "fi\n"
    )
    (bin_dir / "gh").chmod(0o755)
    return bin_dir


def feed_dir(root: Path, platforms: list[str]) -> Path:
    for ch in ("stable", "beta"):
        d = root / ch
        d.mkdir(parents=True)
        files = [f"{p}.json" for p in platforms]
        (d / "index.json").write_text(json.dumps({"platforms": [{"platform": p, "file": f} for p, f in zip(platforms, files)]}))
        (d / "index.json.sigstore.json").write_text("{}")
        for f in files:
            (d / f).write_text(json.dumps({"from": root.name, "file": f}))
            (d / (f + ".sigstore.json")).write_text("{}")
    return root


def run(tmp: Path, run_id: str, artifact: Path | None, live: Path | None) -> subprocess.CompletedProcess[str]:
    env = dict(os.environ)
    env["PATH"] = f"{fake_gh(tmp, run_id, artifact)}{os.pathsep}{env['PATH']}"
    env["REPO"] = "o/r"
    env["GH_LOG"] = str(tmp / "gh.log")
    env["GITHUB_STEP_SUMMARY"] = str(tmp / "summary.md")
    env["GH_RETRY_DELAYS"] = "0"
    env["FEED_LIVE"] = f"file://{live}" if live else f"file://{tmp}/nothing-live"
    return subprocess.run(["bash", str(SCRIPT), str(tmp / "dist/updates/v1")], check=False, env=env, text=True, capture_output=True)


def test_the_newest_artifact_goes_live(tmp_path: Path) -> None:
    art = feed_dir(tmp_path / "artifact", ["linux-amd64", "darwin-arm64"])
    live = feed_dir(tmp_path / "live", ["linux-amd64"])
    r = run(tmp_path, "4242", art, live)
    assert r.returncode == 0, r.stderr
    out = tmp_path / "dist/updates/v1"
    assert json.loads((out / "stable/darwin-arm64.json").read_text())["from"] == "artifact"
    assert (out / "beta/linux-amd64.json.sigstore.json").exists()
    log = (tmp_path / "gh.log").read_text()
    # the newest artifact of main, not expired
    assert "actions/artifacts?name=update-feed" in log and "run download 4242" in log
    assert 'head_branch == "main"' in log and "expired | not" in log
    assert "run 4242" in (tmp_path / "summary.md").read_text()


def test_no_artifact_carries_over_the_live_feed(tmp_path: Path) -> None:
    live = feed_dir(tmp_path / "live", ["linux-amd64", "windows-amd64"])
    r = run(tmp_path, "", None, live)
    assert r.returncode == 0, r.stderr
    out = tmp_path / "dist/updates/v1"
    for ch in ("stable", "beta"):
        for f in ("index.json", "linux-amd64.json", "windows-amd64.json"):
            assert (out / ch / f).exists() and (out / ch / (f + ".sigstore.json")).exists(), f"{ch}/{f}"
    assert json.loads((out / "stable/windows-amd64.json").read_text())["from"] == "live"
    assert "carried over" in (tmp_path / "summary.md").read_text()


def test_an_artifact_that_will_not_download_carries_over_the_live_feed(tmp_path: Path) -> None:
    live = feed_dir(tmp_path / "live", ["linux-amd64"])
    r = run(tmp_path, "4242", None, live)
    assert r.returncode == 0, r.stderr
    assert json.loads((tmp_path / "dist/updates/v1/stable/linux-amd64.json").read_text())["from"] == "live"


def test_neither_deploys_without_a_feed_and_says_so(tmp_path: Path) -> None:
    r = run(tmp_path, "", None, None)
    assert r.returncode == 0, r.stderr
    assert not (tmp_path / "dist/updates/v1").exists()
    assert "deploys without a release feed" in r.stdout


def test_a_live_index_naming_a_path_is_refused(tmp_path: Path) -> None:
    live = feed_dir(tmp_path / "live", ["linux-amd64"])
    (live / "stable/index.json").write_text(json.dumps({"platforms": [{"platform": "x", "file": "../../secret"}]}))
    r = run(tmp_path, "", None, live)
    assert r.returncode != 0
    assert "bad file name" in r.stderr


def test_every_deploy_puts_the_feed_in_before_it_deploys() -> None:
    steps = workflow("website-deploy.yml")["jobs"]["deploy"]["steps"]
    names = [s.get("name", "") for s in steps]
    feed, deploy = names.index("The release feed"), names.index("Deploy")
    assert feed < deploy
    step = steps[feed]
    assert "tools/release/website-feed.sh web/packages/website/dist/updates/v1" in step["run"]
    # behind the go-live gate, as every write is; a rollback deploys a version as it was
    assert step["if"] == "steps.gate.outputs.ok == 'true' && !inputs.rollback"
    assert workflow("website-deploy.yml")["jobs"]["deploy"]["permissions"] == {"contents": "read", "actions": "read"}


def test_update_feed_signs_verifies_then_starts_the_deploy() -> None:
    wf = workflow("update-feed.yml")
    on = wf[True] if True in wf else wf["on"]
    assert on["workflow_run"]["workflows"] == ["Release", "Desktop Release"]
    job = wf["jobs"]["feed"]
    assert job["permissions"] == {"contents": "read", "id-token": "write", "actions": "write"}
    names = [s.get("name", "") for s in job["steps"]]
    order = ["Write the feed", "Sign the feed (keyless cosign)", "Verify the signed feed (releaseverify)",
             "Upload the feed", "Deploy maktaba.tv with it"]
    assert [names.index(n) for n in order] == sorted(names.index(n) for n in order)
    # the live check waits for the deploy and fails the run when the release is not listed
    live = wf["jobs"]["live"]
    assert live["needs"] == ["feed"]
    assert "release-feed live" in live["steps"][-1]["run"]
