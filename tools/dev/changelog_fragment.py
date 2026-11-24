#!/usr/bin/env python3
"""Changelog fragments: one small pair of files per pull request (changelog.d/README.md).

    changelog_fragment.py check [--base REF] [--head REF]
    changelog_fragment.py convert [--base REF] [--slug NAME] [--merge]

`check` is the lint job's gate. It checks every pair in changelog.d/ (name, sections, entries,
the same sections and number of entries in English and Arabic), and refuses a branch that
changes the [Unreleased] section of CHANGELOG.md or CHANGELOG.ar.md: those entries are frozen,
a new one is a fragment. A branch begun before the fragments landed on its base (its oldest
commit is older) may still add lines there: it gets a warning. A commit in the branch with a
`Changelog-Edit: <reason>` trailer may correct frozen entries, never add one.

`convert` moves the entries this branch added to CHANGELOG.md and CHANGELOG.ar.md into one
fragment pair, puts both files back, and stages it all. Entries the branch edited stay edited.
With --merge it finishes the two files of a merge in progress: main's files, this branch's
entries in a fragment (tools/dev/merge-main.sh runs it).

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
TRAILER = re.compile(r"^Changelog-Edit:\s*(\S.*)$", re.MULTILINE)
# an Arabic fragment with more Latin prose letters than this was left in English (the most
# any entry of CHANGELOG.ar.md had when fragments began is 0.42)
MAX_LATIN = 0.5
CONVERT = "python3 tools/dev/changelog_fragment.py convert"


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
        out[s] = [e.strip("\n") + "\n" for e in entries]
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


# ---- git ------------------------------------------------------------------------------------


def git(*args: str, ok: bool = True) -> str:
    # bytes: text mode would turn a lone "\r" into a line end
    r = subprocess.run(["git", *args], capture_output=True, check=False)
    if r.returncode and ok:
        raise Bad(f"git {' '.join(args)}: {r.stderr.decode('utf-8', 'replace').strip()}")
    return "" if r.returncode else r.stdout.decode("utf-8")


def has(rev: str, path: str) -> bool:
    return bool(git("rev-parse", "-q", "--verify", f"{rev}:{path}", ok=False))


def at(rev: str, path: str) -> str:
    return git("show", f"{rev}:{path}", ok=False)


def fork_point(base: str, head: str) -> str:
    out = git("merge-base", base, head, ok=False).strip()
    if not out:
        raise Bad(f"no merge-base of {base} and {head}: fetch them (git fetch origin main)")
    return out


def landed(base: str) -> int | None:
    """When the fragments landed on base (its first-parent commit that added the README)."""
    out = git("log", "-m", "--first-parent", "--diff-filter=A", "--format=%ct", base, "--", README).split()
    return int(out[-1]) if out else None


def started(base: str, head: str) -> int | None:
    """When the branch began: its oldest own commit's author date (a rebase keeps it)."""
    own = git("log", "--no-merges", "--format=%at", f"{base}..{head}").split()
    return min(map(int, own)) if own else None


# ---- check ------------------------------------------------------------------------------------


def unreleased(text: str, lang: str) -> tuple[str, list[str]]:
    """The [Unreleased] section's text, and its entries."""
    m = re.search(rf"^{re.escape(UNRELEASED[lang])}\s*$(.*?)(?=^## |\Z)", text, re.MULTILINE | re.DOTALL)
    if not m:
        return "", []
    doc = changelog_merge.parse(m.group(0))
    return m.group(0).rstrip(), [e.text for e in doc.entries()]


