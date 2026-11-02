"""Which CI jobs a pull request runs (tools/ci/ci_paths.py over the real ci.yml and
.github/ci-paths.yml), and that the guard catches a job that could silently never run.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import ci_paths  # noqa: E402

# the jobs every pull request runs, whatever it changes
ALWAYS = {"changes", "lint", "pr-body-check", "ci-success"}
LINT_PARTS = ("go", "pipeline", "web")

GO_SERVER = {"unit", "integration", "replica", "e2e", "media-e2e", "perf-ci", "build-artifacts"}


def run(*files: str, **kw: object) -> dict[str, ci_paths.Run | None]:
    return ci_paths.which(list(files), **kw)  # type: ignore[arg-type]


def ran(runs: dict[str, ci_paths.Run | None]) -> set[str]:
    return {j for j, r in runs.items() if r is not None}


def parts(runs: dict[str, ci_paths.Run | None], job: str) -> set[str]:
    r = runs[job]
    assert r is not None, f"{job} did not run"
    return {k for k, v in r.inputs.items() if v is True}


CASES: dict[str, tuple[list[str], set[str], dict[str, set[str]]]] = {
    # name: (changed files, the jobs besides ALWAYS, {job: its inputs that are true})
    "android only": (
        ["apps/android/remote/src/main/res/values/strings.xml", "apps/android/player/build.gradle.kts"],
        {"android-apps"},
        {"lint": set()},
    ),
    "android core (the TV app and the desktop Player build on it)": (
        ["apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/net/Http.kt"],
        {"android-apps", "android-tv"},
        {"lint": set()},
    ),
    "android tv only": (
        ["apps/tv/android/app/src/main/AndroidManifest.xml"],
        {"android-tv"},
        {"lint": set()},
    ),
    "remote only (apple)": (
        ["apps/apple/Remote/InfoPlist.xcstrings", "apps/apple/Remote/RemoteApp.swift"],
        {"apple-apps"},
        {"apple-apps": {"build"}, "lint": set()},
    ),
    "apple tv only": (
        ["apps/tv/tvos/MaktabaTV/ContentView.swift"],
        {"apple-apps"},
        {"apple-apps": {"build"}},
    ),
    "player (apple)": (
        ["apps/apple/Player/Watch/WatchLogic.swift"],
        {"apple-apps"},
        {"apple-apps": {"build", "player_ui"}},
    ),
    "player ui tests (apple)": (
        ["apps/apple/UITests/PlayerShellTests.swift"],
        {"apple-apps"},
        {"apple-apps": {"build", "player_ui"}},
    ),
    "maktabakit sources (apple)": (
        ["apps/apple/MaktabaKit/Sources/MaktabaKit/HomeClient.swift"],
        {"apple-apps"},
        {"apple-apps": {"build", "player_ui"}},
    ),
    "maktabakit's browse fixtures are the api's test data too": (
        ["apps/apple/MaktabaKit/Tests/Fixtures/Browse/hub.json"],
        {"apple-apps"} | GO_SERVER - {"e2e", "perf-ci", "build-artifacts", "replica", "media-e2e"},
        {"apple-apps": {"build"}, "lint": {"go"}, "unit": {"go"}},
    ),
    "the project file alone: the scope script decides (not the Player's)": (
        ["apps/apple/Maktaba.xcodeproj/project.pbxproj", "apps/apple/project.yml"],
        {"apple-apps"},
        {"apple-apps": {"build"}},
    ),
    "pipeline only": (
        ["pipeline/src/maktaba_pipeline/scanner/walk.py"],
        {"unit", "integration", "e2e", "node-packaging"},
        {
            "lint": {"pipeline"},
            "unit": {"pipeline"},
            "integration": {"integration"},
            "node-packaging": {"packaging", "release_venv"},
        },
    ),
    "api only": (
        ["api/internal/handlers/browse/browse.go"],
        GO_SERVER,
        {"lint": {"go"}, "unit": {"go"}, "integration": {"integration", "compat_prev"}},
    ),
    "server only": (
        ["cmd/maktaba-server/internal/supervisor/supervisor.go"],
        {"unit", "integration", "replica", "launcher", "node-packaging"},
        {"lint": {"go"}, "unit": {"go"}, "integration": {"integration"}, "node-packaging": {"release_venv"}},
    ),
    "web only": (
        ["web/src/pages/Home.tsx"],
        {"unit", "e2e", "build-artifacts"},
        {"lint": {"web"}, "unit": {"web"}},
    ),
    "design tokens": (
        ["web/design-system/tokens/color.json"],
        {"unit", "e2e", "build-artifacts"},
        {"lint": {"web"}, "unit": {"web", "tokens"}},
    ),
    "desktop shell": (
        ["apps/desktop/src-tauri/src/main.rs"],
        {"unit", "mac-app", "win-app", "linux-app"},
        {"unit": {"desktop_shell"}, "lint": set()},
    ),
    "docs only": (["docs/claude-trail.md", "README.md", "specs/epics/40-x/story.md"], set(), {"lint": set()}),
    "a doc the pipeline's tests read": (
        ["docs/updates.md"],
        {"unit", "integration"},
        {"lint": {"pipeline"}, "unit": {"pipeline"}},
    ),
    "a reusable workflow: only its job": (
        [".github/workflows/_apple-apps.yml"],
        {"apple-apps"},
        {"apple-apps": {"build", "player_ui"}, "lint": set()},
    ),
    "the android workflow": ([".github/workflows/_android-apps.yml"], {"android-apps"}, {}),
    "the filters file: the lint job's guard and the pipeline test that reads it": (
        [".github/ci-paths.yml"],
        {"unit", "integration"},
        {"lint": {"pipeline"}, "unit": {"pipeline"}},
    ),
    "shared migrations": (
        ["shared/db/migrations/0200_example.sql"],
        GO_SERVER,
        {
            "lint": {"go", "pipeline"},
            "unit": {"go", "pipeline"},
            "integration": {"integration", "compat_prev"},
        },
    ),
}


@pytest.mark.parametrize("name", list(CASES))
def test_which_jobs_a_change_runs(name: str) -> None:
    files, jobs, want_parts = CASES[name]
    runs = run(*files)
    assert ran(runs) == ALWAYS | jobs, f"{name}: ran {sorted(ran(runs) - ALWAYS)}, want {sorted(jobs)}"
    for job, want in want_parts.items():
        assert parts(runs, job) == want, f"{name}: {job} got {sorted(parts(runs, job))}, want {sorted(want)}"


def test_the_project_file_with_a_player_change_runs_the_player_ui_tests() -> None:
    runs = run("apps/apple/Maktaba.xcodeproj/project.pbxproj", apple_scope=True)
    assert parts(runs, "apple-apps") == {"build", "player_ui"}


def test_ci_yml_runs_every_gate() -> None:
    runs = run(".github/workflows/ci.yml")
    gates = set(ci_paths.load_workflow().jobs) - {"linux-app-downloads", "fork-rerun-comment"}
    assert ran(runs) == gates
    for job in ("lint", "unit", "integration", "node-packaging", "apple-apps"):
        assert all(v is True for v in runs[job].inputs.values()), job  # type: ignore[union-attr]


def test_a_push_runs_everything() -> None:
    runs = run(event="push")
    assert ran(runs) == set(ci_paths.load_workflow().jobs) - {"pr-body-check", "fork-rerun-comment"}
    for job in ("lint", "unit", "integration", "node-packaging", "apple-apps"):
        assert all(v is True for v in runs[job].inputs.values()), job  # type: ignore[union-attr]


def test_a_fork_skips_e2e_and_perf_and_is_told() -> None:
    runs = run("api/main.go", fork=True)
    assert runs["e2e"] is None and runs["perf-ci"] is None
    assert runs["fork-rerun-comment"] is not None
    assert runs["integration"] is not None


def test_e2e_runs_when_integration_is_not_needed() -> None:
    # a web change: integration's filter is false, e2e's true; e2e needs integration
    runs = run("web/src/pages/Home.tsx")
    assert runs["integration"] is None and runs["e2e"] is not None


# ---- the guard ------------------------------------------------------------------------


def test_the_repository_passes_the_guard() -> None:
    assert ci_paths.guard() == []


def _wf_with(job: str, body: dict[str, object], marker: str | None = None) -> ci_paths.Workflow:
    wf = ci_paths.load_workflow()
    wf.jobs[job] = body
    wf.markers[job] = {marker} if marker else set()
    gate = wf.jobs["ci-success"]
    gate["needs"] = [*gate["needs"], job]
    return wf


def test_guard_catches_an_ungated_unmarked_job() -> None:
    wf = _wf_with("new-tests", {"runs-on": "ubuntu-22.04", "steps": []})
    assert any("new-tests is gated by no filter" in e for e in ci_paths.guard(wf))


def test_guard_accepts_a_marked_always_run_job() -> None:
    wf = _wf_with("new-tests", {"runs-on": "ubuntu-22.04", "steps": []}, marker="always-run")
    assert ci_paths.guard(wf) == []


def test_guard_catches_a_filter_that_does_not_exist() -> None:
    wf = _wf_with("new-tests", {"needs": ["changes"], "if": "needs.changes.outputs.nope == 'true'", "steps": []})
    assert any("needs.changes.outputs.nope, which is no filter" in e for e in ci_paths.guard(wf))


def test_guard_catches_a_job_its_filter_cannot_run() -> None:
    wf = _wf_with("new-tests", {"needs": ["changes"], "if": "needs.changes.outputs.e2e == 'false'", "steps": []})
    assert any("new-tests does not run when e2e alone is true" in e for e in ci_paths.guard(wf))


def test_guard_catches_a_skipped_need_that_skips_the_job() -> None:
    # e2e needs integration: without !cancelled() a web-only change would skip it silently
    wf = ci_paths.load_workflow()
    wf.jobs["e2e"] = {**wf.jobs["e2e"], "if": "needs.changes.outputs.e2e == 'true'"}
    assert any("job e2e does not run when e2e alone is true" in e for e in ci_paths.guard(wf))


def test_guard_catches_a_gate_left_out_of_ci_success() -> None:
    wf = ci_paths.load_workflow()
    wf.jobs["ci-success"]["needs"] = [n for n in wf.jobs["ci-success"]["needs"] if n != "launcher"]
    assert any("launcher is not in ci-success's needs" in e for e in ci_paths.guard(wf))


def test_guard_catches_an_unused_filter() -> None:
    filters = ci_paths.load_filters()
    filters["orphan"] = ["orphan/**"]
    errs = ci_paths.guard(filters=filters, files=[])
    assert any("filter orphan is no job's" in e for e in errs)
    assert any("outputs are not the filters" in e for e in errs)


def test_guard_catches_a_negated_glob() -> None:
    filters = ci_paths.load_filters()
    filters["lint_go"] = [*filters["lint_go"], "!docs/**"]
    assert any("not a plain glob" in e for e in ci_paths.guard(filters=filters, files=[]))


def test_guard_catches_a_file_no_job_reads() -> None:
    errs = ci_paths.guard(files=["newthing/main.rs", "api/main.go", "docs/x.md"])
    assert len(errs) == 1 and "newthing/main.rs" in errs[0]


def test_globs_read_as_paths_filter_does() -> None:
    assert ci_paths.matches("api/**", "api/internal/x.go")
    assert not ci_paths.matches("api/**", "apix/main.go")
    assert ci_paths.matches("*.md", "README.md")
    assert not ci_paths.matches("*.md", "docs/README.md")
    assert ci_paths.matches("**/.gitignore", ".gitignore")
    assert ci_paths.matches("**/.gitignore", "apps/tv/.gitignore")
    assert ci_paths.matches(
        "apps/apple/Maktaba.xcodeproj/xcshareddata/xcschemes/Maktaba Player*",
        "apps/apple/Maktaba.xcodeproj/xcshareddata/xcschemes/Maktaba Player iPad.xcscheme",
    )
    assert not ci_paths.matches(
        "apps/apple/Maktaba.xcodeproj/xcshareddata/xcschemes/Maktaba Player*",
        "apps/apple/Maktaba.xcodeproj/xcshareddata/xcschemes/Maktaba Remote.xcscheme",
    )


@pytest.mark.parametrize(
    ("expr", "want"),
    [
        ("'a' == 'A'", True),
        ("!cancelled() && ('x' == 'y' || true)", True),
        ("github.event_name != 'pull_request' || steps.filter.outputs.e2e == 'true'", True),
        ("needs.changes.outputs.missing == 'true'", False),
        ("format('{0}-{1}', 'a', 'b') == 'a-b'", True),
    ],
)
def test_expressions(expr: str, want: bool) -> None:
    ctx = {"github": {"event_name": "push"}, "needs": {"changes": {"outputs": {}}}, "steps": {"filter": {"outputs": {}}}}
    assert ci_paths.evaluate(expr, ctx) is want
