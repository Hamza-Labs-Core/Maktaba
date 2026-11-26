#!/usr/bin/env python3
"""Changelog fragments: one small pair of files per pull request (changelog.d/README.md).

    changelog_fragment.py check [--base REF] [--head REF]
    changelog_fragment.py convert [--base REF] [--slug NAME] [--merge]

`check` validates bilingual fragment pairs and protects the release sections of both legacy
CHANGELOGs. A `Changelog-Edit: <reason>` commit trailer allows corrections or removal of an
existing fragment. A branch begun before fragments may still add [Unreleased] entries with a
warning. A repeat in one fragment or in two the branch adds is an error; a repeat of an entry
from the base or a merged parent warns because the page shows it once.

`convert` moves the entries this branch added to CHANGELOG.md and CHANGELOG.ar.md into one new
fragment pair, puts both files back, and stages those files. Entries the branch edited stay
edited, an entry that a fragment in the tree already has is not moved again, and no existing
fragment is changed. With --merge it finishes the two files of a merge in progress: main's files,
this branch's entries in a fragment (tools/dev/merge-main.sh runs it). A file resolved by hand,
staged or left with no conflict marker, stays: it stops.

Exit codes: 0 done, 1 the check failed, 2 usage, 3 nothing written: a person must look.
Standard library only, Python 3.9 (macOS's) or newer.
"""

from __future__ import annotations

import argparse
import datetime
import os
import pathlib
import re
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict
from collections.abc import Sequence

sys.dont_write_bytecode = True  # no __pycache__ beside a tool run in a merge
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import changelog_merge  # noqa: E402

DIR = "changelog.d"
README = f"{DIR}/README.md"
FILES = {"en": "CHANGELOG.md", "ar": "CHANGELOG.ar.md"}
UNRELEASED = {"en": "## [Unreleased]", "ar": "## [لم يصدر بعد]"}
# The sections, in the order a page lists them: English -> Arabic. The same as
# web/packages/devportal/scripts/parity.mjs (tools/ci/tests keeps them equal).
SECTIONS = {
    "Added": "أُضيف",
    "Changed": "تغيّر",
    "Deprecated": "أُهمل",
    "Removed": "أُزيل",
    "Fixed": "أُصلح",
    "Security": "الأمان",
}
NAME = re.compile(r"(\d{4}-\d\d-\d\d)-[a-z0-9]+(?:-[a-z0-9]+)*")
RELEASE_HEADING = re.compile(r"^## [^\n]+", re.MULTILINE)
# an Arabic fragment with more Latin prose letters than this was left in English (the most
# any entry of CHANGELOG.ar.md had when fragments began is 0.42)
MAX_LATIN = 0.5
TOOL = "tools/dev/changelog_fragment.py"
CONVERT = f"python3 {TOOL} convert"


def template(name: str) -> str:
    return f"""\
{DIR}/{name}.md:

    ### Fixed

    - **What changed, in a few words.** What a user sees, in a sentence or two, each
      further line indented by two spaces.

{DIR}/{name}.ar.md, the same sections and the same number of entries:

    ### {SECTIONS["Fixed"]}

    - **ما تغيّر، في كلمات قليلة.** ما يراه المستخدم، في جملة أو جملتين، وكل سطر يلي
      الأول مسبوق بمسافتين.

Sections: {", ".join(f"{en} / {ar}" for en, ar in SECTIONS.items())}"""


# ---- fragments ----------------------------------------------------------------------------


class Bad(Exception):
    """What is wrong, for the person (or agent) who wrote it."""


def outline(text: str, lang: str) -> dict[str, list[str]]:
    """A fragment's sections (by English name, in the file's order), each its entries."""
    names = list(SECTIONS) if lang == "en" else list(SECTIONS.values())
    out: dict[str, list[str]] = {}
    section = ""
    for n, line in enumerate(text.splitlines(), 1):
        if line.startswith("#"):
            m = re.fullmatch(r"### (.+?)\s*", line)
            if not m or m.group(1) not in names:
                raise Bad(f"line {n}: {line!r} is not a section; write one of: "
                          + ", ".join(f"### {x}" for x in names))  # fmt: skip
            section = list(SECTIONS)[names.index(m.group(1))]
            if section in out:
                raise Bad(f"line {n}: a second {line.strip()!r}; put its entries under the first")
            out[section] = []
        elif line.startswith("- "):
            if not section:
                raise Bad(f"line {n}: an entry above any section; begin with a section, '### {names[4]}'")
            out[section].append(line + "\n")
        elif not line.strip():
            if section and out[section]:
                out[section][-1] += "\n"
        elif line.startswith("  ") and section and out[section]:
            out[section][-1] += line + "\n"
        else:
            raise Bad(f"line {n}: {line[:60]!r} is neither a section ('### {names[4]}'), an entry ('- …') "
                      "nor a line of an entry (indented by two spaces)")  # fmt: skip
    if not out:
        raise Bad(f"it is empty; begin with a section, '### {names[4]}', and an entry, '- **…**'")
    for s, entries in out.items():
        if not entries:
            raise Bad(f"the section {s} has no entry ('- …')")
        out[s] = [e.strip() + "\n" for e in entries]
        if len(set(out[s])) != len(entries):
            repeated = next(e for e, count in Counter(out[s]).items() if count > 1)
            raise Bad(f"the section {s} repeats entry {repeated.splitlines()[0][:70]!r}")
    return out


