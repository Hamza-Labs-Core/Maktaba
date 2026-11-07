"""desktop-release.yml's player legs run the Compose UI tests before packaging.

Skiko's native library (the Compose renderer) links to libGL, libEGL, libX11 and fontconfig.
GitHub's ubuntu-22.04-arm image ships none of them (the x64 image does), so v0.3.85's
linux-aarch64 player leg failed every UI test with an UnsatisfiedLinkError, and the release
had no latest-player.json. Every Linux player leg installs them before the build.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
SKIKO_LIBS = ("libgl1", "libegl1", "libx11-6", "libfontconfig1")


def player_job() -> dict[str, Any]:
    doc = yaml.safe_load((ROOT / ".github/workflows/desktop-release.yml").read_text())
    return doc["jobs"]["player"]


def test_the_player_legs_include_linux_arm() -> None:
    oses = {leg["os"] for leg in player_job()["strategy"]["matrix"]["include"]}
    assert "ubuntu-22.04-arm" in oses


def test_linux_player_legs_install_skikos_libraries_before_the_tests() -> None:
    steps = player_job()["steps"]
    names = [s.get("name", "") for s in steps]
    build = names.index("Build and check the installers")
    installs = [
        i
        for i, s in enumerate(steps)
        if s.get("if") == "runner.os == 'Linux'" and "apt-get install" in s.get("run", "")
    ]
    assert installs and min(installs) < build, "the Linux packages are installed before the build"
    run = " ".join(steps[i]["run"] for i in installs).split()
    missing = [lib for lib in SKIKO_LIBS if lib not in run]
    assert not missing, f"Skiko's libraries are not installed: {missing}"
