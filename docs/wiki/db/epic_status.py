#!/usr/bin/env python3
"""The counts on docs/wiki/epic-status.md, derived from its story rows.

Each epic's section has one row per story (its status in the second column). This script
counts them and rewrites the numbers the page states about them:

- the Summary table's Done / Partial / Not started / Deferred columns, one row per epic,
  and its Total row (the Verdict column is kept as written);
- the summary sentence near the top ("Of N stories across all M epics, X are done, ...").

It also checks that "What is left" lists exactly the stories whose row is not done, each
under its status's heading (Not started, Partial, Deferred or removed). That part is prose,
so it is never rewritten: a difference is an error to fix by hand.

    python3 docs/wiki/db/epic_status.py          # rewrite the counts
    python3 docs/wiki/db/epic_status.py --check  # fail, with the differences, if they are stale

Change a story's row, then run this; never edit the counts by hand. Stdlib only (CI: ci.yml's
wiki job; tests: test_epic_status.py).
"""

from __future__ import annotations

import argparse
import difflib
import re
import sys
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path

PAGE = Path(__file__).resolve().parents[1] / "epic-status.md"

# The four counted statuses, in the Summary table's column order.
DONE, PARTIAL, NOT_STARTED, DEFERRED = "done", "partial", "not started", "deferred"
STATUSES = (DONE, PARTIAL, NOT_STARTED, DEFERRED)
# A row's status cell starts with one of these words (bold or not, any case); what follows
# is a qualifier, such as "(sampled)" or "(#1441)". "removed" counts with deferred.
_PREFIXES = (("done", DONE), ("partial", PARTIAL), ("not started", NOT_STARTED),
             ("deferred", DEFERRED), ("removed", DEFERRED))
# "What is left"'s headings, by status.
LEFT_HEADINGS = {"Not started": NOT_STARTED, "Partial": PARTIAL, "Deferred or removed": DEFERRED}

EPIC_HEADING = re.compile(r"^### Epic (\d+)\b")
SENTENCE = re.compile(
    r"Of \d+ stories across all \d+ epics, \d+ are done, \d+ are partial, "
    r"\d+ are not started, and \d+ were deferred or removed on purpose")
# A story id: "26.2", "16.02", "31.R"; a range "54.47–54.51" in a list item.
STORY_ID = re.compile(r"(\d+)\.([0-9A-Za-z]+)")
ID_RANGE = re.compile(r"^(\d+)\.(\d+)\s*[–-]\s*(\d+)\.(\d+)")


class PageError(Exception):
    """The page cannot be read as a status page (a missing section, an unknown status)."""


@dataclass
class Story:
    key: str     # normalised id ("26.2", "16.2", "31.R"), or the row's name when it has none
    label: str   # the row's first cell, as written
    status: str  # one of STATUSES
    sampled: bool
    line: int    # 1-based


@dataclass
class Epic:
    number: int
    line: int
    stories: list[Story] = field(default_factory=list)

    def counts(self) -> Counter:
        return Counter(s.status for s in self.stories)


def cells(line: str) -> list[str]:
    return [c.strip() for c in line.strip().strip("|").split("|")]


def is_separator(line: str) -> bool:
    return re.match(r"^\|\s*:?-{3,}", line) is not None


def is_table_row(line: str) -> bool:
    return line.startswith("|") and not is_separator(line)


def is_story_header(cell: str) -> bool:
    return cell.replace("*", "").strip().lower() == "story"


def status_of(cell: str) -> str | None:
    s = cell.replace("*", "").strip().lower()
    for prefix, status in _PREFIXES:
        if s.startswith(prefix):
            return status
    return None


def story_key(label: str) -> str:
    m = STORY_ID.match(label.replace("*", "").strip())
    if not m:
        return label.strip()
    epic, part = m.groups()
    part = (part.lstrip("0") or "0") if part.isdigit() else part.upper()
    return f"{int(epic)}.{part}"


