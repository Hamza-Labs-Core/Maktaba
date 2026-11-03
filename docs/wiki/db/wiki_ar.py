"""The wiki in Arabic: the translation store, its lookup and the drift check.

Used by generate_wiki.py. Every entry's title and content (and an epic's engine) has its
Modern Standard Arabic in docs/wiki/db/ar/: epic-NN.json for Epic NN, its stories and its
plans, common.json for the rest. Each text is keyed by entry id and field and records `hash`,
the first 16 hex digits of the SHA-256 of the English it translates:

    {"entries": {"story-1.1": {"title": {"hash": "…", "ar": "…"}, "content": {…}}}}

The drift check fails when an English text has no Arabic, when its English changed since it
was translated, when the Arabic drops or changes a piece of code (a code span or block stays
as written), or when the Arabic is mostly Latin prose. Each finding names the file, the entry
and the field. Python 3.10+, standard library only.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections import defaultdict
from collections.abc import Callable
from pathlib import Path

DESCRIPTION_AR = ("منصة ذكاء وسائط تستضيفها بنفسك: بديل كامل لـ Plex، ونصوص مفرّغة يمكن البحث "
                  "في كل كلمة منها. انظر specs/architecture.md.")

# The fields of an entry the wiki shows as text, per type. An endpoint's title and an entity's
# are code (`GET /api/x`, a table's name) and stay as written.
FIELDS: dict[str, tuple[str, ...]] = {
    "epic": ("title", "content", "engine"),
    "endpoint": ("content",),
    "entity": ("content",),
}
DEFAULT_FIELDS = ("title", "content")

# An Arabic text fails when more than this share of its prose letters (outside code, URLs,
# paths and identifiers) are Latin: the devportal's MAX_LATIN_SHARE
# (web/packages/devportal/scripts/parity.mjs). An entry is judged alone once its Arabic has
# MIN_LETTERS prose letters (a title of two words and a product name is a fair 40% Latin),
# and every store file is judged as a whole, as a page is.
MAX_LATIN_SHARE = 0.2
MIN_LETTERS = 200

ARABIC_RE = re.compile("[؀-ۿݐ-ݿࢠ-ࣿﭐ-﷿ﹰ-﻿]")
LATIN_RE = re.compile(r"[A-Za-z]")
FENCE_RE = re.compile(r"^[ \t]*```[^\n]*\n[\s\S]*?^[ \t]*```", re.MULTILINE)
# an inline code span, which may wrap onto the next line (markdown's rule: not past a blank line)
CODE_RE = re.compile(r"`(?:[^`\n]|\n(?![ \t]*\n))+`")

TODO_HINT = "python3 docs/wiki/db/generate_wiki.py --ar-todo lists the English and its hash"


def text_hash(s: str) -> str:
    """The hash a translation records of the English it translates."""
    return hashlib.sha256(s.encode("utf-8")).hexdigest()[:16]


def prose(text: str) -> str:
    """Text without its code, URLs, link targets, paths and identifiers (parity.mjs's
    markdownProse and htmlProse together)."""
    t = FENCE_RE.sub(" ", text)
    t = re.sub(r"^[ \t]*```[\s\S]*\Z", " ", t, flags=re.MULTILINE)  # a fence cut open
    t = CODE_RE.sub(" ", t)
    t = re.sub(r"`[^`]*\Z", " ", t)  # a code span the excerpt cut open
    t = re.sub(r"\]\([^)]*\)", "]", t)
    t = re.sub(r"https?://\S+", " ", t)
    t = re.sub(r"<[^>]+>", " ", t)
    return re.sub(r"\S*[/_.]\S*[A-Za-z]\S*", " ", t)


def letters(text: str) -> tuple[int, int]:
    """(Latin, Arabic) letters in `text`'s prose."""
    p = prose(text)
    return len(LATIN_RE.findall(p)), len(ARABIC_RE.findall(p))


def latin_share(text: str) -> float:
    """The Latin share of the letters in `text`'s prose: 0 when it has none."""
    latin, arabic = letters(text)
    return 0.0 if latin + arabic == 0 else latin / (latin + arabic)


def needs_arabic(en: str) -> bool:
    """English with prose to translate: not code alone (`GET /api/x`, a table's name)."""
    return bool(en) and bool(LATIN_RE.search(prose(en)))


def code_pieces(text: str) -> list[str]:
    """The code a translation keeps as written: fenced blocks and inline code spans."""
    fences = [m.group(0).strip() for m in FENCE_RE.finditer(text)]
    return fences + CODE_RE.findall(FENCE_RE.sub(" ", text))


def missing_code(en: str, ar: str) -> list[str]:
    """The English's code pieces the Arabic does not carry as written."""
    have = code_pieces(ar)
    out = []
    for c in code_pieces(en):
        if c in have:
            have.remove(c)
        else:
            out.append(c)
    return out


def store_file(entry: dict) -> str:
    """The store file an entry's Arabic is in: its epic's own file for an epic, its stories
    and its plans; common.json for the rest."""
    if entry["type"] in ("epic", "story", "plan") and entry.get("epic"):
        return f"epic-{entry['epic'][:2]}.json"
    return "common.json"


def store_path(name: str) -> str:
    return f"docs/wiki/db/ar/{name}"


def load_store(root: Path) -> dict[str, dict]:
    """The translation store: file name -> {entry id: {field: {"hash", "ar"}}}."""
    out: dict[str, dict] = {}
    for f in sorted(root.glob("*.json")):
        out[f.name] = json.loads(f.read_text(encoding="utf-8") or "{}").get("entries", {})
    return out


