#!/usr/bin/env python3
"""The TV catalogs' check (story 58.15): English and Arabic, key for key.

Each catalog directory holds `en.json` and `ar.json`, one object of keys to strings. Keys may
be flat (`"home.title"`) or nested objects, read as dotted keys. A key starting with `@` is
metadata about the key after it (ARB's convention, e.g. Roku's label width in characters:
`"@home.title": {"widthChars": 24}`), not a string, and is skipped.

It fails, naming the catalog and the key, on:

- a key in English and missing (or empty) in Arabic, or the reverse;
- placeholders that differ between the two (`{name}`, `{{name}}`, `%s`, `%d`, `%1$s`,
  compared as multisets, so a placeholder used twice in one must be used twice in the other);
- an Arabic string with a Latin-only word that is not a product name (LATIN_ALLOWED) or code
  (a placeholder, a backticked span, a URL or host name, a token holding a digit such as `4K`
  or `S2`).

    check_tv_catalogs.py                 # the TV catalogs (CATALOGS), from the repository root
    check_tv_catalogs.py DIR...          # these catalog directories instead
    check_tv_catalogs.py --root R DIR... # DIRs relative to R

A catalog directory that does not exist yet is skipped (each platform's story adds its own
catalog and the check covers it from then on); one that exists without both files fails. The
Android and Apple catalogs keep their own checks (Android lint, check-string-catalogs.py).
It runs in the lint job's repository checks on every pull request, and in each TV job.
Standard library only.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from collections.abc import Iterable, Mapping, Sequence
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# The TV platforms' catalogs: /tv/ and its launcher (Samsung, LG), the Cast receiver, Roku.
CATALOGS: tuple[str, ...] = (
    "apps/tv/web/i18n",
    "web/packages/castreceiver/i18n",
    "apps/tv/roku/i18n",
)

# Latin words an Arabic string may hold as they are: product, platform and format names.
# Compared case-sensitively, as written in the product.
LATIN_ALLOWED: frozenset[str] = frozenset(
    {
        "Maktaba",
        "Roku",
        "Samsung",
        "Tizen",
        "LG",
        "webOS",
        "Fire",
        "TV",
        "Amazon",
        "Alexa",
        "Vega",
        "OS",
        "Google",
        "Cast",
        "Chromecast",
        "Android",
        "Apple",
        "AirPlay",
        "tvOS",
        "Plex",
        "HDR",
        "HDR10",
        "HLG",
        "Dolby",
        "Vision",
        "Atmos",
        "DTS",
        "HD",
        "UHD",
        "HLS",
        "MP4",
        "Wi-Fi",
        "PIN",
        "QR",
        "URL",
        "ID",
    }
)

_PLACEHOLDER = re.compile(r"\{\{\s*[\w.]+\s*\}\}|\{[\w.]+\}|%(?:\d+\$)?[sdif@]")
# Spans that are code, removed before looking for Latin words.
_CODE = re.compile(
    r"`[^`]*`"  # backticked code
    r"|\b[a-z][a-z0-9+.-]*://\S+"  # a URL
    r"|\b[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+\b(?:/\S*)?"  # a host or file name (example.org/path, en.json)
)
_LATIN_WORD = re.compile(r"[A-Za-z0-9][A-Za-z0-9'-]*")


def flatten(obj: Mapping[str, object], prefix: str = "") -> dict[str, object]:
    """A catalog's keys, nested objects read as dotted keys, metadata (`@...`) skipped."""
    out: dict[str, object] = {}
    for k, v in obj.items():
        if k.startswith("@"):
            continue
        key = f"{prefix}{k}"
        if isinstance(v, Mapping):
            out.update(flatten(v, key + "."))
        else:
            out[key] = v
    return out


def placeholders(text: str) -> Counter[str]:
    """The placeholders in a string, `{{ name }}` normalised to `{{name}}`."""
    return Counter(re.sub(r"\s+", "", m) for m in _PLACEHOLDER.findall(text))


def latin_words(text: str) -> list[str]:
    """The Latin-only words of an Arabic string that are neither product names nor code."""
    text = _CODE.sub(" ", _PLACEHOLDER.sub(" ", text))
    found: list[str] = []
    for word in _LATIN_WORD.findall(text):
        if any(c.isdigit() for c in word):  # 4K, S2, E5, 1080p, 12: codes and numbers, not words
            continue
        if word in LATIN_ALLOWED:
            continue
        found.append(word)
    return found


def _load(path: Path, rel: str) -> tuple[dict[str, object] | None, list[str]]:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return None, [f"{rel}: missing (a catalog has en.json and ar.json)"]
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as e:
        return None, [f"{rel}: not readable JSON ({e})"]
    if not isinstance(raw, dict):
        return None, [f"{rel}: not an object of keys to strings"]
    return flatten(raw), []


def check_catalog(directory: Path, label: str) -> list[str]:
    """The problems of one catalog directory, each naming the file or key."""
    en, problems = _load(directory / "en.json", f"{label}/en.json")
    ar, more = _load(directory / "ar.json", f"{label}/ar.json")
    problems += more
    if en is None or ar is None:
        return problems
    for lang, cat in (("en", en), ("ar", ar)):
        for key, value in cat.items():
            if not isinstance(value, str):
                problems.append(f"{label}/{lang}.json: {key!r} is not a string")
    for key in sorted(en.keys() - ar.keys()):
        problems.append(f"{label}: {key!r} is in en.json and missing in ar.json")
    for key in sorted(ar.keys() - en.keys()):
        problems.append(f"{label}: {key!r} is in ar.json and missing in en.json")
    for key in sorted(en.keys() & ar.keys()):
        e, a = en[key], ar[key]
        if not isinstance(e, str) or not isinstance(a, str):
            continue
        for lang, value in (("en", e), ("ar", a)):
            if not value.strip():
                problems.append(f"{label}/{lang}.json: {key!r} is empty")
        pe, pa = placeholders(e), placeholders(a)
        if pe != pa:
            problems.append(
                f"{label}: {key!r} placeholders differ: en {sorted(pe.elements())}, ar {sorted(pa.elements())}"
            )
        words = latin_words(a)
        if words:
            problems.append(
                f"{label}/ar.json: {key!r} has Latin words that are not product names or code: {', '.join(words)}"
            )
    return problems


def check(dirs: Iterable[str], root: Path = ROOT) -> tuple[list[str], list[str]]:
    """(problems, notes) over the catalog directories; a directory not there yet is a note."""
    problems: list[str] = []
    notes: list[str] = []
    for d in dirs:
        path = root / d
        if not path.is_dir():
            notes.append(f"{d}: not there yet, skipped")
            continue
        problems += check_catalog(path, d)
        notes.append(f"{d}: checked")
    return problems, notes


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("dirs", nargs="*", help="catalog directories (default: the TV catalogs)")
    ap.add_argument("--root", type=Path, default=ROOT, help="the directories are relative to it")
    args = ap.parse_args(argv)
    problems, notes = check(args.dirs or CATALOGS, args.root)
    for n in notes:
        print(n)
    for p in problems:
        print(f"::error::{p}", file=sys.stderr)
    if problems:
        print(f"{len(problems)} problem(s) in the TV catalogs", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