def parse_epics(lines: list[str]) -> list[Epic]:
    """Every `### Epic NN` section and its story table's rows."""
    epics: list[Epic] = []
    current: Epic | None = None
    in_table = False
    for i, line in enumerate(lines, 1):
        m = EPIC_HEADING.match(line)
        if m:
            current = Epic(int(m.group(1)), i)
            epics.append(current)
            in_table = False
            continue
        if line.startswith("## ") or line.startswith("### "):
            current = None
            continue
        line = line.lstrip()
        if current is None or is_separator(line):
            continue
        if not is_table_row(line):
            in_table = False
            continue
        row = cells(line)
        if not in_table:  # a table's header row
            in_table = is_story_header(row[0])
            continue
        status = status_of(row[1]) if len(row) > 1 else None
        if status is None:
            raise PageError(f"line {i}: story {row[0]!r} has an unknown status "
                            f"{row[1] if len(row) > 1 else ''!r} (expected one of: "
                            "done, partial, not started, deferred, removed)")
        current.stories.append(Story(story_key(row[0]), row[0], status,
                                     "sampled" in row[1].lower(), i))
    if not epics:
        raise PageError("no `### Epic NN` sections")
    for epic in epics:
        if not epic.stories:
            raise PageError(f"line {epic.line}: Epic {epic.number:02d} has no story rows")
    return epics


def section(lines: list[str], heading: str) -> tuple[int, int]:
    """[start, end) of the `## heading` section's body (0-based line indexes)."""
    try:
        start = lines.index(f"## {heading}") + 1
    except ValueError:
        raise PageError(f"no `## {heading}` section") from None
    end = next((j for j in range(start, len(lines)) if lines[j].startswith("## ")), len(lines))
    return start, end


def bold(n: int) -> str:
    return f"**{n}**"


def rewrite_summary(lines: list[str], epics: list[Epic], errors: list[str]) -> Counter:
    start, end = section(lines, "Summary")
    by_number = {e.number: e for e in epics}
    seen: set[int] = set()
    total = Counter()
    for e in epics:
        total += e.counts()
    total_row = None
    for j in range(start, end):
        line = lines[j].lstrip()
        if not is_table_row(line):
            continue
        row = cells(line)
        if row[0] == "Epic":
            continue
        if row[0].replace("*", "") == "Total":
            total_row = j
            counts = [bold(total[s]) for s in STATUSES]
        else:
            m = re.match(r"^(\d+)\b", row[0])
            if not m:
                raise PageError(f"line {j + 1}: Summary row {row[0]!r} names no epic")
            number = int(m.group(1))
            epic = by_number.get(number)
            if epic is None:
                errors.append(f"line {j + 1}: the Summary lists Epic {number:02d}, "
                              "which has no `### Epic` section")
                continue
            seen.add(number)
            c = epic.counts()
            sampled = any(s.sampled for s in epic.stories if s.status == DONE)
            counts = [f"{c[DONE]}{'*' if sampled else ''}"] + [str(c[s]) for s in STATUSES[1:]]
        if len(row) != 6:
            raise PageError(f"line {j + 1}: a Summary row has {len(row)} columns, not 6")
        indent = lines[j][:len(lines[j]) - len(line)]
        lines[j] = indent + "| " + " | ".join([row[0], *counts, row[5]]) + " |"
    for e in epics:
        if e.number not in seen:
            errors.append(f"line {e.line}: Epic {e.number:02d} has no row in the Summary table")
    if total_row is None:
        raise PageError("the Summary table has no **Total** row")
    return total


def rewrite_sentence(lines: list[str], total: Counter, n_epics: int) -> None:
    hits = [j for j, line in enumerate(lines) if SENTENCE.search(line)]
    if len(hits) != 1:
        raise PageError(f"expected one summary sentence (\"Of N stories across all M epics, "
                        f"...\"), found {len(hits)}")
    j = hits[0]
    new = (f"Of {sum(total.values())} stories across all {n_epics} epics, {total[DONE]} are "
           f"done, {total[PARTIAL]} are partial, {total[NOT_STARTED]} are not started, and "
           f"{total[DEFERRED]} were deferred or removed on purpose")
    lines[j] = SENTENCE.sub(new, lines[j])


