"""Every Player UI job keeps the app's whole log: info and debug lines, not errors alone.

The iPhone, iPad and Mac jobs of .github/workflows/_apple-apps.yml each save the app's
unified log next to their result bundle. The system keeps a subsystem's info and debug lines
only while `log config` asks it to, for that exact subsystem (the app's bundle id: a parent's
config is not inherited), so each job configures its app's bundle id at persist:debug, reads
the log once the tests are done (`log show`, not a live stream: that slowed the simulator
until accessibility audits timed out) and fails when tools/apple/check-app-log.py finds no
launch info and debug line (MaktabaKit's `Log.launched`) in it.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/_apple-apps.yml").read_text(encoding="utf-8"))
PROJECT = yaml.safe_load((ROOT / "apps/apple/project.yml").read_text(encoding="utf-8"))
CHECK = ROOT / "tools/apple/check-app-log.py"
PERSIST = '--mode "level:debug,persist:debug"'


def _steps() -> list[dict[str, Any]]:
    steps: list[dict[str, Any]] = WORKFLOW["jobs"]["player-ui-tests"]["steps"]
    return steps


def _run(name: str) -> str:
    script: str = next(s for s in _steps() if s.get("name") == name)["run"]
    return script


def _bundle(target: str) -> str:
    bundle: str = PROJECT["targets"][target]["settings"]["base"]["PRODUCT_BUNDLE_IDENTIFIER"]
    return bundle


def test_check_self_test_passes() -> None:
    out = subprocess.run([sys.executable, str(CHECK), "--self-test"], capture_output=True, text=True, check=False)
    assert out.returncode == 0, out.stderr
    assert "self-test passed" in out.stdout


def test_an_error_only_log_fails(tmp_path: Path) -> None:
    log = tmp_path / "app.log"
    log.write_text(
        "Timestamp               Ty Process[PID:TID]\n"
        "2026-11-02 14:35:47.001 E  Maktaba Player[812:1a2b] [com.hamzalabs.maktaba.player:ui] home failed\n",
        encoding="utf-8",
    )
    out = subprocess.run([sys.executable, str(CHECK), str(log)], capture_output=True, text=True, check=False)
    assert out.returncode == 1
    assert "info" in out.stderr and "debug" in out.stderr


def test_a_log_with_no_line_of_the_app_says_it_never_ran(tmp_path: Path) -> None:
    """A failed build leaves an empty log: that is not blamed on lost info/debug lines."""
    log = tmp_path / "app.log"
    log.write_text("Timestamp               Ty Process[PID:TID]\n", encoding="utf-8")
    out = subprocess.run([sys.executable, str(CHECK), str(log)], capture_output=True, text=True, check=False)
    assert out.returncode == 1
    assert "never ran" in out.stderr and "not whole" not in out.stderr


def test_the_app_writes_the_launch_lines() -> None:
    log = (ROOT / "apps/apple/MaktabaKit/Sources/MaktabaKit/Log.swift").read_text(encoding="utf-8")
    assert 'session.info("\\(lines.info' in log and '"launched \\(subsystem) ' in log
    assert 'session.debug("\\(lines.debug' in log and '"launch arguments: ' in log
    for app in ("apps/apple/Player/PlayerApp.swift", "apps/apple/Remote/RemoteApp.swift"):
        assert "Log.launched()" in (ROOT / app).read_text(encoding="utf-8"), app


def test_iphone_and_ipad_keep_info_and_debug() -> None:
    script = _run("Run the Player UI tests")
    assert f'log config --subsystem "$4" {PERSIST}' in script
    # the bundle id each device's run configures is its app's own
    assert f'"iphone" "{_bundle("Maktaba Player")}"' in script
    assert f'"ipad" "{_bundle("Maktaba Player iPad")}"' in script
    assert "log show --style compact --info --debug" in script
    assert "log stream" not in script
    assert 'check-app-log.py "$log" || status=1' in script


def test_mac_keeps_info_and_debug() -> None:
    script = _run("Run the Mac Player tests")
    assert f"log config --subsystem {_bundle('Maktaba Player Mac')} {PERSIST}" in script
    assert 'log show --start "$start" --style compact --info --debug' in script
    assert "log stream" not in script
    assert 'check-app-log.py "$RESULTS/mac-app.log" || status=1' in script
    # the hosted test that reads the launch line from the app's own log runs here
    assert '-only-testing "Maktaba Player Mac Tests/PlayerLaunchLog"' in script


def test_the_logs_are_uploaded() -> None:
    upload = next(s for s in _steps() if s.get("uses", "").startswith("actions/upload-artifact") and s.get("if") == "always()")
    assert "build/player-results" in upload["with"]["path"]
