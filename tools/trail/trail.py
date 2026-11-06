#!/usr/bin/env python3
"""The Claude trail (v2): events, snapshots and the missing-events list.

    docs/trail/events/      one file per piece of work, written by the session that did it
    docs/trail/snapshots/   vX.Y.Z.md, one per tag, built by an agent from the last snapshot
                            plus the events since
    docs/trail/current.md   a copy of the newest snapshot

Commands:
    check                   validate every event and snapshot (the lint job runs this)
    missing [--ref R]       merged pull requests with no event, up to R (default HEAD)
    skeleton --tag vX.Y.Z   print a new snapshot: the last snapshot's lessons carried forward,
                            the events since it to fold in, and the computed Index and Missing
    index [--ref R]         print the Index table for the events at R

Standard library only; git is the only outside program. Read docs/trail/README.md for the
format and .claude/skills/trail/SKILL.md for when to write what.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TRAIL = "docs/trail"
EVENTS = f"{TRAIL}/events"
SNAPSHOTS = f"{TRAIL}/snapshots"
CURRENT = f"{TRAIL}/current.md"
# Pull requests up to this number are covered by the frozen docs/claude-trail.md (and the
# migrated events); from the next one on, a merged pull request with no event is Missing.
MISSING_AFTER = 653

KINDS = {"pr", "incident", "decision", "correction", "release", "migration"}
SECTIONS = ["What happened", "What we learned", "Keep doing", "Stop doing", "Open"]
REQUIRED_KEYS = {"schema", "kind", "title", "date", "session"}
OPTIONAL_KEYS = {"pr", "issue", "prs", "corrects", "source"}
SNAPSHOT_KEYS = {"schema", "tag", "up_to_commit", "built_from"}
SNAPSHOT_SECTIONS = ["Keep doing", "Stop doing", "What we learned", "Open", "Missing events", "Index"]
CITED_SECTIONS = {"Keep doing", "Stop doing", "What we learned", "Open"}

FILE_RE = re.compile(
    r"^(?:(?P<pr>\d{4,5})|i(?P<issue>\d{4,5})|d(?P<day>\d{8}))-(?P<slug>[a-z0-9]+(?:-[a-z0-9]+)*)\.md$"
)
DATE_RE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
TAG_RE = re.compile(r"^v\d+\.\d+\.\d+$")
SHA_RE = re.compile(r"^[0-9a-f]{7,40}$")
# `#634` cites a pull request's events; `issue #649` names an issue and cites nothing
CITE_RE = re.compile(r"(?<![\w/])(?:(?<![Ii]ssue )#(\d+)|\b(i\d+)\b|\b(d\d{8}-[a-z0-9-]+)\b)")
MERGE_RE = re.compile(r"^Merge pull request #(\d+) ")

# Patterns that must never appear in trail text (it is read by every session, and leaves
# this machine with the repository). Not a full secret scanner: the shapes that have
# mattered here.
SECRET_RES = [
    (re.compile(r"\bgh[pousr]_[A-Za-z0-9]{30,}"), "a GitHub token"),
    (re.compile(r"\bgithub_pat_[A-Za-z0-9_]{20,}"), "a GitHub token"),
    (re.compile(r"\bsk-ant-[A-Za-z0-9_-]{20,}"), "an Anthropic API key"),
    (re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b"), "an AWS access key id"),
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"), "a private key"),
    (re.compile(r"X-Plex-Token=[A-Za-z0-9_-]{8,}"), "a Plex token"),
    (re.compile(r"\bBearer\s+[A-Za-z0-9._~+/-]{20,}"), "a bearer token"),
    (re.compile(r"\b[a-z][a-z0-9+.-]*://[^:/@\s]*:[^@/\s]+@"), "a URL with a password"),
]
# `password=…`, `"secret": "…"`, `MAKTABA_PG_PASSWORD=…`: the key, then a value of 8 or more
# characters. The key may end a longer name (POSTGRES_PASSWORD, client_secret), so it is not
# preceded by a letter rather than by a word boundary.
CREDENTIAL_RE = re.compile(
    r"(?i)(?:^|[^a-z])(?:password|passwd|secret|token|api[_-]?key)[\"']?\s*[:=]\s*(?P<q>[\"']?)(?P<v>[^\s\"'`]{8,})"
)
# CLAUDE.md rule: never quote or attribute the owner's words in repo text.
OWNER_RE = re.compile(r"\((?:the )?owner,\s*\d{4}-\d{2}-\d{2}\)|\bthe owner (?:said|wrote|asked)\s*[:,]?\s*[\"“]")


class TrailError(Exception):
    pass


@dataclass
class Doc:
    path: str
    meta: dict[str, object]
    sections: dict[str, str]
    order: list[str]
    body: str


@dataclass
class Event:
    path: str
    stem: str
    kind: str
    title: str
    date: str
    pr: int | None = None
    issue: int | None = None
    prs: list[int] = field(default_factory=list)

    @property
    def cites(self) -> set[str]:
        out = {self.stem}
        if self.pr is not None:
            out.add(f"#{self.pr}")
        out |= {f"#{p}" for p in self.prs}
        if self.issue is not None:
            out.add(f"i{self.issue}")
        if self.stem.startswith("d"):
            out.add(self.stem)
        return out


# ---- parsing --------------------------------------------------------------------------


def _value(raw: str) -> object:
    raw = raw.strip()
    if raw.startswith("[") and raw.endswith("]"):
        return [_value(x) for x in raw[1:-1].split(",") if x.strip()]
    if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "'\"":
        return raw[1:-1]
    if re.fullmatch(r"-?\d+", raw):
        return int(raw)
    return raw


def parse(path: str, text: str) -> Doc:
    """Front matter (`key: value` lines between `---` fences) and `## ` sections."""
    if not text.startswith("---\n"):
        raise TrailError(f"{path}: no front matter (the file must start with a --- line)")
    end = text.find("\n---\n", 4)
    if end < 0:
        raise TrailError(f"{path}: the front matter has no closing --- line")
    meta: dict[str, object] = {}
    for n, line in enumerate(text[4:end].splitlines(), start=2):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, sep, raw = line.partition(":")
        if not sep or not re.fullmatch(r"[a-z_]+", key.strip()):
            raise TrailError(f"{path}:{n}: front matter lines are `key: value`")
        if key.strip() in meta:
            raise TrailError(f"{path}:{n}: {key.strip()} appears twice")
        meta[key.strip()] = _value(raw)
    body = text[end + 5 :]
    sections: dict[str, str] = {}
    order: list[str] = []
    current: str | None = None
    lines: list[str] = []
    for line in body.splitlines():
        m = re.match(r"^## (.+?)\s*$", line)
        if m:
            if current is not None:
                sections[current] = "\n".join(lines).strip()
            current = m.group(1)
            if current in sections:
                raise TrailError(f"{path}: section `## {current}` appears twice")
            order.append(current)
            lines = []
        elif current is not None:
            lines.append(line)
        elif line.strip() and not line.startswith("# "):
            raise TrailError(f"{path}: text before the first `## ` section")
    if current is not None:
        sections[current] = "\n".join(lines).strip()
    return Doc(path, meta, sections, order, body)


def _credential(m: re.Match[str]) -> bool:
    """A credential's value, not prose about one (`password: regenerated`, `token=$TOKEN`).

    A quoted value is a value. An unquoted one is a value unless it reads as a word: letters
    only, maybe ending in punctuation, or a reference (`$X`, `<x>`, `${x}`, `%x%`)."""
    v = m.group("v")
    if v[0] in "$<{%":
        return False
    if m.group("q"):
        return True
    return not re.fullmatch(r"[A-Za-z][a-z]*(?:-[a-z]+)*[.,;:!?)]*", v)


def scan_text(path: str, text: str) -> list[str]:
    errs = []
    for rx, what in SECRET_RES:
        if rx.search(text):
            errs.append(f"{path}: looks like it holds {what}; never write secrets in the trail")
    if any(_credential(m) for m in CREDENTIAL_RE.finditer(text)):
        errs.append(f"{path}: looks like it holds a credential value; never write secrets in the trail")
    if OWNER_RE.search(text):
        errs.append(f"{path}: quotes or attributes the owner's words (CLAUDE.md rule 5); state it neutrally")
    return errs


def event_from(path: str, text: str) -> tuple[Event | None, list[str]]:
    name = Path(path).name
    m = FILE_RE.match(name)
    if not m:
        return None, [
            f"{path}: an event is named NNNN-slug.md (its pull request), iNNNN-slug.md (an issue) "
            "or dYYYYMMDD-slug.md (neither); the slug is lowercase words joined by -"
        ]
    try:
        doc = parse(path, text)
    except TrailError as e:
        return None, [str(e)]
    errs = scan_text(path, text)
    meta = doc.meta
    missing = REQUIRED_KEYS - set(meta)
    if missing:
        errs.append(f"{path}: front matter lacks {sorted(missing)}")
    unknown = set(meta) - REQUIRED_KEYS - OPTIONAL_KEYS
    if unknown:
        errs.append(f"{path}: unknown front matter keys {sorted(unknown)}")
    if meta.get("schema") != 1:
        errs.append(f"{path}: schema must be 1")
    kind = str(meta.get("kind", ""))
    if kind not in KINDS:
        errs.append(f"{path}: kind must be one of {sorted(KINDS)}")
    date = str(meta.get("date", ""))
    if not DATE_RE.match(date):
        errs.append(f"{path}: date must be YYYY-MM-DD")
    if not str(meta.get("title", "")).strip():
        errs.append(f"{path}: title is empty")
    if not str(meta.get("session", "")).strip():
        errs.append(f"{path}: session is empty (the session id, or `unknown` for a migrated entry)")
    ev = Event(path=path, stem=name[:-3], kind=kind, title=str(meta.get("title", "")), date=date)
    if m.group("pr"):
        if meta.get("pr") != int(m.group("pr")):
            errs.append(f"{path}: the file is named for pull request {int(m.group('pr'))} but pr is {meta.get('pr')!r}")
        ev.pr = int(m.group("pr"))
    elif "pr" in meta:
        errs.append(f"{path}: pr is set, so name the file NNNN-slug.md")
    if m.group("issue"):
        if meta.get("issue") != int(m.group("issue")):
            errs.append(f"{path}: the file is named for issue {int(m.group('issue'))} but issue is {meta.get('issue')!r}")
        ev.issue = int(m.group("issue"))
    elif "issue" in meta:
        # an issue the work also belongs to (a PR event that closes an issue, say)
        if isinstance(meta["issue"], int):
            ev.issue = meta["issue"]
        else:
            errs.append(f"{path}: issue must be an issue number")
    if m.group("day") and m.group("day") != date.replace("-", ""):
        errs.append(f"{path}: the file is named for {m.group('day')} but date is {date}")
    prs = meta.get("prs", [])
    if not isinstance(prs, list) or not all(isinstance(p, int) for p in prs):
        errs.append(f"{path}: prs must be a list of pull request numbers, like [608, 635]")
    else:
        ev.prs = list(prs)
    if kind == "pr" and ev.pr is None:
        errs.append(f"{path}: kind pr needs a NNNN-slug.md name and pr")
    if kind == "correction" and not meta.get("corrects"):
        errs.append(f"{path}: kind correction needs corrects: <the event file it corrects, without .md>")
    bad = [s for s in doc.order if s not in SECTIONS]
    if bad:
        errs.append(f"{path}: unknown sections {bad}; use {SECTIONS}")
    if not doc.sections.get("What happened"):
        errs.append(f"{path}: `## What happened` is required and must not be empty")
    known = [s for s in doc.order if s in SECTIONS]
    if known != [s for s in SECTIONS if s in known]:
        errs.append(f"{path}: sections must come in the order {SECTIONS}")
    return ev, errs


# ---- git ------------------------------------------------------------------------------


def git(*args: str, cwd: Path = ROOT) -> str:
    r = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True)
    if r.returncode != 0:
        raise TrailError(f"git {' '.join(args)}: {r.stderr.strip()}")
    return r.stdout


def files_at(ref: str | None, prefix: str, cwd: Path = ROOT) -> dict[str, str]:
    """{path: text} under prefix, at a commit, or in the working tree when ref is None."""
    out: dict[str, str] = {}
    if ref is None:
        base = cwd / prefix
        if base.is_dir():
            for p in sorted(base.rglob("*.md")):
                out[p.relative_to(cwd).as_posix()] = p.read_text(encoding="utf-8")
        return out
    try:
        names = git("ls-tree", "-r", "--name-only", ref, "--", prefix, cwd=cwd).split()
    except TrailError:
        return out
    for n in names:
        if n.endswith(".md"):
            out[n] = git("show", f"{ref}:{n}", cwd=cwd)
    return out


def merged_prs(ref: str, cwd: Path = ROOT) -> list[tuple[int, str, str, str]]:
    """(number, merge sha, date, title) for each pull request merged into ref's first-parent line."""
    log = git("log", "--first-parent", "--merges", "--format=%H%x09%cs%x09%s%x09%b%x1e", ref, cwd=cwd)
    out = []
    for rec in log.split("\x1e"):
        rec = rec.strip("\n")
        if not rec:
            continue
        sha, day, subject, body = (rec.split("\t", 3) + ["", "", "", ""])[:4]
        m = MERGE_RE.match(subject)
        if m:
            title = body.strip().splitlines()[0] if body.strip() else subject
            out.append((int(m.group(1)), sha, day, title))
    return out


