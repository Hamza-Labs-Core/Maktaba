"""tools/ci/reference_images.py: a changed reference image is listed with why (story 60.04, T09)."""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
_spec = importlib.util.spec_from_file_location("reference_images", ROOT / "tools/ci/reference_images.py")
assert _spec and _spec.loader
mod = importlib.util.module_from_spec(_spec)
sys.modules["reference_images"] = mod
_spec.loader.exec_module(mod)

SHOT = "web/packages/website/e2e/__screenshots__/home-en-360.png"
SHOT_AR = "web/packages/website/e2e/__screenshots__/home-ar-360.png"
STATIC = "web/packages/website/static/shots/tv-home-ar.webp"


def files(*names: str) -> list[dict[str, str]]:
    return [{"filename": n, "status": "modified"} for n in names]


def test_only_the_reference_folders_count() -> None:
    got = mod.changed_references(
        files("web/packages/website/src/worker.ts", SHOT, STATIC, "web/packages/devportal/static/x.png")
        + [{"filename": "web/packages/website/e2e/__screenshots__/new.png", "status": "renamed",
            "previous_filename": "web/packages/website/e2e/__screenshots__/old.png"}]
    )
    assert got == [
        SHOT,
        STATIC,
        "web/packages/website/e2e/__screenshots__/new.png",
        "web/packages/website/e2e/__screenshots__/old.png",
    ]


# T09: a body with no section while a reference changed fails, naming the file
def test_t09_no_section_names_the_file() -> None:
    got = mod.problems([SHOT], "Adds the home page.\n\nTests: the e2e run.")
    assert got == [f"{SHOT}: not listed under `Reference images:`"]


@pytest.mark.parametrize(
    "body",
    [
        f"Reference images:\n- {SHOT}: the hero's new copy\n- {SHOT_AR}: the hero's new copy, Arabic\n",
        f"## Reference images\n\n- `{SHOT}` — the hero's new copy\n- `{SHOT_AR}` - same, in Arabic\n",
        f"**Reference images:**\n* {SHOT} (the hero's new copy)\n* {SHOT_AR}: Arabic\n\n## Tests\n",
    ],
)
def test_every_file_listed_with_a_reason_passes(body: str) -> None:
    assert mod.problems([SHOT, SHOT_AR], body) == []


def test_one_unlisted_and_one_without_a_reason() -> None:
    body = f"Reference images:\n- `{SHOT}`\n"
    assert mod.problems([SHOT, SHOT_AR], body) == [
        f"{SHOT}: listed with no reason (one line on why it changed)",
        f"{SHOT_AR}: not listed under `Reference images:`",
    ]


def test_a_path_outside_the_section_does_not_count() -> None:
    body = f"Changes:\n- {SHOT}: new hero\n\nReference images:\n- {SHOT_AR}: Arabic\n\nNotes:\n- {STATIC}: x\n"
    assert mod.problems([SHOT, SHOT_AR, STATIC], body) == [
        f"{SHOT}: not listed under `Reference images:`",
        f"{STATIC}: not listed under `Reference images:`",
    ]


def test_cli_with_files(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    body = tmp_path / "body.md"
    changed = tmp_path / "files.txt"
    changed.write_text(f"README.md\n{SHOT}\n")
    body.write_text("No section.\n")
    assert mod.main(["--body", str(body), "--files", str(changed)]) == 1
    assert f"::error::{SHOT}: not listed" in capsys.readouterr().out
    # T12's half that a test can hold: the body edited to add the section passes
    body.write_text(f"Reference images:\n- {SHOT}: the hero's new copy\n")
    assert mod.main(["--body", str(body), "--files", str(changed)]) == 0
    changed.write_text("README.md\n")
    body.write_text("No section.\n")
    assert mod.main(["--body", str(body), "--files", str(changed)]) == 0


def test_reads_the_body_and_every_page_of_files_from_the_api(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The body and files come from the API, not the event (a re-run reads the edited body),
    and the file list is read page by page."""
    event = tmp_path / "event.json"
    event.write_text(json.dumps({"pull_request": {"number": 7, "body": "STALE BODY"}}))
    monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))
    monkeypatch.setenv("GITHUB_REPOSITORY", "o/r")
    monkeypatch.setenv("GH_TOKEN", "t")
    page1 = [{"filename": f"web/x{i}.ts"} for i in range(100)]
    page2 = [{"filename": SHOT}]
    asked: list[str] = []

    def fake_get(url: str, token: str) -> object:
        asked.append(url)
        if url.endswith("/pulls/7"):
            return {"body": f"Reference images:\n- {SHOT}: new hero\n"}
        if url.endswith("&page=1"):
            return page1
        if url.endswith("&page=2"):
            return page2
        raise AssertionError(url)

    monkeypatch.setattr(mod, "_get", fake_get)
    assert mod.main([]) == 0
    assert asked == [
        "https://api.github.com/repos/o/r/pulls/7",
        "https://api.github.com/repos/o/r/pulls/7/files?per_page=100&page=1",
        "https://api.github.com/repos/o/r/pulls/7/files?per_page=100&page=2",
    ]


def test_an_api_failure_fails_closed(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    event = tmp_path / "event.json"
    event.write_text(json.dumps({"pull_request": {"number": 7}}))
    monkeypatch.setenv("GITHUB_EVENT_PATH", str(event))
    monkeypatch.setenv("GITHUB_REPOSITORY", "o/r")
    monkeypatch.setenv("GH_TOKEN", "t")

    def boom(url: str, token: str) -> object:
        raise OSError("down")

    monkeypatch.setattr(mod, "_get", boom)
    assert mod.main([]) == 2


def test_the_workflow_reads_fresh_and_runs_on_edited() -> None:
    text = (ROOT / ".github/workflows/reference-images.yml").read_text()
    for kind in ("opened", "edited", "synchronize", "reopened"):
        assert kind in text
    assert "python3 tools/ci/reference_images.py" in text