def latin_share(md: str) -> float:
    """The Latin share of the letters of markdown's prose (as parity.mjs measures it)."""
    for pat in (r"`[^`\n]*`", r"\]\([^)]*\)", r"https?://\S+", r"<[^>]+>"):
        md = re.sub(pat, " ", md)
    latin = len(re.findall(r"[A-Za-z]", md))
    arabic = len(re.findall(r"[؀-ۿݐ-ݿࢠ-ࣿ]", md))
    return latin / (latin + arabic) if latin + arabic else 0.0


def check_fragments(files: dict[str, str]) -> list[tuple[str, str]]:
    """(file, what is wrong) for the files of changelog.d/ (name -> text), README.md aside."""
    errors: list[tuple[str, str]] = []
    stems: dict[str, dict[str, str]] = {}
    for name in sorted(files):
        stem = re.sub(r"(\.ar)?\.md$", "", name)
        m = NAME.fullmatch(stem)
        day = True
        if m:
            try:
                datetime.date.fromisoformat(m.group(1))
            except ValueError:
                day = False
        if not name.endswith(".md") or not m or not day:
            errors.append((name, "a fragment is named YYYY-MM-DD-slug.md, its Arabic YYYY-MM-DD-slug.ar.md "
                                 "(a real date; the slug in lower case letters, digits and single hyphens)"))  # fmt: skip
            continue
        stems.setdefault(stem, {})["ar" if name.endswith(".ar.md") else "en"] = name
    for stem, pair in sorted(stems.items()):
        for lang, other in (("en", "ar"), ("ar", "en")):
            if lang not in pair:
                missing = stem + (".ar.md" if lang == "ar" else ".md")
                errors.append((pair[other], f"no {DIR}/{missing}: an entry is written in English and Arabic together"))
        got: dict[str, dict[str, list[str]]] = {}
        for lang, name in pair.items():
            try:
                got[lang] = outline(files[name], lang)
            except Bad as e:
                errors.append((name, str(e)))
        if len(got) == 2:
            en, ar = ({s: len(x) for s, x in got[lang].items()} for lang in ("en", "ar"))
            if list(en.items()) != list(ar.items()):
                fmt = lambda d: ", ".join(f"{s}: {n}" for s, n in d.items())  # noqa: E731
                errors.append((pair["ar"], f"its entries are [{fmt(ar)}], the English has [{fmt(en)}]: "
                                           "the same sections, in the same order, with the same entries"))  # fmt: skip
            share = latin_share(files[pair["ar"]])
            if share > MAX_LATIN:
                errors.append((pair["ar"], f"{share:.0%} of its letters are Latin: write it in Arabic"))
    return errors


def pairs_of(files: dict[str, str]) -> dict[tuple[str, str, str], set[str]]:
    """Each entry (section, English, Arabic), and the fragments (their English files) it is in;
    a malformed pair has none."""
    out: dict[tuple[str, str, str], set[str]] = {}
    for name, text in files.items():
        ar = name[:-3] + ".ar.md"
        if name.endswith(".ar.md") or ar not in files:
            continue
        try:
            x, y = outline(text, "en"), outline(files[ar], "ar")
        except Bad:
            continue
        for s in x:
            for a, b in zip(x[s], y.get(s, [])):
                out.setdefault((s, a.strip("\n"), b.strip("\n")), set()).add(name)
    return out


def entry_names(files: dict[str, str]) -> dict[tuple[str, str, str], set[str]]:
    """Fragments sharing an entry's text in either language within the same section."""
    out: dict[tuple[str, str, str], set[str]] = defaultdict(set)
    for (section, en, ar), names in pairs_of(files).items():
        out[section, "en", en].update(names)
        out[section, "ar", ar].update(names)
    return out