def trail_only(sha: str, cwd: Path = ROOT) -> bool:
    """A merge that changed only the trail (events, snapshots or the frozen file)."""
    files = git("diff", "--name-only", f"{sha}^1", sha, cwd=cwd).split()
    return bool(files) and all(f.startswith(f"{TRAIL}/") or f == "docs/claude-trail.md" for f in files)


# ---- events, index, missing -----------------------------------------------------------


def load_events(ref: str | None, cwd: Path = ROOT) -> tuple[list[Event], list[str]]:
    events: list[Event] = []
    errs: list[str] = []
    for path, text in files_at(ref, EVENTS, cwd).items():
        if Path(path).name.lower() == "readme.md":
            continue
        ev, e = event_from(path, text)
        errs += e
        if ev:
            events.append(ev)
    stems = [e.stem for e in events]
    for s in sorted({s for s in stems if stems.count(s) > 1}):
        errs.append(f"{EVENTS}/{s}.md: two events share this name")
    by_stem = {e.stem for e in events}
    for path, text in files_at(ref, EVENTS, cwd).items():
        m = re.search(r"^corrects:\s*(\S+)\s*$", text, re.M)
        if m and m.group(1) not in by_stem:
            errs.append(f"{path}: corrects {m.group(1)}, which is no event")
    return sorted(events, key=lambda e: (e.date, e.stem)), errs


