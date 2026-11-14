"""Epic 58 story 58.04 T02: no review credential in the repository. The store pages, the review
home's deploy files and its tools hold no password-shaped value, and every store's submission
notes name the secret and point to review-home.md."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "ci"))

import review_credentials as rc  # noqa: E402


def test_the_repository_holds_no_review_credential() -> None:
    assert rc.check() == []
    scanned = {p.relative_to(ROOT).as_posix() for p in rc.files()}
    assert {"docs/stores/review-home.md", "docs/stores/roku.md", "deploy/review-home/seed.sh",
            "deploy/review-home/content/manifest.json", "tools/review_home/probe.py"} <= scanned


@pytest.mark.parametrize(
    "line",
    [
        "Password: hunter2-correct-horse",
        "password = 'S3cr3t!pass'",
        "The password is Tr0ub4dor&3x",
        "REVIEW_HOME_VIEWER_PASSWORD=plain-text-value-1",
        "- **Password:** `aB3dE6gH9jK`",
        'export REVIEW_HOME_CHILD_PASSWORD="child-pass-0001"',
    ],
)
def test_a_written_password_is_found(line: str) -> None:
    assert rc.suspect_values(line), line


@pytest.mark.parametrize(
    "line",
    [
        "Password: {{REVIEW_HOME_VIEWER_PASSWORD}}",
        "the password lives in `REVIEW_HOME_VIEWER_PASSWORD`",
        "Password: <the release secret>",
        ': "${REVIEW_HOME_VIEWER_PASSWORD:?the password of the viewer}"',
        "password: ${{ secrets.REVIEW_HOME_VIEWER_PASSWORD }}",
        "pw, no := p[1], false; password = args.password",
        "- **The password rotates** after each submission round.",
        "a password shorter than 16 characters",
        "| 1 | The logic tests | pass |",
    ],
)
def test_a_named_secret_or_prose_passes(line: str) -> None:
    assert rc.suspect_values(line) == [], line


def test_a_store_page_without_the_notes_fails(tmp_path: Path) -> None:
    (tmp_path / "docs" / "stores").mkdir(parents=True)
    for store in rc.STORES:
        (tmp_path / "docs" / "stores" / f"{store}.md").write_text(
            f"# {store}\n\nSee [review-home.md](review-home.md); the secret {rc.NOTES_VAR}.\n", encoding="utf-8"
        )
    assert rc.check(tmp_path) == []
    (tmp_path / "docs" / "stores" / "roku.md").write_text("# Roku\n\nPassword: hunter2-roku-pass\n", encoding="utf-8")
    problems = rc.check(tmp_path)
    assert any("roku.md:3: a password-shaped value" in p for p in problems)
    # the report names the line, never any part of the value
    assert not any("hun" in p for p in problems), problems
    assert any("roku.md: the submission notes do not name the secret" in p for p in problems)
    assert any("roku.md: the submission notes do not point to review-home.md" in p for p in problems)
