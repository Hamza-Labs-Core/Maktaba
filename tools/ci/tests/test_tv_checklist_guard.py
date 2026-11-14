"""Story 58.16 T03: the release submission guard stops a TV build whose real-device checklist
has no result for its version (or whose newest result failed), and lets a passed one through."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "release"))

import tv_checklist_guard as guard  # noqa: E402

PAGE = """# Samsung (Tizen)

## Real-device checklist

- Samsung checklist passed on 2020/2022/2025 TVs, launcher 9.9.9 (not a result: outside Results)

## Results

- Samsung checklist failed on 2020 TV, launcher 1.2.0 (2026-11-20): EAC3 not passed through
- Samsung checklist passed on 2020/2022/2025 TVs, launcher 1.2.0 (2026-11-22)
- Samsung checklist passed on 2020/2022/2025 TVs, launcher 1.3.0 (2026-12-01)
- Samsung checklist failed on 2022 TV, launcher 1.3.1 (2026-12-05): keys
- LG checklist passed on webOS 5/23/25 TVs, launcher 1.4.0 (pasted on the wrong page)

## Later section

- Samsung checklist passed on 2025 TV, launcher 1.4.0
"""


def test_a_version_with_no_result_stops_with_the_reason() -> None:
    ok, reason = guard.decide(PAGE, "samsung", "1.5.0")
    assert not ok
    assert reason.startswith("docs/stores/samsung.md has no checklist result for Samsung launcher 1.5.0")


def test_a_passed_result_continues_with_the_release_note_line() -> None:
    assert guard.decide(PAGE, "samsung", "1.3.0") == (
        True,
        "Samsung checklist passed on 2020/2022/2025 TVs, launcher 1.3.0 (2026-12-01)",
    )


def test_the_newest_result_for_a_version_decides() -> None:
    assert guard.decide(PAGE, "samsung", "1.2.0")[0]
    ok, reason = guard.decide(PAGE, "samsung", "1.3.1")
    assert not ok
    assert "failed" in reason and "1.3.1" in reason


@pytest.mark.parametrize("version", ["9.9.9", "1.4.0"])
def test_lines_outside_results_or_for_another_platform_do_not_count(version: str) -> None:
    assert not guard.decide(PAGE, "samsung", version)[0]


def test_the_component_must_be_the_platforms() -> None:
    page = "## Results\n\n- Roku checklist passed on Roku Ultra, launcher 1.0.0\n"
    assert not guard.decide(page, "roku", "1.0.0")[0]
    page = "## Results\n\n- Roku checklist passed on Roku Ultra, channel 1.0.0\n"
    assert guard.decide(page, "roku", "1.0.0")[0]


def _cli(tmp_path: Path, capsys: pytest.CaptureFixture[str], *args: str) -> tuple[int, dict[str, str]]:
    rc = guard.main(["--stores", str(tmp_path), *args])
    out = dict(line.split("=", 1) for line in capsys.readouterr().out.splitlines())
    return rc, out


def test_the_command_stops_and_continues(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    (tmp_path / "samsung.md").write_text(PAGE, encoding="utf-8")
    rc, out = _cli(tmp_path, capsys, "--platform", "samsung", "--version", "1.5.0")
    assert rc == 1 and out["ok"] == "false" and "no checklist result" in out["reason"]
    rc, out = _cli(tmp_path, capsys, "--platform", "samsung", "--version", "1.3.0")
    assert rc == 0 and out["ok"] == "true" and out["line"].endswith("launcher 1.3.0 (2026-12-01)")


def test_a_missing_checklist_stops(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    rc, out = _cli(tmp_path, capsys, "--platform", "lg", "--version", "1.0.0")
    assert rc == 1 and out["ok"] == "false" and "cannot be read" in out["reason"]


def test_a_version_that_is_not_x_y_z_is_refused(tmp_path: Path) -> None:
    with pytest.raises(SystemExit):
        guard.main(["--stores", str(tmp_path), "--platform", "lg", "--version", "v1.0"])


def test_every_platform_has_its_checklist_page_with_a_results_section() -> None:
    stores = ROOT / "docs" / "stores"
    # review-home.md is the store reviewers' home (story 58.04), not a platform's checklist
    pages = {p.stem for p in stores.glob("*.md")} - {"README", "review-home"}
    assert pages == set(guard.PLATFORMS)
    for key in guard.PLATFORMS:
        text = (stores / f"{key}.md").read_text(encoding="utf-8")
        assert "## Real-device checklist" in text, key
        assert "\n## Results\n" in text, key
        assert "README.md" in text, f"{key}.md links the rule"
    readme = (stores / "README.md").read_text(encoding="utf-8")
    for key, p in guard.PLATFORMS.items():
        assert f"`{p.name}`/`{p.component}`" in readme, key
    assert "tools/release/tv_checklist_guard.py" in readme


def test_every_platform_submission_step_runs_the_guard() -> None:
    # Story 58.16: a release session does not submit a TV build without its checklist's result.
    # Each page's submission (or publish) step names the guard for its own platform; Fire TV's
    # amazon_submit.py also runs it before staging the edit.
    stores = ROOT / "docs" / "stores"
    for key in guard.PLATFORMS:
        text = (stores / f"{key}.md").read_text(encoding="utf-8")
        assert f"tv_checklist_guard.py --platform {key}" in text, f"{key}.md's submission step names the guard"
    amazon = (ROOT / "tools" / "release" / "amazon_submit.py").read_text(encoding="utf-8")
    assert "tv_checklist_guard.main(" in amazon