def index_table(events: list[Event]) -> str:
    rows = ["| Event | Date | Kind | Title |", "| --- | --- | --- | --- |"]
    for e in events:
        rows.append(f"| {e.stem} | {e.date} | {e.kind} | {e.title.replace('|', '/')} |")
    return "\n".join(rows)


def missing(ref: str, events: list[Event], cwd: Path = ROOT) -> list[tuple[int, str, str]]:
    covered = {p for e in events for p in ([e.pr] if e.pr else []) + e.prs}
    out = []
    for n, sha, day, title in merged_prs(ref, cwd):
        if n > MISSING_AFTER and n not in covered and not trail_only(sha, cwd):
            out.append((n, day, title))
    return sorted(out)


def missing_table(rows: list[tuple[int, str, str]]) -> str:
    if not rows:
        return "None."
    out = ["| PR | Merged | Title |", "| --- | --- | --- |"]
    out += [f"| #{n} | {day} | {title.replace('|', '/')} |" for n, day, title in rows]
    return "\n".join(out)


# ---- snapshots ------------------------------------------------------------------------


def check_snapshot(path: str, text: str, cwd: Path = ROOT, verify_git: bool = True) -> list[str]:
    try:
        doc = parse(path, text)
    except TrailError as e:
        return [str(e)]
    errs = scan_text(path, text)
    meta = doc.meta
    if set(meta) != SNAPSHOT_KEYS:
        errs.append(f"{path}: snapshot front matter is exactly {sorted(SNAPSHOT_KEYS)}")
    if meta.get("schema") != 1:
        errs.append(f"{path}: schema must be 1")
    tag = str(meta.get("tag", ""))
    if not TAG_RE.match(tag):
        errs.append(f"{path}: tag must be vX.Y.Z")
    if path != CURRENT and Path(path).name != f"{tag}.md":
        errs.append(f"{path}: a snapshot is named for its tag ({tag}.md)")
    built = str(meta.get("built_from", ""))
    if built != "full" and not TAG_RE.match(built):
        errs.append(f"{path}: built_from is the previous snapshot's tag, or full for a rebuild from every event")
    if doc.order != SNAPSHOT_SECTIONS:
        errs.append(f"{path}: a snapshot has exactly the sections {SNAPSHOT_SECTIONS}, in that order")
    sha = str(meta.get("up_to_commit", ""))
    if not SHA_RE.match(sha):
        errs.append(f"{path}: up_to_commit must be a commit sha")
        return errs
    if not verify_git:
        return errs
    try:
        full = git("rev-parse", "--verify", f"{sha}^{{commit}}", cwd=cwd).strip()
    except TrailError:
        return errs + [f"{path}: up_to_commit {sha} is no commit here (fetch with history)"]
    try:
        tagged = git("rev-parse", "--verify", f"{tag}^{{commit}}", cwd=cwd).strip()
        if tagged != full:
            errs.append(f"{path}: up_to_commit {sha} is not {tag}'s commit ({tagged[:9]})")
    except TrailError:
        errs.append(f"{path}: tag {tag} is not here (fetch tags)")
    events, e = load_events(full, cwd)
    errs += [f"{path}: the events at {sha} do not check: {x}" for x in e[:3]]
    cites = set().union(*(ev.cites for ev in events)) if events else set()
    for name in CITED_SECTIONS:
        for line in doc.sections.get(name, "").splitlines():
            if not line.lstrip().startswith("- "):
                continue
            found = [f"#{n}" if n else (i or d) for n, i, d in CITE_RE.findall(line)]
            if not found:
                errs.append(f"{path}: `## {name}` item cites no event: {line.strip()[:80]}")
            for c in found:
                if c not in cites:
                    errs.append(f"{path}: `## {name}` cites {c}, which no event at {sha} has")
    if doc.sections.get("Index", "") != index_table(events):
        errs.append(f"{path}: `## Index` is not the events at {sha}; print it with: trail.py index --ref {sha}")
    if doc.sections.get("Missing events", "") != missing_table(missing(full, events, cwd)):
        errs.append(f"{path}: `## Missing events` is not what git says; print it with: trail.py missing --ref {sha}")
    return errs