# ---- git ------------------------------------------------------------------------------------


def git_bytes(*args: str, ok: bool = True, stdin: str = "") -> bytes:
    r = subprocess.run(["git", *args], input=stdin.encode("utf-8"), capture_output=True, check=False)
    if r.returncode and ok:
        raise Bad(f"git {' '.join(args)}: {r.stderr.decode('utf-8', 'replace').strip()}")
    return b"" if r.returncode else r.stdout


def git(*args: str, ok: bool = True) -> str:
    # from bytes: text mode would turn a lone "\r" into a line end
    return git_bytes(*args, ok=ok).decode("utf-8")


def has(rev: str, path: str) -> bool:
    return bool(git("rev-parse", "-q", "--verify", f"{rev}:{path}", ok=False))


def at(rev: str, path: str) -> str:
    return git("show", f"{rev}:{path}", ok=False)


def fork_points(base: str, head: str) -> list[str]:
    points = git("merge-base", "--all", base, head, ok=False).split()
    if not points:
        raise Bad(f"no merge-base of {base} and {head}: fetch them (git fetch origin main)")
    return points


def landed(base: str) -> int | None:
    """When the fragments landed on base (its first-parent commit that added the README)."""
    out = git("log", "-m", "--first-parent", "--diff-filter=A", "--format=%ct", base, "--", README).split()
    return int(out[-1]) if out else None


def started(base: str, head: str) -> int | None:
    """When the branch began: its oldest own commit's author date (a rebase keeps it)."""
    own = git("log", "--no-merges", "--format=%at", f"{base}..{head}").split()
    return min(map(int, own)) if own else None


# ---- check ------------------------------------------------------------------------------------


def release_sections(text: str) -> dict[str, str]:
    """The heading and body of each bracketed release section in a legacy changelog."""
    headings = list(RELEASE_HEADING.finditer(text))
    sections = {}
    for i, m in enumerate(headings):
        heading = m.group().strip()
        if not heading.startswith("## ["):
            continue
        if heading in sections:
            raise Bad(f"duplicate changelog heading: {heading}")
        end = headings[i + 1].start() if i + 1 < len(headings) else len(text)
        sections[heading] = text[m.start():end].rstrip()
    return sections


def frozen_correction(old: str, new: str) -> bool:
    """Whether all new entries are recognizable edits in their original sections."""
    if not old or not new:
        return False
    before, after = (changelog_merge.parse(text) for text in (old, new))
    old_prose, new_prose = (
        [line.strip() for part in doc.prose() for line in part.splitlines() if line.strip()]
        for doc in (before, after)
    )
    if old_prose != new_prose:
        return False
    sections: dict[tuple[str, str], list[changelog_merge.Entry]] = defaultdict(list)
    for entry in after.entries():
        sections[entry.section].append(entry)
    for section, entries in sections.items():
        previous = [e for e in before.entries() if e.section == section]
        added = Counter(e.text for e in entries) - Counter(e.text for e in previous)
        edits = changelog_merge.replacements(changelog_merge.Doc(previous), changelog_merge.Doc(entries))
        if added - Counter(edits.values()):
            return False
    return True


def check_frozen(
    base: str, point: str, head: str, was: dict[str, str], now: dict[str, str]
) -> list[tuple[str, str, str]]:
    """Protect legacy release sections and committed fragment filenames."""
    since = landed(base)
    began = started(base, head) if since is not None else None
    reason = git("log", "--format=%(trailers:key=Changelog-Edit,valueonly)", f"{base}..{head}").strip()
    notice = f"Changelog-Edit: {reason.splitlines()[0]}" if reason else ""
    out = []
    for name in sorted(was.keys() - now.keys()):
        message = notice or "removing or renaming a committed fragment needs a `Changelog-Edit: <reason>` trailer"
        out.append(("notice" if reason else "error", f"{DIR}/{name}", message))

    if since is None:
        return out  # this maintenance line has no fragments yet
    how = f"`{CONVERT}`" if has(head, TOOL) else (
        f"This branch has no {TOOL} yet: bring main in first (tools/dev/merge-main.sh), and then `{CONVERT}`")  # fmt: skip
    for lang, f in FILES.items():
        before, after = (release_sections(at(rev, f)) for rev in (point, head))
        for heading in sorted(before.keys() | after.keys()):
            old, new = before.get(heading, ""), after.get(heading, "")
            if old == new:
                continue
            if heading == UNRELEASED[lang] and (began is None or began < since):
                out.append(("warning", f, f"this branch began before the fragments, so its change of [Unreleased] is "
                                         f"accepted. {how} moves its entries to {DIR}/"))
                continue
            label = "[Unreleased]" if heading == UNRELEASED[lang] else heading[3:]
            correction = frozen_correction(old, new)
            if correction and reason:
                out.append(("notice", f, notice))
            else:
                message = f"{label} is frozen: a new entry is a fragment pair in {DIR}/. {how} moves this branch's entries there"
                if correction:
                    message += "; correcting an entry that is there already needs a `Changelog-Edit: <reason>` trailer"
                out.append(("error", f, message))
    return out


