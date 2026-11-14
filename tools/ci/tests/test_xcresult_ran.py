"""tools/ci/xcresult_ran.py: an Apple UI job fails when its result bundle ran none, or not all,
of the tests it selected.

The fixtures in fixtures/xcresult are `xcresulttool get test-results tests` output:
- normal.json: ipad 5/6 of CI run 43613104921, 28 tests, all passed (normal.selection is its
  `-only-testing` list, as the job writes it);
- incident.json: ipad 6/6 of run 43605618404, attempt 1 (#1312): `-test-iterations 20`, the
  runner restarted after one iteration and ran nothing more, the job went green;
- loop.json: the same shard's rerun, 20 repetitions;
- mac.json: the Mac Player job of run 43613104921 (a unit and a UI bundle, Swift Testing
  parameterised tests);
- zero.json: normal.json with its bundle's suites removed (a runner that ran nothing);
- missing.json: normal.json without PlayerHomeUITests/testEmptyRowIsAbsent.

Every UI job in .github/workflows/_apple-apps.yml writes its selection and runs the check.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/xcresult_ran.py"
FIXTURES = Path(__file__).resolve().parent / "fixtures/xcresult"
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/_apple-apps.yml").read_text(encoding="utf-8"))
UI_JOBS = ("player-ui-tests", "tv-ui-tests", "player-ui-mac", "remote-ui-tests")
LOOPED = [
    "-only-testing",
    "Maktaba Player iPad UITests/PlayerProfileUITests/testPlaybackSettingsAreKept",
    "-test-iterations",
    "20",
]

sys.path.insert(0, str(SCRIPT.parent))
import xcresult_ran


def tree(name: str) -> dict[str, Any]:
    data: dict[str, Any] = json.loads((FIXTURES / name).read_text(encoding="utf-8"))
    return data


def normal_selection() -> xcresult_ran.Selection:
    return xcresult_ran.parse_selection((FIXTURES / "normal.selection").read_text().splitlines())


def run(
    tmp_path: Path, fixture: str, selection: list[str], label: str = "ipad 5/6"
) -> subprocess.CompletedProcess[str]:
    sel = tmp_path / "x.selection"
    sel.write_text("".join(a + "\n" for a in selection), encoding="utf-8")
    return subprocess.run(
        [sys.executable, str(SCRIPT), label, "--tests-json", str(FIXTURES / fixture), str(sel)],
        capture_output=True,
        text=True,
        check=False,
    )


def test_a_normal_shard_passes_and_prints_its_count() -> None:
    lines, errors = xcresult_ran.check("ipad 5/6", tree("normal.json"), normal_selection())
    assert errors == []
    assert lines == [
        (
            "ipad 5/6: 28 tests ran of 28 in the results (28 passed, 0 failed, 0 expected failures, "
            "0 skipped), 28 runs; 28 selected"
        )
    ]


def test_zero_tests_run_fails() -> None:
    _, errors = xcresult_ran.check("ipad 5/6", tree("zero.json"), normal_selection())
    assert errors[0].startswith("ipad 5/6: no test ran")
    assert len(errors) == 1 + 28  # and each selected test is missing


def test_a_missing_test_fails_and_is_named() -> None:
    _, errors = xcresult_ran.check("ipad 5/6", tree("missing.json"), normal_selection())
    assert errors == [
        (
            "ipad 5/6: Maktaba Player iPad UITests/PlayerHomeUITests/testEmptyRowIsAbsent was selected "
            "but is not in the results (never ran)"
        )
    ]


def test_the_incident_fails_on_its_dropped_iterations() -> None:
    sel = xcresult_ran.parse_selection(LOOPED)
    lines, errors = xcresult_ran.check("ipad 6/6", tree("incident.json"), sel)
    assert "1 tests ran of 1" in lines[0] and "1 expected failures" in lines[0] and "1 runs" in lines[0]
    assert errors == [
        (
            "ipad 6/6: Maktaba Player iPad UITests/PlayerProfileUITests/testPlaybackSettingsAreKept ran "
            "1 of 20 iterations (the test runner restarted and dropped the rest)"
        )
    ]


def test_a_full_loop_passes() -> None:
    lines, errors = xcresult_ran.check("ipad 6/6", tree("loop.json"), xcresult_ran.parse_selection(LOOPED))
    assert errors == []
    assert "20 runs; 1 selected, 20 iterations each" in lines[0]


def test_the_mac_jobs_class_and_bundle_selections_pass() -> None:
    classes = [
        "PlayerMenus",
        "MacPlayerSurface",
        "PlayerLaunchLog",
        "PageLocale",
        "SeriesScreenDetails",
        "PageSnapshots",
        "PlaybackUnderALimit",
        "HomeCardActions",
    ]
    args: list[str] = []
    for c in classes:
        args += ["-only-testing", f"Maktaba Player Mac Tests/{c}"]
    args += ["-only-testing", "Maktaba Player Mac UITests"]
    lines, errors = xcresult_ran.check("mac", tree("mac.json"), xcresult_ran.parse_selection(args))
    assert errors == []
    # a parameterised case's Arguments are one run, not repetitions
    assert "38 tests ran of 38" in lines[0] and "38 runs" in lines[0]


def test_a_class_or_bundle_not_in_the_results_fails() -> None:
    sel = xcresult_ran.parse_selection(
        ["-only-testing", "Maktaba Player Mac Tests/NoSuchClass", "-only-testing", "Maktaba Player Mac NoBundle"]
    )
    _, errors = xcresult_ran.check("mac", tree("mac.json"), sel)
    assert [e.split(" was ")[0] for e in errors] == [
        "mac: Maktaba Player Mac Tests/NoSuchClass",
        "mac: Maktaba Player Mac NoBundle",
    ]


def test_a_class_prefix_is_not_a_match() -> None:
    # PlayerHome must not match PlayerHomeUITests
    sel = xcresult_ran.parse_selection(["-only-testing", "Maktaba Player iPad UITests/PlayerHome"])
    _, errors = xcresult_ran.check("ipad 5/6", tree("normal.json"), sel)
    assert len(errors) == 1


@pytest.mark.parametrize("args", [[], ["-only-testing"], ["-skip-testing", "X"]])
def test_a_selection_it_cannot_read_is_an_error(args: list[str]) -> None:
    with pytest.raises(ValueError):
        xcresult_ran.parse_selection(args)


def test_the_command_exits_and_annotates(tmp_path: Path) -> None:
    ok = run(tmp_path, "normal.json", (FIXTURES / "normal.selection").read_text().splitlines())
    assert ok.returncode == 0, ok.stdout + ok.stderr
    assert ok.stdout.startswith("ipad 5/6: 28 tests ran")
    bad = run(tmp_path, "incident.json", LOOPED, label="ipad 6/6")
    assert bad.returncode == 1
    assert (
        "::error::ipad 6/6: Maktaba Player iPad UITests/PlayerProfileUITests/testPlaybackSettingsAreKept ran 1 of 20"
        in bad.stdout
    )


def test_an_unreadable_bundle_fails(tmp_path: Path) -> None:
    sel = tmp_path / "x.selection"
    sel.write_text("-only-testing\nT/C\n", encoding="utf-8")
    out = subprocess.run(
        [sys.executable, str(SCRIPT), "tv", "--tests-json", str(tmp_path / "absent.json"), str(sel)],
        capture_output=True,
        text=True,
        check=False,
    )
    assert out.returncode == 1
    assert out.stdout.startswith("::error::tv: the result bundle")


@pytest.mark.parametrize("job", UI_JOBS)
def test_every_ui_job_writes_its_selection_and_checks_its_bundle(job: str) -> None:
    steps: list[dict[str, Any]] = WORKFLOW["jobs"][job]["steps"]
    names = [s.get("name") for s in steps]
    check = names.index("Check every selected test ran")
    runs = [i for i, s in enumerate(steps) if "xcodebuild test" in (s.get("run") or "")]
    assert len(runs) == 1 and runs[0] < check, f"{job}: the check comes after its one test run"
    run_script: str = steps[runs[0]]["run"]
    cmd: str = steps[check]["run"]
    assert "if" not in steps[check], f"{job}: the check runs when the tests passed"
    bundle = cmd.split('"')[3]
    selection = cmd.split('"')[5]
    assert cmd.startswith("python3 tools/ci/xcresult_ran.py ")
    assert bundle.removesuffix(".xcresult") == selection.removesuffix(".selection")
    # the run writes that selection from the very arguments it hands xcodebuild
    written = selection.replace("${{ matrix.device }}-${{ matrix.shard }}", "$device-$shard")
    assert f'printf \'%s\\n\' "${{only[@]}}" > "{written}"' in run_script
    assert '"${only[@]}"' in run_script.split("xcodebuild test", 1)[1]
    assert bundle.replace("${{ matrix.device }}-${{ matrix.shard }}", "$device-$shard") in run_script
