"""tools/ci/app_words.py: every code the home sends has its words on the web and in both Remotes."""

from __future__ import annotations

import importlib.util
import shutil
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("app_words", ROOT / "tools/ci/app_words.py")
assert spec and spec.loader
app_words = importlib.util.module_from_spec(spec)
spec.loader.exec_module(app_words)

# what the check reads, whole files and folders
READ = [
    "pipeline/src/maktaba_pipeline/engine/graph.py",
    "pipeline/src/maktaba_pipeline/engine/causes.py",
    "api/internal/handlers/queues/causes.go",
    "web/src/i18n/en.json",
    "web/src/i18n/ar.json",
    "apps/android/remote/src/main/res/values/strings.xml",
    "apps/android/remote/src/main/res/values-ar/strings.xml",
    "apps/android/remote/src/main/kotlin/com/hamzalabs/maktaba/remote/ServerWords.kt",
    "apps/android/remote/src/main/kotlin/com/hamzalabs/maktaba/remote/AttentionWords.kt",
    "apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings",
    "apps/apple/MaktabaKit/Sources/MaktabaKit/StageWords.swift",
    "apps/apple/MaktabaKit/Sources/MaktabaKit/AttentionWords.swift",
]


@pytest.fixture
def repo(tmp_path: Path) -> Path:
    """A copy of what the check reads: the files above, and every .py and manifest of the home's."""
    files = [ROOT / f for f in READ]
    files += [p for d in ("pipeline/src", "plugins/first-party") for p in (ROOT / d).rglob("*.py") if "progress(" in p.read_text(encoding="utf-8")]
    files += (ROOT / "plugins/first-party").glob("*/manifest.toml")
    for f in files:
        to = tmp_path / f.relative_to(ROOT)
        to.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(f, to)
    return tmp_path


def edit(repo: Path, path: str, old: str, new: str) -> None:
    f = repo / path
    text = f.read_text(encoding="utf-8")
    assert old in text, f"{old!r} is not in {path}"
    f.write_text(text.replace(old, new, 1), encoding="utf-8")


def test_every_code_has_its_words() -> None:
    assert app_words.gaps() == []


def test_the_copy_reads_the_same(repo: Path) -> None:
    assert app_words.gaps(repo) == []
    assert app_words.stages(repo) == app_words.stages()
    assert app_words.steps(repo) == app_words.steps()


def test_a_new_step_without_words_is_named_on_each_client(repo: Path) -> None:
    (repo / "pipeline/src/maktaba_pipeline/new_stage.py").write_text('await ctx.progress("warp drive", 1, 2)\n')
    assert app_words.gaps(repo) == [
        f"{c}: no words for the step warp_drive" for c in ("the web", "the Android Remote", "the Apple Remote")
    ]


def test_a_listed_step_counts(repo: Path) -> None:
    (repo / "pipeline/src/maktaba_pipeline/new_stage.py").write_text(
        'STEPS = ["decode", "warp"]\nawait ctx.progress(STEPS[0], 1, 2, steps=STEPS)\n'
    )
    assert "the web: no words for the step warp" in app_words.gaps(repo)


def test_a_new_stage_in_a_manifest_is_named(repo: Path) -> None:
    manifest = next((repo / "plugins/first-party").glob("*/manifest.toml"))
    with manifest.open("a", encoding="utf-8") as f:
        f.write('\n[[stage]]\nname = "warp_stage"\nlabel = "Warp"\n')
    assert "the Apple Remote: no words for the stage warp_stage" in app_words.gaps(repo)


def test_a_new_cause_is_named_on_the_apps(repo: Path) -> None:
    edit(repo, "api/internal/handlers/queues/causes.go", '\t"nas_timeout":', '\t"warp_failed":          {"Warp failed", ActionRetry},\n\t"nas_timeout":')
    assert app_words.gaps(repo) == [f"{c}: no words for the cause warp_failed" for c in ("the Android Remote", "the Apple Remote")]


def test_words_with_no_arabic_are_named(repo: Path) -> None:
    edit(repo, "web/src/i18n/ar.json", '"queue.running.step.embed": "الفهرسة للبحث"', '"queue.running.step.embed": "Index"')
    found = app_words.gaps(repo)
    assert "the web: no Arabic for the step embed (Index for search)" in found


def test_a_step_said_two_ways_is_named(repo: Path) -> None:
    edit(repo, "apps/apple/MaktabaKit/Sources/MaktabaKit/StageWords.swift", '"fan_out": "Share out the work"', '"fan_out": "List the files"')
    assert any(g.startswith("the Apple Remote: the step fan_out reads") for g in app_words.gaps(repo))


def test_lists_read_short_fail(repo: Path) -> None:
    graph = repo / "pipeline/src/maktaba_pipeline/engine/graph.py"
    graph.write_text("", encoding="utf-8")
    assert "read short" in app_words.gaps(repo)[0]


def test_it_runs_on_every_pull_request() -> None:
    lint = yaml.safe_load((ROOT / ".github/workflows/_lint.yml").read_text(encoding="utf-8"))
    repo_job = lint["jobs"]["repo"]
    assert "if" not in repo_job
    assert any("tools/ci/app_words.py" in step.get("run", "") for step in repo_job["steps"])
