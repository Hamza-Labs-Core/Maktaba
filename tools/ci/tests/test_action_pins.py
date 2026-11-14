"""Actions that run with write permissions are pinned to a commit, not a tag.

A tag can be moved to other code; a commit cannot. `pnpm/action-setup` runs in jobs that hold
`contents: write` (the TV web release uploads with it), so every use is pinned to the commit
behind its release tag, with the tag kept in a trailing comment for readers and update tools.
"""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
PINNED = ("pnpm/action-setup", "sigstore/cosign-installer")
USE = re.compile(r"uses:\s*([\w.-]+/[\w.-]+)@(\S+)(.*)$")
SHA = re.compile(r"[0-9a-f]{40}")


def uses() -> list[tuple[str, int, str, str, str]]:
    found = []
    for wf in WORKFLOWS:
        for n, line in enumerate(wf.read_text().splitlines(), 1):
            m = USE.search(line)
            if m and m.group(1) in PINNED:
                found.append((wf.name, n, m.group(1), m.group(2), m.group(3)))
    return found


def test_some_workflow_uses_each_pinned_action() -> None:
    names = {u[2] for u in uses()}
    assert names == set(PINNED), f"no workflow uses {set(PINNED) - names}"


def test_each_use_is_a_commit_with_its_tag_named() -> None:
    bad = [
        f"{wf}:{n}: {name}@{ref}"
        for wf, n, name, ref, rest in uses()
        if not SHA.fullmatch(ref) or not re.search(r"#\s*v\d", rest)
    ]
    assert not bad, "pin these to a commit sha with a `# vX.Y.Z` comment:\n" + "\n".join(bad)


def test_one_commit_per_action() -> None:
    refs: dict[str, set[str]] = {}
    for _, _, name, ref, _ in uses():
        refs.setdefault(name, set()).add(ref)
    split = {name: sorted(r) for name, r in refs.items() if len(r) > 1}
    assert not split, f"every use of an action pins the same commit: {split}"