def left_keys(text: str) -> list[str]:
    """The stories a "What is left" entry names: one id, or a range of ids in one epic."""
    text = text.replace("*", "").strip()
    m = ID_RANGE.match(text)
    if m:
        e1, a, e2, b = (int(x) for x in m.groups())
        if e1 != e2 or a > b:
            raise PageError(f"\"What is left\": the range {m.group(0)!r} is not within one epic")
        return [f"{e1}.{n}" for n in range(a, b + 1)]
    return [story_key(text)]


def parse_left(lines: list[str]) -> dict[str, list[tuple[str, int]]]:
    """{status: [(story key, line)]} from "What is left"'s three lists."""
    start, end = section(lines, "What is left")
    found: dict[str, list[tuple[str, int]]] = {s: [] for s in LEFT_HEADINGS.values()}
    status = None
    for j in range(start, end):
        line = lines[j]
        if line.startswith("### "):
            heading = line[4:].strip()
            if heading not in LEFT_HEADINGS:
                raise PageError(f"line {j + 1}: unknown \"What is left\" heading {heading!r}")
            status = LEFT_HEADINGS[heading]
            continue
        if status is None:
            continue
        if line.startswith("- "):
            m = re.match(r"^- \*\*(.+?)\*\*", line)
            if not m:
                raise PageError(f"line {j + 1}: a \"What is left\" item does not start with a "
                                "bold story id")
            entry = m.group(1)
        elif is_table_row(line.lstrip()):
            entry = cells(line)[0]
            if is_story_header(entry):
                continue
        else:
            continue
        found[status].extend((k, j + 1) for k in left_keys(entry))
    return found


def check_left(lines: list[str], epics: list[Epic], errors: list[str]) -> None:
    rows = {s.key: s for e in epics for s in e.stories}
    listed: dict[str, tuple[str, int]] = {}
    for status, entries in parse_left(lines).items():
        for key, line in entries:
            if key in listed:
                errors.append(f"line {line}: \"What is left\" lists {key} twice "
                              f"(also line {listed[key][1]})")
                continue
            listed[key] = (status, line)
            story = rows.get(key)
            if story is None:
                errors.append(f"line {line}: \"What is left\" lists {key}, which has no "
                              "story row")
            elif story.status != status:
                errors.append(f"line {line}: \"What is left\" lists {key} as {status}, but "
                              f"its row (line {story.line}) says {story.status}")
    for story in rows.values():
        if story.status != DONE and story.key not in listed:
            errors.append(f"line {story.line}: {story.label} is {story.status}, but "
                          "\"What is left\" does not list it")


def check_duplicates(epics: list[Epic], errors: list[str]) -> None:
    seen: dict[str, Story] = {}
    for e in epics:
        for s in e.stories:
            if s.key in seen:
                errors.append(f"line {s.line}: story {s.key} has two rows "
                              f"(also line {seen[s.key].line})")
            seen[s.key] = s


def render(text: str) -> tuple[str, list[str]]:
    """The page with its counts derived from its rows, and the errors no rewrite can fix."""
    lines = text.split("\n")
    errors: list[str] = []
    epics = parse_epics(lines)
    check_duplicates(epics, errors)
    total = rewrite_summary(lines, epics, errors)
    rewrite_sentence(lines, total, len(epics))
    check_left(lines, epics, errors)
    return "\n".join(lines), errors


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--check", action="store_true",
                    help="change nothing; fail if the page's counts are stale or its lists "
                         "disagree with its rows")
    ap.add_argument("page", nargs="?", type=Path, default=PAGE)
    args = ap.parse_args(argv)

    text = args.page.read_text(encoding="utf-8")
    try:
        new, errors = render(text)
    except PageError as exc:
        print(f"{args.page}: {exc}", file=sys.stderr)
        return 1
    for err in errors:
        print(f"{args.page}: {err}", file=sys.stderr)

    if new != text:
        if args.check:
            diff = difflib.unified_diff(text.splitlines(), new.splitlines(),
                                        f"{args.page} (committed)", f"{args.page} (from rows)",
                                        n=0, lineterm="")
            print("\n".join(diff))
            print(f"{args.page}: the counts disagree with the story rows; run "
                  "python3 docs/wiki/db/epic_status.py", file=sys.stderr)
            return 1
        args.page.write_text(new, encoding="utf-8")
        print(f"{args.page}: counts rewritten from the story rows")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
