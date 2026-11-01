#!/usr/bin/env python3
"""The Apple apps' String Catalogs are complete: every app speaks English and Arabic.

    tools/apple/check-string-catalogs.py                      # the catalogs alone
    tools/apple/check-string-catalogs.py --stringsdata <DerivedData>

Every key of every .xcstrings under apps/apple and apps/tv/tvos needs an Arabic ("ar")
translation in state "translated" (each plural or device variation too) with the key's
arguments, and a key that is not its own English (one added by hand, extractionState
"manual") its English as well. With --stringsdata, the .stringsdata Xcode writes as it
builds (SWIFT_EMIT_LOC_STRINGS) are read too: every key the code uses must be in its
target's catalog for its table (Localizable, or a screen's own, such as the Player's
Shell.xcstrings; a table with no catalog fails too), so a new Text("...") without its
Arabic fails here, not in a review.
"""
import argparse
import glob
import json
import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
CATALOG_DIRS = ["apps/apple", "apps/tv/tvos"]
LANGUAGES = ["ar"]
# a target's build folder (its .stringsdata) -> the folders of its sources. A table the code
# names (`Text("…", tableName: "Shell")`, `LocalizedStringResource(_:table:)`) is the catalog
# of that name in those folders (`Player/Shell/Shell.xcstrings`); the default table is
# Localizable. The Player's screens (Epic 48) each have their own table, and a new table
# needs no change here.
TARGETS = {
    "Maktaba Remote": ["apps/apple/Remote"],
    "Maktaba Remote iPad": ["apps/apple/Remote"],
    "Maktaba Player": ["apps/apple/Player"],
    "Maktaba Player iPad": ["apps/apple/Player"],
    "Maktaba Player Mac": ["apps/apple/Player"],
    "Maktaba TV": ["apps/tv/tvos/MaktabaTV"],
    "MaktabaKit-t": ["apps/apple/MaktabaKit/Sources/MaktabaKit"],
    "MaktabaKit": ["apps/apple/MaktabaKit/Sources/MaktabaKit"],
}
# catalogs that are not string tables of the code (the Info.plist's own keys)
NOT_TABLES = {"InfoPlist"}

SPEC = re.compile(r"%(?:(\d+)\$)?(@|ll[du]|l[du]|[du]|f|\.\d+f)")


def specs(s):
    """The format arguments of a string, in argument order: ["@", "lld"]."""
    found, n = {}, 0
    for m in SPEC.finditer(s.replace("%%", "")):
        n += 1
        found[int(m.group(1)) if m.group(1) else n] = m.group(2)
    return [found[i] for i in sorted(found)]


def units(loc):
    """Every stringUnit of a localization (its variations' too)."""
    if "stringUnit" in loc:
        yield loc["stringUnit"]
    for kinds in loc.get("variations", {}).values():
        for v in kinds.values():
            yield from units(v)
    for v in loc.get("substitutions", {}).values():
        yield from units(v)


def check_catalog(path, errors, warnings):
    rel = os.path.relpath(path, ROOT)
    try:
        doc = json.load(open(path, encoding="utf-8"))
    except (OSError, ValueError) as e:
        errors.append(f"{rel}: not a String Catalog: {e}")
        return set()
    source = doc.get("sourceLanguage", "en")
    strings = doc.get("strings", {})
    for key, entry in strings.items():
        if entry.get("shouldTranslate") is False:
            continue
        if entry.get("extractionState") == "stale":
            warnings.append(f"{rel}: {key!r} is no longer used (stale): remove it")
        locs = entry.get("localizations", {})
        english = [u.get("value", "") for u in units(locs.get(source, {}))]
        manual = entry.get("extractionState") in ("manual", "extracted_with_value")
        if manual and not english:
            errors.append(f"{rel}: {key!r} has no English ({source})")
        want = specs(english[-1] if english else key)
        for lang in LANGUAGES:
            us = list(units(locs.get(lang, {})))
            if not us:
                errors.append(f"{rel}: {key!r} has no {lang} translation")
                continue
            for u in us:
                if u.get("state") != "translated" or not u.get("value", "").strip():
                    errors.append(f"{rel}: {key!r}: {lang} is {u.get('state', 'missing')!r}, not 'translated'")
                elif specs(u["value"]) != want and len(us) == 1:
                    errors.append(f"{rel}: {key!r}: {lang} {u['value']!r} has arguments {specs(u['value'])}, the key {want}")
    return set(strings)


def tables(target, errors):
    """A target's string tables: {"Localizable": path, "Shell": path, …}."""
    found = {}
    for d in TARGETS[target]:
        for path in sorted(glob.glob(os.path.join(ROOT, d, "**", "*.xcstrings"), recursive=True)):
            if "/build/" in path or "/DerivedData/" in path:
                continue
            name = os.path.basename(path)[: -len(".xcstrings")]
            if name in NOT_TABLES:
                continue
            if name in found:
                errors.append(f"{target}: two catalogs named {name}.xcstrings: "
                              f"{os.path.relpath(found[name], ROOT)} and {os.path.relpath(path, ROOT)}")
                continue
            found[name] = path
    return found


def check_code(dd, catalogs, errors):
    """Every key a target's code uses is in the catalog of its table."""
    seen = 0
    for build in glob.glob(os.path.join(dd, "Build", "Intermediates.noindex", "*", "*", "*.build")):
        target = os.path.basename(build)[: -len(".build")]
        if target not in TARGETS:
            continue
        mine = tables(target, errors)
        for f in glob.glob(os.path.join(build, "Objects-normal", "*", "*.stringsdata")):
            # the symbols Xcode generates from a catalog's own keys
            if os.path.basename(f).startswith("GeneratedStringSymbols"):
                continue
            seen += 1
            src = os.path.basename(f).replace(".stringsdata", ".swift")
            for table, entries in json.load(open(f, encoding="utf-8")).get("tables", {}).items():
                path = mine.get(table)
                if not path:
                    errors.append(f"{target}: {src} uses the table {table!r}, but there is no {table}.xcstrings in "
                                  f"{', '.join(TARGETS[target])}")
                    continue
                keys = catalogs.get(path, set())
                cat = os.path.relpath(path, ROOT)
                for e in entries:
                    if e["key"] not in keys:
                        loc = e.get("location", {})
                        errors.append(f"{cat}: {e['key']!r} ({target}, {src}:{loc.get('startingLine', '?')}) is not in the catalog")
    if seen == 0:
        errors.append(f"{dd}: no .stringsdata of the Apple apps (build them with SWIFT_EMIT_LOC_STRINGS=YES)")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--stringsdata", metavar="DERIVED_DATA", help="also check the keys the built code uses")
    args = ap.parse_args()
    errors, warnings, catalogs = [], [], {}
    for d in CATALOG_DIRS:
        for path in sorted(glob.glob(os.path.join(ROOT, d, "**", "*.xcstrings"), recursive=True)):
            if "/build/" in path or "/DerivedData/" in path:
                continue
            catalogs[path] = check_catalog(path, errors, warnings)
    for target in sorted(TARGETS):
        if "Localizable" not in tables(target, errors):
            errors.append(f"{target}: no Localizable.xcstrings in {', '.join(TARGETS[target])}")
    if args.stringsdata:
        check_code(args.stringsdata, catalogs, errors)
    # each architecture's build writes its own .stringsdata: one line per problem
    errors = list(dict.fromkeys(errors))
    for w in warnings:
        print(f"::warning::{w}")
    for e in errors:
        print(f"::error::{e}")
    total = sum(len(k) for k in catalogs.values())
    print(f"{len(catalogs)} catalogs, {total} keys: {len(errors)} problems")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
