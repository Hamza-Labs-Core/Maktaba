"""Roku's catalogs from i18n/{en,ar}.json, the Arabic shaped at build time (story 58.15).

Roku draws text left to right with no Arabic shaping (research.md §6.4), so the channel's static
Arabic strings are turned into visual strings here, with the reference (`visual.py`), and the
channel shows them as they are. Each key's label size is in en.json's metadata:

    "@roku.notAvailable": {"widthChars": 60, "lines": 2},
    "roku.notAvailable": "That video isn't available.",

The build fails, naming the key, when a string (English or Arabic) does not fit its label:
wrapped to `widthChars` it needs more than `lines` lines, or it holds a word longer than the
width. A key without metadata fails too: every label has a size.

Arabic lines are broken before reordering (the first line holds the start of the sentence) and
each line is reordered with the paragraph direction right to left; they are joined with "\\n"
for a Label with `wrap` on. English is left to the Label's own wrapping.

A string with a placeholder (`%1$s`, `%1$d`) cannot be shaped before its value is known: it is
kept in logical order as a template, with its label size, and the channel's `I18n.Format`
fills it in and shapes it on the device (`VisualLines`, source/visualtext.brs).

It writes, for each language (en_US, ar_SA):

    locale/<lang>/strings.json      what source/i18n.brs reads: {"lang", "rtl", "strings",
                                    "templates"} (the channel's own language switch)
    locale/<lang>/translations.ts   Roku's catalog format (tr() by key), the static strings

    shape_strings.py [--check]      # from the repository root, with requirements.txt

`--check` fails when a generated file is stale (CI runs it after the catalog check).
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any
from xml.sax.saxutils import escape

sys.path.insert(0, str(Path(__file__).resolve().parent))

import visual  # noqa: E402

ROKU = Path(__file__).resolve().parents[1]
LANGS = {"en": ("en_US", False), "ar": ("ar_SA", True)}
PLACEHOLDER = re.compile(r"%(?:\d+\$)?[sd]")


class Unfit(Exception):
    pass


def size(meta: dict[str, Any], key: str) -> tuple[int, int]:
    m = meta.get("@" + key)
    if not isinstance(m, dict) or not isinstance(m.get("widthChars"), int) or m["widthChars"] < 1:
        raise Unfit(f'{key!r} has no label size: add "@{key}": {{"widthChars": N, "lines": N}} to en.json')
    lines = m.get("lines", 1)
    if not isinstance(lines, int) or lines < 1:
        raise Unfit(f"{key!r}: lines must be a positive number")
    return m["widthChars"], lines


def fit(key: str, lang: str, text: str, width: int, lines: int) -> list[str]:
    """The text's lines in logical order, or Unfit naming the key."""
    logical = visual.strip_harakat(visual.normalise(text))
    long_words = [w for w in logical.split(" ") if len(w) > width]
    if long_words:
        raise Unfit(f"{lang}.json: {key!r} has a word longer than its label ({width} characters): {long_words[0]!r}")
    wrapped = visual.wrap(logical, width)
    if len(wrapped) > lines:
        raise Unfit(f"{lang}.json: {key!r} needs {len(wrapped)} lines of {width} characters; its label has {lines}")
    return wrapped


def shape_catalog(lang: str, catalog: dict[str, str], meta: dict[str, Any]) -> tuple[dict[str, Any], list[str]]:
    """(strings.json's object, problems) for one language."""
    _, rtl = LANGS[lang]
    strings: dict[str, str] = {}
    templates: dict[str, dict[str, Any]] = {}
    problems: list[str] = []
    for key, text in catalog.items():
        if key.startswith("@"):
            continue
        try:
            width, lines = size(meta, key)
            if PLACEHOLDER.search(text):
                templates[key] = {"text": text, "widthChars": width, "lines": lines}
                continue
            wrapped = fit(key, lang, text, width, lines)
        except Unfit as e:
            problems.append(str(e))
            continue
        if rtl:
            strings[key] = "\n".join(visual.visual_lines(text, width, lines, "rtl"))
            assert len(strings[key].split("\n")) == len(wrapped)
        else:
            strings[key] = text
    return {"lang": lang, "rtl": rtl, "strings": strings, "templates": templates}, problems


def translations_ts(locale: str, strings: dict[str, str]) -> str:
    out = [
        '<?xml version="1.0" encoding="UTF-8"?>',
        "<!DOCTYPE TS>",
        f'<TS version="2.0" language="{locale}">',
        "<context>",
        "    <name>default</name>",
    ]
    for key, text in strings.items():
        out += [
            "    <message>",
            f"        <source>{escape(key)}</source>",
            f"        <translation>{escape(text)}</translation>",
            "    </message>",
        ]
    out += ["</context>", "</TS>"]
    return "\n".join(out) + "\n"


def load(path: Path) -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: not an object")
    return data


def build(root: Path = ROKU) -> tuple[dict[Path, str], list[str]]:
    """{generated file: content}, problems."""
    en = load(root / "i18n" / "en.json")
    files: dict[Path, str] = {}
    problems: list[str] = []
    for lang, (locale, _) in LANGS.items():
        catalog = en if lang == "en" else load(root / "i18n" / f"{lang}.json")
        doc, more = shape_catalog(lang, catalog, en)
        problems += more
        files[root / "locale" / locale / "strings.json"] = json.dumps(doc, ensure_ascii=False, indent=2) + "\n"
        files[root / "locale" / locale / "translations.ts"] = translations_ts(locale, doc["strings"])
    return files, problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--check", action="store_true", help="fail when a generated file is stale")
    ap.add_argument("--root", type=Path, default=ROKU, help="the channel's directory (apps/tv/roku)")
    args = ap.parse_args(argv)
    files, problems = build(args.root)
    for p in problems:
        print(f"::error::apps/tv/roku/i18n/{p}", file=sys.stderr)
    if problems:
        print(f"{len(problems)} string(s) do not fit their labels", file=sys.stderr)
        return 1
    stale = []
    for path, content in files.items():
        if args.check:
            if not path.exists() or path.read_text(encoding="utf-8") != content:
                stale.append(path)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")
            print(f"wrote {path.relative_to(args.root)}")
    if stale:
        for path in stale:
            print(
                f"::error::{path.relative_to(args.root)} is stale: run apps/tv/roku/tools/shape_strings.py",
                file=sys.stderr,
            )
        return 1
    if args.check:
        print("the Roku catalogs are up to date")
    return 0


if __name__ == "__main__":
    sys.exit(main())