def fragments_here() -> dict[str, str]:
    """The files of changelog.d/ in the working tree but its README: name -> text."""
    folder = pathlib.Path(DIR)
    files = {}
    for p in sorted(folder.rglob("*")) if folder.is_dir() else []:
        name = p.relative_to(folder).as_posix()
        if p.is_file() and name != "README.md" and not name.startswith("."):
            files[name] = p.read_text(encoding="utf-8", errors="replace")
    return files


def fragments_at(rev: str) -> dict[str, str]:
    """The fragments of a commit, or ("") of the index: what the next commit holds."""
    if rev:
        names = git("ls-tree", "--name-only", f"{rev}:{DIR}", ok=False).splitlines()
    else:
        rows = (row.split("\t") for row in git("ls-files", "--stage", "--", DIR).splitlines())
        names = [f[len(DIR) + 1 :] for how, f in rows if how.endswith(" 0")]  # not one in conflict
    names = [n for n in names if n.endswith(".md") and n != "README.md"]
    # one process for all of them: "<sha> blob <bytes>" and the content, each
    raw = git_bytes("cat-file", "--batch", stdin="".join(f"{rev}:{DIR}/{n}\n" for n in names))
    out = {}
    for n in names:
        head, _, raw = raw.partition(b"\n")
        size = int(head.split()[2])
        out[n], raw = raw[:size].decode("utf-8", "replace"), raw[size + 1 :]
    return out


def came(name: str, head: str) -> bool:
    """The branch got the fragment by merging another branch that has it (one it is stacked on),
    and did not add it itself: not so for one a merge commit of its own added."""
    log = git("log", "-1", "-m", "--first-parent", "--diff-filter=A", "--format=%P", head, "--", f"{DIR}/{name}")
    return any(has(p, f"{DIR}/{name}") for p in log.split()[1:])


def check_twice(was: dict[str, str], now: dict[str, str], head: str) -> list[tuple[str, str, str]]:
    """(level, file, message) for an entry in a fragment the branch adds and in another one."""
    out = []
    reported = set()
    for (_, _, entry), names in sorted(entry_names(now).items()):
        if len(names) < 2:
            continue
        mine = sorted(n for n in names if n not in was and not came(n, head))
        if not mine:
            continue
        f = mine[-1]
        other = mine[0] if len(mine) > 1 else min(names - {f})
        if (f, other) in reported:
            continue
        reported.add((f, other))
        what = f"its entry {entry.splitlines()[0][:70]!r} is in"
        if len(mine) > 1:
            out.append(("error", f"{DIR}/{f}", f"{what} {DIR}/{other} too, which this branch also adds: keep one"))
        else:
            out.append(("warning", f"{DIR}/{f}", f"{what} {DIR}/{other} too, which is not this branch's: "
                                                 "the page shows it once, there is nothing to do"))  # fmt: skip
    return out


def check(base: str, head: str) -> int:
    files = fragments_here()
    found = [("error", f"{DIR}/{name}", what) for name, what in check_fragments(files)]
    point = fork_points(base, head)[0]
    was = fragments_at(point)
    branch = fragments_at(head)
    # At HEAD, include working-tree changes to fragments.
    here = git("rev-parse", head).strip() == git("rev-parse", "HEAD").strip()
    now = files if here else branch
    found += check_frozen(base, point, head, was, now)
    found += check_twice(was, now, head)
    for level, f, what in found:
        print(f"::{level} file={f}::{f}: {what}")
    if any(level == "error" for level, _, _ in found):
        print(f"\nA changelog entry is a pair of files of its own ({README}). Add:\n")
        print(template(f"{datetime.date.today().isoformat()}-your-slug"))
        print(f"\nOr, with the lines in CHANGELOG.md and CHANGELOG.ar.md already: {CONVERT}")
        return 1
    print(f"changelog fragments: {len(files) // 2} pair(s), in English and Arabic")
    return 0