def check_frozen(base: str, head: str) -> list[tuple[str, str, str]]:
    """(level, file, message) for a branch's changes of the [Unreleased] sections."""
    point = fork_point(base, head)
    changed = []
    for lang, f in FILES.items():
        was, now = at(point, f), at(head, f)
        if was != now and unreleased(was, lang)[0] != unreleased(now, lang)[0]:
            added = len(unreleased(now, lang)[1]) - len(unreleased(was, lang)[1])
            changed.append((f, added))
    if not changed:
        return []
    since, began = landed(base), started(base, head)
    if since is None:
        return []  # the base (a maintenance line) has no fragments
    if began is None or began < since:
        return [("warning", f, f"this branch began before the fragments, so its change of [Unreleased] is accepted; "
                               f"`{CONVERT}` moves its entries to {DIR}/, and nothing conflicts on them again")
                for f, _ in changed]  # fmt: skip
    reason = TRAILER.search(git("log", "--format=%B", f"{base}..{head}"))
    out = []
    for f, added in changed:
        if added > 0 or not reason:
            out.append(("error", f, f"[Unreleased] is frozen: a new entry is a fragment pair in {DIR}/. "
                                    f"`{CONVERT}` moves this branch's entries there"
                                    + ("" if added > 0 else "; correcting an entry that is there already needs a "
                                       "`Changelog-Edit: <reason>` trailer on a commit")))  # fmt: skip
        else:
            out.append(("notice", f, f"Changelog-Edit: {reason.group(1).strip()}"))
    return out


def check(base: str, head: str) -> int:
    folder = pathlib.Path(DIR)
    files = {}
    for p in sorted(folder.rglob("*")) if folder.is_dir() else []:
        name = p.relative_to(folder).as_posix()
        if p.is_file() and name != "README.md":
            files[name] = p.read_text(encoding="utf-8", errors="replace")
    found = [("error", f"{DIR}/{name}", what) for name, what in check_fragments(files)]
    found += check_frozen(base, head)
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


def slug_of(branch: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", branch.lower()).strip("-")[:60].strip("-") or "change"


def convert(base: str, slug: str | None, merging: bool) -> int:
    if merging:
        head, theirs = "HEAD", git("rev-parse", "-q", "--verify", "MERGE_HEAD", ok=False).strip()
        if not theirs:
            raise Bad("no merge in progress")
        point = fork_point(head, theirs)
    else:
        if git("rev-parse", "-q", "--verify", "MERGE_HEAD", ok=False).strip():
            raise Bad("a merge is in progress: tools/dev/merge-main.sh finishes it")
        point = theirs = fork_point(base, "HEAD")
    got: dict[str, list[tuple[str, str]]] = {}
    result: dict[str, str] = {}
    for lang, f in FILES.items():
        ours = at("HEAD", f) if merging else pathlib.Path(f).read_text(encoding="utf-8")
        got[lang], result[f] = take(at(point, f), ours, at(theirs, f), lang)
    if not got["en"] and not got["ar"]:
        print("changelog_fragment: this branch adds no CHANGELOG entry; nothing to convert")
        return 3 if merging else 0
    count = {lang: {s: sum(sec == s for sec, _ in got[lang]) for s in SECTIONS} for lang in FILES}
    if count["en"] != count["ar"]:
        fmt = lambda d: ", ".join(f"{s}: {n}" for s, n in d.items() if n) or "none"  # noqa: E731
        raise Bad(f"this branch adds [{fmt(count['en'])}] to CHANGELOG.md and [{fmt(count['ar'])}] to "
                  "CHANGELOG.ar.md: add the missing entries, then run it again")  # fmt: skip
    branch = git("symbolic-ref", "-q", "--short", "HEAD", ok=False).strip() or git("rev-parse", "--short", "HEAD").strip()
    stem = f"{datetime.date.today().isoformat()}-{slug_of(slug or branch)}"
    name, n = stem, 1
    while any(pathlib.Path(DIR, name + x).exists() for x in (".md", ".ar.md")):
        n += 1
        name = f"{stem}-{n}"
    pair = {f"{name}.md": render(got["en"], "en"), f"{name}.ar.md": render(got["ar"], "ar")}
    errors = check_fragments(pair)
    if errors:
        raise Bad("; ".join(f"{f}: {what}" for f, what in errors))
    pathlib.Path(DIR).mkdir(exist_ok=True)
    written = {**{f"{DIR}/{f}": text for f, text in pair.items()}, **result}
    for f, text in written.items():
        pathlib.Path(f).write_bytes(text.encode("utf-8"))
    git("add", "--", *written)
    total = len(got["en"])
    print(f"changelog_fragment: moved {total} entr{'y' if total == 1 else 'ies'} to {DIR}/{name}.md and {DIR}/{name}.ar.md"
          + ("" if merging else " (staged: commit them with the two CHANGELOGs)"))  # fmt: skip
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description="Changelog fragments (changelog.d/README.md).")
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("check", help="the gate: every fragment, and no change of [Unreleased]")
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
