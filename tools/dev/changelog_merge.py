#!/usr/bin/env python3
"""Three-way merge of a CHANGELOG by entries (tools/dev/merge-main.sh).

    changelog_merge.py BASE OURS THEIRS OUT

BASE, OURS and THEIRS are the file at the merge base, on this branch and on main (git's
stages 1, 2 and 3). OUT gets main's file with this branch's changes made to it, entry by
entry, never line by line: a hunk-wise merge splits two new entries that share a line (a
blank line, a "maktaba.dev: ..." tail) around it and interleaves them.

An entry is a list item at column 0 ("- ...") with every line after it up to the next blank
line, heading or item, and each indented paragraph that follows it. The text between entries
(headings, intros) is kept as main has it.

- An entry this branch added goes at the end of its section on main (after main's entries
  there; a section is the "### " heading it sits under, inside its "## " release).
- An entry this branch changed takes the change, when main left it as the base had it.
- An entry this branch removed goes, when main left it as the base had it.

Exit 1, writing nothing, when the two sides cannot be told apart by entries: both changed
the same entry, one changed an entry the other removed, this branch changed text that is not
an entry, or this branch's new entry sits in a section where main has no entry left.
"""

from __future__ import annotations

import difflib
import pathlib
import re
import sys
from dataclasses import dataclass, field

ITEM = re.compile(r"^- ")
HEADING = re.compile(r"^#{1,6} ")


class Unmergeable(Exception):
    pass


@dataclass
class Entry:
    section: tuple[str, str]  # the "## " and "### " headings it sits under
    text: str  # its lines, each ending in "\n"


@dataclass
class Doc:
    """A CHANGELOG as a list of parts: the text between entries, and entries."""

    parts: list[Entry | str] = field(default_factory=list)

    def entries(self) -> list[Entry]:
        return [p for p in self.parts if isinstance(p, Entry)]

    def prose(self) -> list[str]:
        """The text between entries, without the blank lines that only separate entries: an
        entry added brings its separator, which is no change of the text (each side adds
        as many as it adds entries)."""
        return [p for p in self.parts if isinstance(p, str) and p.strip()]


def parse(text: str) -> Doc:
    doc = Doc()
    h2 = h3 = ""
    cur: list[str] | None = None  # the open entry's lines
    blank: list[str] = []  # the blank lines after it: its own when an indented line follows
    prose: list[str] = []

    def close_entry() -> None:
        nonlocal cur
        if cur is not None:
            doc.parts.append(Entry((h2, h3), "".join(cur)))
            cur = None
        prose.extend(blank)
        blank.clear()

    def close_prose() -> None:
        if prose:
            doc.parts.append("".join(prose))
            prose.clear()

    for line in text.splitlines(keepends=True):
        if not line.endswith("\n"):
            line += "\n"
        if ITEM.match(line):
            close_entry()
            close_prose()
            cur = [line]
            continue
        if cur is not None and not line.strip():
            blank.append(line)
            continue
        if cur is not None and (line.startswith("  ") or (not blank and not HEADING.match(line))):
            cur += [*blank, line]  # an entry's continuation line, or its next paragraph
            blank.clear()
            continue
        close_entry()
        if line.startswith("## "):
            h2, h3 = line.strip(), ""
        elif line.startswith("### "):
            h3 = line.strip()
        prose.append(line)
    close_entry()
    close_prose()
    return doc


# how alike (0 to 1, by words) an entry and its edit are; less, it is another entry
SAME_ENTRY = 0.5


def _alike(a: str, b: str) -> float:
    return difflib.SequenceMatcher(None, a.removeprefix("- ").split(), b.removeprefix("- ").split(), autojunk=False).ratio()


