"""Epic 32 story 32.12 TC1: the TV runbook's relative links resolve.

Every relative link in ``docs/tv-cluster-and-remote.md`` and in the Epic 32 specs points at
a file that exists, and every ``#anchor`` into the runbook names one of its anchors (the Live
TV admin page links straight to them: web/src/pages/Admin/LiveTV/docs.ts). CI counts these
files as code (ci.yml's ``meta`` filter), so a change to them runs this test.
"""

from __future__ import annotations

import re
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
RUNBOOK = ROOT / "docs" / "tv-cluster-and-remote.md"
SPECS = ROOT / "specs" / "epics" / "32-tv-streaming-cluster-remote"
ADMIN_LINKS = ROOT / "web" / "src" / "pages" / "Admin" / "LiveTV" / "docs.ts"
CI = ROOT / ".github" / "workflows" / "ci.yml"

LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)\)")


def _anchors(md: Path) -> set[str]:
    text = md.read_text(encoding="utf-8")
    ids = set(re.findall(r'<a id="([^"]+)"></a>', text))
    for h in re.findall(r"^#+ (.+)$", text, re.M):
        slug = re.sub(r"[^\w\- ]", "", h.lower()).strip().replace(" ", "-")
        ids.add(slug)
    return ids


def _links(md: Path) -> list[str]:
    text = md.read_text(encoding="utf-8")
    # code spans and blocks are not links
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    text = re.sub(r"`[^`]*`", "", text)
    return [m for m in LINK.findall(text) if not re.match(r"^[a-z]+:", m)]


def test_runbook_and_specs_links_resolve() -> None:
    files = [RUNBOOK, *sorted(SPECS.glob("*.md"))]
    bad: list[str] = []
    for md in files:
        for link in _links(md):
            target, _, anchor = link.partition("#")
            path = (md.parent / target).resolve() if target else md
            if not path.exists():
                bad.append(f"{md.relative_to(ROOT)}: {link} (no such file)")
                continue
            if anchor and path == RUNBOOK and anchor not in _anchors(RUNBOOK):
                bad.append(f"{md.relative_to(ROOT)}: {link} (no such anchor)")
    assert not bad, "\n".join(bad)


def test_admin_page_anchors_exist() -> None:
    anchors = _anchors(RUNBOOK)
    wanted = re.findall(r':\s*"([a-z-]+)"', ADMIN_LINKS.read_text(encoding="utf-8"))
    assert wanted, "no anchors found in docs.ts"
    missing = [a for a in wanted if a not in anchors]
    assert not missing, f"the Live TV page links to missing runbook anchors: {missing}"


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
    read = [f.relative_to(ROOT).as_posix() for f in [RUNBOOK, *sorted(SPECS.glob("*.md"))]]
    unlisted = [f for f in read if not any(_matches(p, f) for p in patterns)]
    assert not unlisted, (
        f"markdown this test reads that ci.yml's meta filter does not list (a change to it would "
        f"skip the gates as docs only): {unlisted}"
    )
    # and the rest of the docs are still docs
    assert not any(_matches(p, "docs/another-page.md") for p in patterns)
    assert not any(_matches(p, "specs/epics/38-windows-app/README.md") for p in patterns)