def openapi_pointer(path: str, method: str) -> str:
    """The JSON pointer of an operation's summary in openapi.json (and openapi.ar.json)."""
    part = path.replace("~", "~0").replace("/", "~1")
    return f"/paths/{part}/{method.lower()}/summary"


ENTITY_EN = re.compile(r"^Database table `(\w+)`\. Source: (.+)\.$")


class Translated:
    def __init__(self) -> None:
        self.entries: list[dict] = []
        self.findings: list[str] = []
        # what needs translating: store file -> {id: {field: {"hash", "en", ...}}}
        self.todo: dict[str, dict[str, dict]] = defaultdict(dict)
        self.translated = 0  # entries with every text in Arabic


def lookup(entry: dict, field: str, en: str, store: dict[str, dict],
           openapi_ar: dict[str, dict]) -> tuple[str | None, str]:
    """A text's Arabic, and where it is from: openapi.ar.json for an endpoint's summary it
    translates, a fixed sentence for a plain entity, else the store ("store")."""
    if entry["type"] == "endpoint" and field == "content":
        md = entry.get("metadata") or {}
        s = openapi_ar.get(openapi_pointer(md.get("path", ""), md.get("method", "")))
        if s and s.get("en") == en and s.get("ar"):
            return s["ar"], "openapi"
    if entry["type"] == "entity" and field == "content":
        m = ENTITY_EN.match(en)
        if m:
            return f"جدول قاعدة البيانات `{m.group(1)}`. المصدر: {m.group(2)}.", "fixed"
    t = store.get(store_file(entry), {}).get(entry["id"], {}).get(field)
    return (t.get("ar") if isinstance(t, dict) else None), "store"


def translate(entries: list[dict], store: dict[str, dict], openapi_ar: dict[str, dict],
              scrub: Callable[[str], str] = lambda s: s) -> Translated:
    """Each entry with its text in Arabic, and the drift check's findings.

    An entry's Arabic records `i18n.source`: field -> the hash of the English it translates.
    A field with no Arabic, or whose English changed since it was translated, stays in English
    in the Arabic database (the page still shows something) and is a finding."""
    res = Translated()
    used: set[tuple[str, str, str]] = set()
    by_file: dict[str, list[str]] = defaultdict(list)
    for e in entries:
        fname = store_file(e)
        where = f"{store_path(fname)}: {e['id']}"
        out = dict(e)
        source: dict[str, str] = {}
        texts: list[str] = []
        complete = True
        for field in FIELDS.get(e["type"], DEFAULT_FIELDS):
            en = e.get(field)
            if not isinstance(en, str) or not needs_arabic(en):
                continue
            h = text_hash(en)
            ar, origin = lookup(e, field, en, store, openapi_ar)
            if origin == "store":
                used.add((fname, e["id"], field))
                t = store.get(fname, {}).get(e["id"], {}).get(field)
                todo = {"hash": h, "en": en}
                if not ar:
                    res.findings.append(f"{where} {field}: no Arabic; translate it ({TODO_HINT})")
                    res.todo[fname].setdefault(e["id"], {})[field] = todo
                    complete = False
                    continue
                if t.get("hash") != h:
                    res.findings.append(
                        f"{where} {field}: the English changed since it was translated; "
                        f"translate it again and record hash {h} ({TODO_HINT})")
                    res.todo[fname].setdefault(e["id"], {})[field] = {**todo, "ar_before": ar}
                    complete = False
                    continue
                if not ARABIC_RE.search(ar):
                    res.findings.append(f"{where} {field}: the Arabic is not Arabic")
                    complete = False
                    continue
                lost = missing_code(en, ar)
                if lost:
                    res.findings.append(
                        f"{where} {field}: the Arabic drops or changes code the English has; "
                        f"keep it as written: {', '.join(lost[:3])}")
                    complete = False
            out[field] = scrub(ar)
            source[field] = h
            texts.append(ar)
        if source:
            out["i18n"] = {"lang": "ar", "source": source}
        if complete:
            res.translated += 1
        joined = "\n\n".join(texts)
        latin, arabic = letters(joined)
        if latin + arabic >= MIN_LETTERS and latin / (latin + arabic) > MAX_LATIN_SHARE:
            res.findings.append(
                f"{where}: {round(100 * latin / (latin + arabic))}% of its Arabic prose is Latin "
                "script (translate the prose; code, paths and names stay as written)")
        by_file[fname].append(joined)
        res.entries.append(out)
    for fname, texts in sorted(by_file.items()):
        joined = "\n\n".join(t for t in texts if t)
        if joined and latin_share(joined) > MAX_LATIN_SHARE:
            res.findings.append(
                f"{store_path(fname)}: {round(latin_share(joined) * 100)}% of its Arabic prose "
                "is Latin script")
    for fname, ents in sorted(store.items()):
        for eid, fields in ents.items():
            for field in sorted(fields):
                if (fname, eid, field) not in used:
                    res.findings.append(
                        f"{store_path(fname)}: {eid} {field} is not a text the wiki shows "
                        "(the entry or field is gone, or belongs in another file): remove it")
    return res


def cross_refs(xref: dict, ar_by_id: dict[str, dict]) -> dict:
    """The cross-reference inventory with its epics' and stories' text in Arabic."""
    out = json.loads(json.dumps(xref))
    out["lang"] = "ar"
    for ep in out.get("epics", []):
        e = ar_by_id.get(f"epic-{ep['id']}-{ep['slug']}")
        if e:
            ep["title"] = e["title"]
            ep["description"] = e.get("content") or ep["description"]
    for st in out.get("stories", []):
        num = str(st["num"])
        e = ar_by_id.get(f"story-{st['epic']}.{int(num)}" if num.isdigit() else f"story-{st['id']}")
        if e:
            st["title"] = e["title"]
    return out
