"""Story 22.3 TC3: the image size guard fails CI with the size delta when an image is over its
budget, passes when every image fits, and fails on an image that was not built.

The guard runs for real under bash with a stand-in `docker` first on PATH that answers
`docker history` with the image's layers from a table; CI's `images` job
(.github/workflows/_images.yml) runs the same script after building the four images.
"""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
GUARD = ROOT / "tools" / "image-size-guard.sh"
MIB = 1024 * 1024
BUDGET = {"api": 60, "streaming": 80, "pipeline": 1200, "web": 30}

FAKE_DOCKER = """#!/usr/bin/env bash
# docker history --no-trunc --human=false --format '{{.Size}}' IMAGE: the image's layers,
# its size from $SIZES_DIR/<image's file> split over three layers and an empty one
[ "$1" = history ] || { echo "unexpected: $*" >&2; exit 2; }
img="${@: -1}"
f="$SIZES_DIR/$(printf '%s' "$img" | tr '/:' '__')"
[ -f "$f" ] || { echo "Error: No such image: $img" >&2; exit 1; }
n=$(cat "$f")
echo $((n - 2 * (n / 3))); echo $((n / 3)); echo 0; echo $((n / 3))
"""


def run_guard(tmp_path: Path, sizes: dict[str, int], **env: str) -> subprocess.CompletedProcess[str]:
    bin_dir, sizes_dir = tmp_path / "bin", tmp_path / "sizes"
    bin_dir.mkdir()
    sizes_dir.mkdir()
    docker = bin_dir / "docker"
    docker.write_text(FAKE_DOCKER)
    docker.chmod(0o755)
    registry, version = env.get("MAKTABA_REGISTRY", "ghcr.io/maktaba"), env.get("MAKTABA_VERSION", "dev")
    for svc, size in sizes.items():
        (sizes_dir / f"{registry}/{svc}:{version}".replace("/", "_").replace(":", "_")).write_text(f"{size}\n")
    bash = shutil.which("bash")
    assert bash
    return subprocess.run(
        [bash, str(GUARD)],
        env={**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}", "SIZES_DIR": str(sizes_dir), **env},
        capture_output=True,
        text=True,
        check=False,
    )


def at_budget() -> dict[str, int]:
    return {svc: mib * MIB for svc, mib in BUDGET.items()}


def test_every_image_within_its_budget_passes(tmp_path: Path) -> None:
    sizes = at_budget()
    sizes["web"] -= 5 * MIB
    r = run_guard(tmp_path, sizes)
    assert r.returncode == 0, r.stdout + r.stderr
    lines = r.stdout.splitlines()
    assert [ln.split()[:2] for ln in lines] == [["OK", s] for s in ("api", "streaming", "pipeline", "web")]
    assert "slack=5.0 MiB" in lines[3]


@pytest.mark.parametrize("svc", sorted(BUDGET))
def test_one_mib_over_fails_with_the_delta(tmp_path: Path, svc: str) -> None:
    sizes = at_budget()
    sizes[svc] += 1 * MIB
    r = run_guard(tmp_path, sizes, GITHUB_ACTIONS="true")
    assert r.returncode == 1, r.stdout
    fail = [ln for ln in r.stdout.splitlines() if ln.startswith("FAIL")]
    assert len(fail) == 1, r.stdout
    assert fail[0].split()[1] == svc
    assert f"size={BUDGET[svc] + 1}.0 MiB" in fail[0]
    assert f"max={BUDGET[svc]}.0 MiB" in fail[0]
    assert "overshoot=1.0 MiB" in fail[0]
    # the annotation the pull request's checks page shows
    assert f"::error title=Image over its size budget::{svc} is" in r.stdout
    assert "1.0 MiB over" in r.stdout


def test_an_image_that_was_not_built_fails(tmp_path: Path) -> None:
    sizes = at_budget()
    del sizes["pipeline"]
    r = run_guard(tmp_path, sizes)
    assert r.returncode == 1
    assert "MISS pipeline" in r.stdout


def test_registry_and_version_come_from_the_environment(tmp_path: Path) -> None:
    r = run_guard(tmp_path, at_budget(), MAKTABA_REGISTRY="maktaba-ci", MAKTABA_VERSION="pr")
    assert r.returncode == 0, r.stdout + r.stderr
    assert "image=maktaba-ci/api:pr" in r.stdout


def test_the_images_job_builds_the_four_images_and_runs_the_guard() -> None:
    wf = yaml.safe_load((ROOT / ".github/workflows/_images.yml").read_text())
    job = wf["jobs"]["images"]
    script = "\n".join(str(s.get("run", "")) for s in job["steps"])
    for svc in BUDGET:
        assert f"{svc}/Dockerfile" in script or "${svc}/Dockerfile" in script
    assert "tools/image-size-guard.sh" in script
    ci = yaml.safe_load((ROOT / ".github/workflows/ci.yml").read_text())
    assert ci["jobs"]["images"]["uses"] == "./.github/workflows/_images.yml"
    assert "images" in ci["jobs"]["ci-success"]["needs"]


def test_the_images_job_measures_every_platform_the_release_pushes() -> None:
    # a budget set from one platform's image holds on the other's too: the job builds and
    # guards each platform release.yml's build-images pushes
    release = yaml.safe_load((ROOT / ".github/workflows/release.yml").read_text())
    build = next(s for s in release["jobs"]["build-images"]["steps"] if s.get("id") == "build")
    pushed = sorted(build["with"]["platforms"].split(","))
    wf = yaml.safe_load((ROOT / ".github/workflows/_images.yml").read_text())
    job = wf["jobs"]["images"]
    legs = job["strategy"]["matrix"]["include"]
    assert sorted(leg["platform"] for leg in legs) == pushed
    script = "\n".join(str(s.get("run", "")) for s in job["steps"])
    assert "--platform linux/amd64" not in script, "the build takes the leg's platform"