def _version(tag: str) -> tuple[int, ...]:
    return tuple(int(x) for x in tag[1:].split("."))


def snapshots_at(cwd: Path = ROOT) -> tuple[dict[str, str], list[str]]:
    """The snapshots ({path: text}, named vX.Y.Z.md) and an error for each other file there."""
    snaps: dict[str, str] = {}
    errs: list[str] = []
    for path, text in files_at(None, SNAPSHOTS, cwd).items():
        if TAG_RE.match(Path(path).stem) and Path(path).parent.as_posix().endswith(SNAPSHOTS):
            snaps[path] = text
        else:
            errs.append(f"{path}: {SNAPSHOTS}/ holds only snapshots, named vX.Y.Z.md")
    return snaps, errs


def check(cwd: Path = ROOT, verify_git: bool = True) -> list[str]:
    events, errs = load_events(None, cwd)
    snaps, e = snapshots_at(cwd)
    errs += e
    for path, text in snaps.items():
        errs += check_snapshot(path, text, cwd, verify_git)
    cur = cwd / CURRENT
    if snaps:
        newest = max(snaps, key=lambda p: _version(Path(p).stem))
        if not cur.is_file():
            errs.append(f"{CURRENT}: missing; it is a copy of {newest}")
        elif cur.read_text(encoding="utf-8") != snaps[newest]:
            errs.append(f"{CURRENT}: is not a copy of the newest snapshot, {newest}")
    return errs


