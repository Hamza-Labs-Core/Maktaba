"""Story 22.3 TC3: the image size guard fails CI with the size delta when an image is over its
budget, passes when every image fits, and fails on an image that was not built.

The guard runs for real under bash with a stand-in `docker` first on PATH that answers
`docker history` with the image's layers from a table; CI's `images` job
(.github/workflows/_images.yml) runs the same script after building the four images.
"""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "ci"))
import ci_paths  # noqa: E402
GUARD = ROOT / "tools" / "image-size-guard.sh"
MIB = 1024 * 1024
BUDGET = {"api": 60, "streaming": 80, "pipeline": 1200, "web": 88}

FAKE_DOCKER = """#!/usr/bin/env bash
# docker history --no-trunc --human=false --format '{{.Size}}' IMAGE: the image's layers,
# its size from $SIZES_DIR/<image's file> split over three layers and an empty one
[ "$1" = history ] || { echo "unexpected: $*" >&2; exit 2; }
img="${@: -1}"
f="$SIZES_DIR/$(printf '%s' "$img" | tr '/:' '__')"
[ -f "$f" ] || { echo "Error: No such image: $img" >&2; exit 1; }
n=$(cat "$f")
case "${FAKE_HISTORY:-bytes}" in
  # a docker that ignores --human=false: the layers in its human units
  human) echo "$((n / 1000000))MB"; echo 0B; exit 0 ;;
  # a docker that fails after a layer
  partial) echo $((n / 3)); exit 1 ;;
esac
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
    loop = re.search(r"for svc in ([a-z ]+); do", script)
    assert loop, script
    assert sorted(loop.group(1).split()) == sorted(BUDGET)
    assert '-f "${svc}/Dockerfile"' in script
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


@pytest.mark.parametrize("mode", ["human", "partial"])
def test_unreadable_layer_sizes_fail_rather_than_under_count(tmp_path: Path, mode: str) -> None:
    # "145MB" would add as 145 bytes, and a docker that failed partway would leave a partial
    # sum: either would pass an image far over its budget, so either fails the guard instead
    sizes = at_budget()
    sizes["web"] += 50 * MIB
    r = run_guard(tmp_path, sizes, FAKE_HISTORY=mode)
    assert r.returncode == 1, r.stdout
    assert "MISS web" in r.stdout
    assert not any(ln.startswith("OK") for ln in r.stdout.splitlines()), r.stdout


def _copy_sources(dockerfile: Path) -> list[str]:
    """The repository paths a Dockerfile COPYs or ADDs from its build context (the repo root)."""
    out = []
    # an instruction may run on over `\` continuations, and Dockerfile keywords take any case
    text = re.sub(r"\\\n", " ", dockerfile.read_text())
    for line in text.splitlines():
        words = line.split()
        if not words or words[0].upper() not in ("COPY", "ADD"):
            continue
        args = [w for w in words[1:] if not w.startswith("--")]
        if any(w.startswith("--from") for w in words[1:]):
            continue
        out += args[:-1]
    return out


@pytest.mark.parametrize("svc", sorted(BUDGET))
def test_a_change_to_anything_an_image_copies_runs_the_images_job(svc: str) -> None:
    # a budget measures the whole image, so the pull request that grows any input of it must
    # run the gate (docs/trail: "a job measuring a whole-product budget needs every input of
    # that budget in its filter"), not only main after it merges
    sources = _copy_sources(ROOT / svc / "Dockerfile")
    assert sources
    filters = ci_paths.job_filters(ci_paths.load_filters())
    missed = []
    for src in sources:
        path = ROOT / src
        # a file inside the source: the source itself if it is a file, else any tracked file under it
        probe = src if path.is_file() else next(
            (str(f.relative_to(ROOT)) for f in sorted(path.rglob("*")) if f.is_file()), src + "/x"
        )
        if "images" not in ci_paths.matched(filters, [probe]):
            missed.append(f"{src} ({probe})")
    assert not missed, f"{svc}/Dockerfile copies from paths the images filter misses: {missed}"
    assert "images" in ci_paths.matched(filters, [f"{svc}/Dockerfile"])


def test_the_copy_parser_reads_continued_and_lowercase_instructions(tmp_path: Path) -> None:
    df = tmp_path / "Dockerfile"
    df.write_text(
        "FROM scratch\n"
        "COPY --chown=1:1 a/one.json \\\n    b/two.bin /dst/\n"
        "copy c /dst/c\n"
        "COPY --from=build /out /out\n"
        "ADD d.tar /\n"
    )
    assert _copy_sources(df) == ["a/one.json", "b/two.bin", "c", "d.tar"]
