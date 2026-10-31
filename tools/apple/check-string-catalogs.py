#!/usr/bin/env python3
"""The Apple apps' String Catalogs are complete: every app speaks English and Arabic.

    tools/apple/check-string-catalogs.py                      # the catalogs alone
    tools/apple/check-string-catalogs.py --stringsdata <DerivedData>

Every key of every .xcstrings under apps/apple and apps/tv/tvos needs an Arabic ("ar")
translation in state "translated" (each plural or device variation too) with the key's
arguments, and a key that is not its own English (one added by hand, extractionState
"manual") its English as well. With --stringsdata, the .stringsdata Xcode writes as it
builds (SWIFT_EMIT_LOC_STRINGS) are read too: every key the code uses must be in its
target's catalog, so a new Text("...") without its Arabic fails here, not in a review.
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
# a target's build folder (its .stringsdata) -> its Localizable catalog
TARGETS = {
    "Maktaba Remote": "apps/apple/Remote/Localizable.xcstrings",
    "Maktaba Remote iPad": "apps/apple/Remote/Localizable.xcstrings",
    "Maktaba Player": "apps/apple/Player/Localizable.xcstrings",
    "Maktaba Player iPad": "apps/apple/Player/Localizable.xcstrings",
    "Maktaba Player Mac": "apps/apple/Player/Localizable.xcstrings",
    "Maktaba TV": "apps/tv/tvos/MaktabaTV/Localizable.xcstrings",
    "MaktabaKit-t": "apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings",
    "MaktabaKit": "apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings",
}

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


def check_code(dd, catalogs, errors):
    """Every key a target's code uses is in its catalog."""
    seen = 0
    for build in glob.glob(os.path.join(dd, "Build", "Intermediates.noindex", "*", "*", "*.build")):
        target = os.path.basename(build)[: -len(".build")]
        cat = TARGETS.get(target)
        if not cat:
            continue
        keys = catalogs.get(os.path.join(ROOT, cat), set())
        for f in glob.glob(os.path.join(build, "Objects-normal", "*", "*.stringsdata")):
            # the symbols Xcode generates from a catalog's own keys
            if os.path.basename(f).startswith("GeneratedStringSymbols"):
                continue
            seen += 1
            for table, entries in json.load(open(f, encoding="utf-8")).get("tables", {}).items():
                if table != "Localizable":
                    continue
                for e in entries:
                    if e["key"] not in keys:
                        loc = e.get("location", {})
                        src = os.path.basename(f).replace(".stringsdata", ".swift")
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
    for want in sorted(set(TARGETS.values())):
        if os.path.join(ROOT, want) not in catalogs:
            errors.append(f"{want}: missing")
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
