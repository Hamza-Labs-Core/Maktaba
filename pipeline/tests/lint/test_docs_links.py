"""Epic 32 story 32.12 TC1: the TV runbook's relative links resolve.

Every relative link in ``docs/tv-cluster-and-remote.md`` and in the Epic 32 specs points at
a file that exists, and every ``#anchor`` into the runbook names one of its anchors (the Live
TV admin page links straight to them: web/src/pages/Admin/LiveTV/docs.ts).
"""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
RUNBOOK = ROOT / "docs" / "tv-cluster-and-remote.md"
SPECS = ROOT / "specs" / "epics" / "32-tv-streaming-cluster-remote"
ADMIN_LINKS = ROOT / "web" / "src" / "pages" / "Admin" / "LiveTV" / "docs.ts"

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