# ---- convert -------------------------------------------------------------------------------------


def _words(doc: changelog_merge.Doc) -> list[str]:
    """The lines between entries that are neither blank nor a "### " heading."""
    return [x for p in doc.prose() for x in p.splitlines() if x.strip() and not x.startswith("### ")]


def take(base: str, ours: str, theirs: str, lang: str) -> tuple[list[tuple[str, str]], str]:
    """The entries ours adds under [Unreleased] (section, text), and the file without them:
    theirs, with ours's edits of the base's entries."""
    b, o, t = (changelog_merge.parse(x) for x in (base, ours, theirs))
    if _words(b) != _words(o):
        raise Bad(f"{FILES[lang]}: this branch changed text that is not an entry")
    edits = changelog_merge.replacements(b, o)
    try:
        changelog_merge.check_entry_moves(b, o, edits)
    except changelog_merge.Unmergeable as e:
        raise Bad(f"{FILES[lang]}: {e}") from e
    old = {e.text for e in b.entries()} | {e.text for e in t.entries()}
    new = [e for e in o.entries() if e.text not in old and e.text not in edits.values()]
    names = {f"### {SECTIONS[s] if lang == 'ar' else s}": s for s in SECTIONS}
    out = []
    for e in new:
        h2, h3 = e.section
        if h2 != UNRELEASED[lang] or h3 not in names:
            raise Bad(f"{FILES[lang]}: a new entry under {h2 or 'no release'} / {h3 or 'no section'}, not in a "
                      f"section of {UNRELEASED[lang]}: {e.text.splitlines()[0][:70]}")  # fmt: skip
        out.append((names[h3], e.text))
    ours_texts = {e.text for e in o.entries()}
    gone = {e.text for e in b.entries()} - ours_texts  # what the branch edited or removed
    if gone - {e.text for e in t.entries()}:
        raise Bad(f"{FILES[lang]}: main and this branch changed the same entry: merge it by hand")
    kept: list[str] = []
    for p in t.parts:
        if isinstance(p, str):
            # the blank line that separated a removed entry goes with it
            kept.append(p[1:] if kept and kept[-1] == "" and p.startswith("\n") else p)
        else:
            kept.append(edits.get(p.text, "") if p.text in gone else p.text)
    return out, "".join(kept)


def render(entries: Sequence[tuple[str, str]], lang: str) -> str:
    blocks = []
    for s, ar in SECTIONS.items():
        texts = [t.strip("\n") for sec, t in entries if sec == s]
        if texts:
            blocks.append("\n\n".join([f"### {ar if lang == 'ar' else s}", *texts]))
    return "\n\n".join(blocks) + "\n"


def write(files: dict[str, str]) -> None:
    for f, text in files.items():
        pathlib.Path(f).write_bytes(text.encode("utf-8"))
    git("add", "--", *files)