def skeleton(tag: str, cwd: Path = ROOT) -> str:
    """A new snapshot to finish by hand: carried-forward lessons plus the events to fold in."""
    sha = git("rev-parse", f"{tag}^{{commit}}", cwd=cwd).strip()
    if not TAG_RE.match(tag):
        raise TrailError(f"--tag {tag}: a tag is vX.Y.Z")
    snaps, _ = snapshots_at(cwd)
    # the newest snapshot older than this tag: a snapshot for a skipped or older tag builds on
    # the one before it, not on a newer one
    older = [p for p in snaps if _version(Path(p).stem) < _version(tag)]
    prev = None
    if older:
        prev_path = max(older, key=lambda p: _version(Path(p).stem))
        prev = parse(prev_path, snaps[prev_path])
    events, _ = load_events(sha, cwd)
    since: list[Event] = events
    if prev:
        old, _ = load_events(str(prev.meta["up_to_commit"]), cwd)
        seen = {e.stem for e in old}
        since = [e for e in events if e.stem not in seen]
    head = [
        "---",
        "schema: 1",
        f"tag: {tag}",
        f"up_to_commit: {sha[:9]}",
        f"built_from: {prev.meta['tag'] if prev else 'full'}",
        "---",
        "",
        f"# Trail snapshot: {tag}",
        "",
        "<!-- Fold each new event below into the sections, citing it, then delete this comment.",
        *[f"     new: {e.path} ({e.title})" for e in since],
        "-->",
        "",
    ]
    body = []
    for name in ["Keep doing", "Stop doing", "What we learned", "Open"]:
        body += [f"## {name}", "", (prev.sections.get(name, "") if prev else ""), ""]
    body += ["## Missing events", "", missing_table(missing(sha, events, cwd)), ""]
    body += ["## Index", "", index_table(events), ""]
    return "\n".join(head + body)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("check")
    m = sub.add_parser("missing")
    m.add_argument("--ref", default="HEAD")
    i = sub.add_parser("index")
    i.add_argument("--ref", default=None)
    s = sub.add_parser("skeleton")
    s.add_argument("--tag", required=True)
    a = ap.parse_args(argv)
    try:
        if a.cmd == "check":
            errs = check()
            for e in errs:
                print(e, file=sys.stderr)
            print(f"trail: {'ok' if not errs else f'{len(errs)} problem(s)'}")
            return 1 if errs else 0
        if a.cmd == "missing":
            events, _ = load_events(a.ref)
            print(missing_table(missing(a.ref, events)))
        elif a.cmd == "index":
            events, _ = load_events(a.ref)
            print(index_table(events))
        elif a.cmd == "skeleton":
            print(skeleton(a.tag))
    except TrailError as e:
        print(f"trail: {e}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
