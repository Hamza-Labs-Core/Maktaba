#!/usr/bin/env python3
"""The Apple UI tests type through `typeExactly` (apps/apple/UITests/TypingInFields.swift).

    tools/apple/check-ui-typing.py              # the UI test sources
    tools/apple/check-ui-typing.py --self-test  # this check, on samples

`typeText` can lose keystrokes (CI: an iPad's picker kept "h" of the mock home's address),
and `typeExactly` reads the field back and types again until it holds the text. So a raw
`typeText(` in a UI test source (the iPhone, iPad and Remote UI tests, the Mac's, the TV's)
fails here, outside TypingInFields.swift itself. `typeText("\\n")`, the key that submits a
field, is allowed: it types no text to read back.
"""
import argparse
import glob
import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SOURCES = ["apps/apple/UITests", "apps/apple/PlayerMacTests/UI", "apps/tv/tvos/MaktabaTVUITests"]
HELPER = "TypingInFields.swift"
RAW = re.compile(r"\btypeText\(")
SUBMIT = re.compile(r'\btypeText\("\\n"\)')


def offences(name: str, text: str) -> list[str]:
    """The lines of `text` (a file called `name`) that type raw text."""
    if os.path.basename(name) == HELPER:
        return []
    found = []
    for n, line in enumerate(text.splitlines(), 1):
        code = line.split("//", 1)[0]
        if len(RAW.findall(code)) > len(SUBMIT.findall(code)):
            found.append(f"{name}:{n}: {line.strip()}")
    return found


def self_test() -> None:
    assert offences("A.swift", 'field.typeText(home.url)') == ["A.swift:1: field.typeText(home.url)"]
    assert offences("A.swift", 'app.typeText(text)')
    assert offences("A.swift", 'if submit { field.typeText("\\n") }') == []
    assert offences("A.swift", '// field.typeText(x) in a comment') == []
    assert offences("A.swift", 'field.typeExactly(home.url, in: app)') == []
    assert offences("UITests/TypingInFields.swift", "typeText(text)") == []
    print("check-ui-typing: self-test passed")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--self-test", action="store_true")
    if parser.parse_args().self_test:
        self_test()
        return 0
    found = []
    for folder in SOURCES:
        for path in sorted(glob.glob(os.path.join(ROOT, folder, "**", "*.swift"), recursive=True)):
            with open(path, encoding="utf-8") as f:
                found += offences(os.path.relpath(path, ROOT), f.read())
    if found:
        print("UI tests type through typeExactly (TypingInFields.swift), not typeText:", file=sys.stderr)
        print("\n".join(found), file=sys.stderr)
        return 1
    print("check-ui-typing: every UI test types through typeExactly")
    return 0


if __name__ == "__main__":
    sys.exit(main())