def replacements(base: Doc, ours: Doc) -> dict[str, str]:
    """Each entry of the base this branch changed, and what it changed it to."""
    base_set = {e.text for e in base.entries()}
    ours_set = {e.text for e in ours.entries()}
    # pair each removed base entry with the added entry that takes its place: the one most
    # like it among the added entries that follow its predecessor in ours's order (a new entry
    # put above an edited one is not its edit)
    replaced: dict[str, str] = {}
    ours_texts = [e.text for e in ours.entries()]
    base_texts = [e.text for e in base.entries()]
    for i, t in enumerate(base_texts):
        if t in ours_set:
            continue
        kept = (base_texts[j] for j in range(i - 1, -1, -1) if base_texts[j] in ours_set)
        prev = next(kept, None)
        start = ours_texts.index(prev) + 1 if prev is not None else 0
        best = SAME_ENTRY
        for new in ours_texts[start:]:
            if new in base_set:
                break
            if new not in replaced.values() and (like := _alike(t, new)) >= best:
                best, replaced[t] = like, new
    return replaced


def check_entry_moves(base: Doc, ours: Doc, replaced: dict[str, str]) -> None:
    """Stop when a move or a rewrite cannot be carried into the same section."""
    before = {e.text: e.section for e in base.entries()}
    after = {e.text: e.section for e in ours.entries()}
    for text, section in before.items():
        new = replaced.get(text, text)
        if new in after and after[new] != section:
            raise Unmergeable("an entry moved between sections: " + text.splitlines()[0][:80])
    if before.keys() - after.keys() - replaced.keys() and after.keys() - before.keys() - set(replaced.values()):
        raise Unmergeable("entries were both removed and added without a matching edit: place them by hand")


def merge(base_text: str, ours_text: str, theirs_text: str) -> str:
    base, ours, theirs = parse(base_text), parse(ours_text), parse(theirs_text)
    if ours.prose() != base.prose() and ours.prose() != theirs.prose():
        if theirs.prose() != base.prose():
            raise Unmergeable("both sides changed the text between entries")
        # only this branch changed the text between entries: not an entry's change
        raise Unmergeable("this branch changed text that is not an entry")

    base_set = {e.text for e in base.entries()}
    ours_set = {e.text for e in ours.entries()}
    theirs_set = {e.text for e in theirs.entries()}
    removed_ours = base_set - ours_set  # changed or removed on this branch
    added_ours = [e for e in ours.entries() if e.text not in base_set]

    replaced = replacements(base, ours)
    check_entry_moves(base, ours, replaced)

    out: list[Entry | str] = []
    for p in theirs.parts:
        if isinstance(p, Entry) and p.text in removed_ours:
            if p.text not in base_set:
                continue
            if p.text in replaced:
                out.append(Entry(p.section, replaced[p.text]))  # this branch's edit
            # else: this branch removed it
            continue
        out.append(p)
    for t in removed_ours:
        if t not in theirs_set:
            raise Unmergeable("an entry both sides changed or removed: " + t.splitlines()[0][:80])

    placed = set(replaced.values())
    new = [e for e in added_ours if e.text not in placed and e.text not in theirs_set]
    for e in new:
        # the end of its section on main: after the section's last entry (or its heading)
        idx = None
        for k, p in enumerate(out):
            if isinstance(p, Entry) and p.section == e.section:
                idx = k
        if idx is None:
            # main has no entry in that section (gone, or emptied): a person places it
            where = " / ".join(e.section)
            raise Unmergeable("main has no entry in a new entry's section: " + where)
        # entries are separated by one blank line: insert "\n" + the entry after idx
        out.insert(idx + 1, "\n")
        out.insert(idx + 2, Entry(e.section, e.text))
    return "".join(p.text if isinstance(p, Entry) else p for p in out)


def main(argv: list[str]) -> int:
    if len(argv) != 5:
        sys.stderr.write(__doc__ or "")
        return 2
    base, ours, theirs = (pathlib.Path(a).read_text(encoding="utf-8") for a in argv[1:4])
    try:
        out = merge(base, ours, theirs)
    except Unmergeable as e:
        sys.stderr.write(f"changelog_merge: {e}\n")
        return 1
    pathlib.Path(argv[4]).write_text(out, encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