def slug_of(branch: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", branch.lower()).strip("-")[:60].strip("-") or "change"


def by_hand(f: str, point: str, theirs: str) -> bool:
    """A file of a merge in progress that a person resolved: staged, and not what git's own merge
    of it is; or not staged, with no conflict marker left."""
    now = pathlib.Path(f).read_bytes().decode("utf-8", "replace") if pathlib.Path(f).exists() else ""
    if git("ls-files", "-u", "--", f).strip():
        return not re.search(r"^<<<<<<< ", now, re.MULTILINE)
    ours, their = at("HEAD", f), at(theirs, f)
    with tempfile.TemporaryDirectory() as d:
        paths = []
        for name, text in (("ours", ours), ("base", at(point, f)), ("theirs", their)):
            paths.append(str(pathlib.Path(d, name)))
            pathlib.Path(d, name).write_bytes(text.encode("utf-8"))
        try:
            auto = git("merge-file", "-p", *paths)
        except Bad:
            return True  # Git could not produce a clean automatic merge.
        return auto != at("", f) or auto != now


def convert(base: str, slug: str | None, merging: bool) -> int:
    if merging:
        head, theirs = "HEAD", git("rev-parse", "-q", "--verify", "MERGE_HEAD", ok=False).strip()
        if not theirs:
            raise Bad("no merge in progress")
        points = fork_points(head, theirs)
    else:
        if git("rev-parse", "-q", "--verify", "MERGE_HEAD", ok=False).strip():
            raise Bad("a merge is in progress: tools/dev/merge-main.sh finishes it")
        points = fork_points(base, "HEAD")
    point = points[0]
    for f in FILES.values():
        if len({at(p, f) for p in points}) > 1:
            raise Bad(f"{f} has different contents at multiple merge bases: resolve it by hand")
    if merging:
        for f in FILES.values():
            if by_hand(f, point, theirs):
                raise Bad(f"{f} was resolved by hand: it stays as it is")
    else:
        theirs = point
    got: dict[str, list[tuple[str, str]]] = {}
    result: dict[str, str] = {}
    stale: dict[str, bool] = {}
    for lang, f in FILES.items():
        ours = at("HEAD", f) if merging else pathlib.Path(f).read_text(encoding="utf-8")
        got[lang], result[f] = take(at(point, f), ours, at(theirs, f), lang)
        stale[f] = ours != result[f]
    count = {lang: {s: sum(sec == s for sec, _ in got[lang]) for s in SECTIONS} for lang in FILES}
    if count["en"] != count["ar"]:
        fmt = lambda d: ", ".join(f"{s}: {n}" for s, n in d.items() if n) or "none"  # noqa: E731
        raise Bad(f"this branch adds [{fmt(count['en'])}] to CHANGELOG.md and [{fmt(count['ar'])}] to "
                  "CHANGELOG.ar.md: add the missing entries, then run it again")  # fmt: skip
    # an entry a fragment already has (a branch merged into this one moved it) stays there: one the
    # commit will hold, never a file only on disk (not added, or edited since)
    there = entry_names(fragments_at(""))
    by = {lang: [(s, t) for s in SECTIONS for sec, t in got[lang] if sec == s] for lang in FILES}
    new = [(s, a, b) for (s, a), (_, b) in zip(by["en"], by["ar"])
           if (s, "en", a.strip()) not in there and (s, "ar", b.strip()) not in there]
    if not new:
        # the files change all the same when the branch's entries are in a fragment already
        if not merging and not any(stale.values()):
            print("changelog_fragment: this branch adds no CHANGELOG entry; nothing to convert")
            return 0
        write(result)
        print("changelog_fragment: no entry to move; the CHANGELOGs are main's"
              + ("" if merging else " (staged: commit them)"))  # fmt: skip
        return 0
    branch = git("symbolic-ref", "-q", "--short", "HEAD", ok=False).strip() or git("rev-parse", "--short", "HEAD").strip()
    stem = f"{datetime.date.today().isoformat()}-{slug_of(slug or branch)}"
    name, n = stem, 1
    while any(pathlib.Path(DIR, name + x).exists() for x in (".md", ".ar.md")):
        n += 1
        name = f"{stem}-{n}"
    pair = {f"{name}.md": render([(s, a) for s, a, _ in new], "en"),
            f"{name}.ar.md": render([(s, b) for s, _, b in new], "ar")}  # fmt: skip
    errors = check_fragments(pair)
    if errors:
        raise Bad("; ".join(f"{f}: {what}" for f, what in errors))
    pathlib.Path(DIR).mkdir(exist_ok=True)
    write({**{f"{DIR}/{f}": text for f, text in pair.items()}, **result})
    total = len(new)
    print(f"changelog_fragment: moved {total} entr{'y' if total == 1 else 'ies'} to {DIR}/{name}.md and {DIR}/{name}.ar.md"
          + ("" if merging else " (staged: commit them with the two CHANGELOGs)"))  # fmt: skip
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Changelog fragments (changelog.d/README.md).")
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("check", help="validate fragments and check changes to the frozen changelogs")
    c.add_argument("--base", default="origin/main")
    c.add_argument("--head", default="HEAD")
    v = sub.add_parser("convert", help="move this branch's CHANGELOG entries into a fragment pair")
    v.add_argument("--base", default="origin/main")
    v.add_argument("--slug", help="the fragment's name after the date (default: the branch's name)")
    v.add_argument("--merge", action="store_true", help="finish the CHANGELOGs of a merge in progress")
    a = ap.parse_args(argv)
    try:
        top = git("rev-parse", "--show-toplevel").strip()
        os.chdir(top)  # every path here is from the top of the repository
        return check(a.base, a.head) if a.cmd == "check" else convert(a.base, a.slug, a.merge)
    except Bad as e:
        print(f"changelog_fragment: {e}", file=sys.stderr)
        return 1 if a.cmd == "check" else 3


if __name__ == "__main__":
    sys.exit(main())
