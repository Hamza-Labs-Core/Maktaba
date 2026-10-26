"""The docs' relative links resolve (Epic 32 story 32.12 TC1, Epic 35 story 35.15 TC5).

Every relative link in the TV runbook (``docs/tv-cluster-and-remote.md``) and the Epic 32
specs, and in the update docs (``docs/updates.md``, ``docs/auto-update.md``, the cluster docs,
the packaging docs) and the Epic 35 specs, points at a file that exists, and every ``#anchor``
into one of those pages names one of its headings. The in-app help's own links are checked by
web/src/help/help.coverage.test.ts (the Live TV page links there now). CI counts these files
as code (ci.yml's ``meta`` filter), so a change to them runs this test.
"""

from __future__ import annotations

import functools
import os
import re
from pathlib import Path

import pytest
import yaml

pytestmark = pytest.mark.unit

ROOT = Path(__file__).resolve().parents[3]
RUNBOOK = ROOT / "docs" / "tv-cluster-and-remote.md"
SPECS = ROOT / "specs" / "epics" / "32-tv-streaming-cluster-remote"
UPDATE_SPECS = ROOT / "specs" / "epics" / "35-cluster-full-nodes-and-updates"
CI = ROOT / ".github" / "workflows" / "ci.yml"

LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def _update_docs() -> list[Path]:
    """The update docs (story 35.15): what an admin reads about updates, and the epic."""
    return [
        ROOT / "docs" / "updates.md",
        ROOT / "docs" / "auto-update.md",
        *sorted((ROOT / "docs" / "cluster").glob("*.md")),
        ROOT / "deploy" / "packaging" / "README.md",
        ROOT / "deploy" / "packaging" / "upgrade.md",
        ROOT / "deploy" / "packaging" / "node" / "README.md",
        *sorted(UPDATE_SPECS.glob("*.md")),
    ]


@functools.cache
def _anchors(md: Path) -> set[str]:
    """A page's anchors as GitHub makes them: each heading (code blocks aside), lowercased,
    without punctuation, spaces as hyphens, a repeated one numbered; and explicit ids."""
    text = re.sub(r"```.*?```", "", md.read_text(encoding="utf-8"), flags=re.S)
    ids = set(re.findall(r'<a id="([^"]+)"></a>', text))
    seen: dict[str, int] = {}
    for h in re.findall(r"^#+ (.+?)\s*$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", h.lower()).replace(" ", "-")
        n = seen.get(slug, 0)
        seen[slug] = n + 1
        ids.add(slug if n == 0 else f"{slug}-{n}")
    return ids


def _links(md: Path) -> list[str]:
    text = md.read_text(encoding="utf-8")
    # code spans and blocks are not links
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    text = re.sub(r"`[^`]*`", "", text)
    return [m for m in LINK.findall(text) if not re.match(r"^[a-z]+:", m)]


@functools.cache
def _exists(path: Path) -> bool:
    return path.exists()


def _target(md: Path, target: str) -> Path:
    """Where a link of md points, as an absolute path: normalised as text, not resolved on
    the disk (the docs hold no symlinks, and resolving each link made the file system calls
    the bulk of this test's time)."""
    return Path(os.path.normpath(os.path.abspath(md.parent / target if target else md)))


def _broken(files: list[Path], checked: set[Path]) -> list[str]:
    """The links of files that name no file, or an anchor missing from a page of checked."""
    checked = {_target(f, "") for f in checked}
    bad: list[str] = []
    for md in files:
        for link in _links(md):
            target, _, anchor = link.partition("#")
            path = _target(md, target)
            if not _exists(path):
                bad.append(f"{md.relative_to(ROOT)}: {link} (no such file)")
                continue
            if anchor and path in checked and anchor not in _anchors(path):
                bad.append(f"{md.relative_to(ROOT)}: {link} (no such anchor)")
    return bad


def test_runbook_and_specs_links_resolve() -> None:
    files = [RUNBOOK, *sorted(SPECS.glob("*.md"))]
    bad = _broken(files, {RUNBOOK})
    assert not bad, "\n".join(bad)


def test_update_docs_links_resolve() -> None:
    files = _update_docs()
    assert all(f.exists() for f in files), [str(f) for f in files if not f.exists()]
    bad = _broken(files, set(files))
    assert not bad, "\n".join(bad)


def test_update_docs_cover_the_story() -> None:
    """Story 35.15 AC4–AC6: the pages say what an admin needs, in the words of the app."""
    updates = (ROOT / "docs" / "updates.md").read_text(encoding="utf-8")
    for needle in (
        "## Settings",
        "auto_install",
        "node_failure_policy",
        "wheelhouse",
        "Update now",
        "deferred_until",
        "pinned_version",
        "## What happens to TV",
        "tv_change_pending",
        "maktaba-server restore",
        "--certificate-identity",
    ):
        assert needle in updates, needle
    runbook = (ROOT / "docs" / "cluster" / "runbook-updates.md").read_text(encoding="utf-8")
    for needle in (
        "maktaba-server update --rollback",
        "maktaba-launcher --status",
        "updates/journal.json",
        "maktaba-launcher.old",
        "restore_no_release",
        "failed_tv",
        "worker_only",
        "## Known issues",
    ):
        assert needle in runbook, needle


def test_runbook_covers_the_story_sections() -> None:
    text = RUNBOOK.read_text(encoding="utf-8")
    for needle in (
        "serve --role streaming",
        "spill_at",
        "Drain",
        "1,000-minute block = $5/month",
        "Stream: Edit",
        "Account Analytics: Read",
        "Test connection",
        "6.5 Mbit/s",
        "143 Mbit/s",
        "/auto/v{n}",
        "Plex's own remote access",
        "ERR_STORAGE_QUOTA_EXHAUSTED",
        "Rotate signing key",
        "relay_or_public_ip",
    ):
        assert needle in text, needle


def _meta_patterns() -> list[str]:
    """The ``meta`` filter of ci.yml's changes job: paths that are code whatever their extension."""
    jobs = yaml.safe_load(CI.read_text(encoding="utf-8"))["jobs"]
    for step in jobs["changes"]["steps"]:
        if step.get("id") == "filter":
            patterns: list[str] = yaml.safe_load(step["with"]["filters"])["meta"]
            return patterns
    raise AssertionError("ci.yml's changes job has no `filter` step")


def _matches(pattern: str, path: str) -> bool:
    """A paths-filter (picomatch) glob: ``**`` spans folders, ``*`` stays within one."""
    rx, i = "", 0
    while i < len(pattern):
        if pattern.startswith("**/", i):
            rx, i = rx + "(?:.*/)?", i + 3
        elif pattern.startswith("**", i):
            rx, i = rx + ".*", i + 2
        elif pattern[i] == "*":
            rx, i = rx + "[^/]*", i + 1
        else:
            rx, i = rx + re.escape(pattern[i]), i + 1
    return re.fullmatch(rx, path) is not None


def test_ci_counts_these_docs_as_code() -> None:
    patterns = _meta_patterns()
    files = [RUNBOOK, *sorted(SPECS.glob("*.md")), *_update_docs()]
    read = [f.relative_to(ROOT).as_posix() for f in files]
    unlisted = [f for f in read if not any(_matches(p, f) for p in patterns)]
    assert not unlisted, (
        f"markdown this test reads that ci.yml's meta filter does not list (a change to it would "
        f"skip the gates as docs only): {unlisted}"
    )
    # and the rest of the docs are still docs
    assert not any(_matches(p, "docs/another-page.md") for p in patterns)
    assert not any(_matches(p, "specs/epics/38-windows-app/README.md") for p in patterns)
