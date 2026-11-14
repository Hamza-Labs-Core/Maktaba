#!/usr/bin/env python3
"""No review credential in the repository (Epic 58 story 58.04, T02).

The store reviewers' home (review.maktaba.tv) has a viewer account whose password lives only
in the release secrets. Each store's submission notes name the secret, and the release session
fills the password in when it submits. This check fails when:

- a store page (docs/stores/*.md), the review home's deploy files (deploy/review-home/) or its
  tools (tools/review_home/) hold a password-shaped value: a value after "password", "pwd" or
  "passcode" that is not a secret's name, a placeholder or an environment reference;
- a store's page (amazon, samsung, lg, roku, cast) does not name the secret or does not point
  to docs/stores/review-home.md.

tools/ci/tests/test_review_credentials.py runs it over the repository on every pull request.

    python3 tools/ci/review_credentials.py
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SECRET = "REVIEW_HOME_VIEWER_PASSWORD"
STORES = ("amazon", "samsung", "lg", "roku", "cast")
SCANNED = ("docs/stores/*.md", "deploy/review-home/**/*", "tools/review_home/**/*")
TEXT = {".md", ".json", ".yml", ".yaml", ".sh", ".py", ".txt", ".example", ".env", "", ".vtt", ".toml"}

# "password: X", "password = X", "password is X", "Password — X", "password `X`", "password "X""
_AFTER = re.compile(
    r"(?i)(?<![a-z0-9])(?:[a-z0-9]+_)*(?:password|passphrase|passcode|pwd)s?\b[`\"'*_]*[^\S\n]*"
    r"(?:[:=]|\bis\b|—)?[*_]*[^\S\n]*"
    r"(?P<q>[`\"']?)(?P<value>[A-Za-z0-9$<*][^\s`\"',;(){}\[\]|]*)(?P=q)"
)
# what may follow the word without being a password
_WORDS = re.compile(r"^[a-z][a-z-]*[.:]?$")  # ordinary words: "password field", "password.", "password-shaped"
_ALLOWED = re.compile(
    r"^(?:"
    r"\$\{?[A-Za-z_][A-Za-z0-9_]*\}?"  # $VAR, ${VAR}
    r"|\$\{\{.*\}\}"  # ${{ secrets.X }}
    r"|<[^>]*>"  # <the password>
    r"|[A-Z][A-Z0-9_]{2,}"  # a secret's or variable's name
    r"|\*+|x+|…|\.\.\.|-+"  # a mask
    r"|[a-z_]+\(\S*\)"  # a function call
    r"|[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+"  # an attribute (args.password)
    r"|[A-Za-z_][A-Za-z0-9_]*\[[^\]]*\]?"  # an index (p[1])
    r"|\d{1,3}"  # a number (a length)
    r"|[a-z]+_[a-z_]+"  # a field name (pw_hash)
    r")$"
)


def password_values(text: str) -> list[tuple[int, str]]:
    """Each (line number, value) that looks like a password written down."""
    found = []
    for n, line in enumerate(text.splitlines(), 1):
        for m in _AFTER.finditer(line):
            v = m.group("value").rstrip("*_")  # markdown emphasis around the word
            if len(v) < 6 or _WORDS.match(v) or _ALLOWED.match(v):
                continue
            found.append((n, v))
    return found


def files(root: Path = ROOT) -> list[Path]:
    out: set[Path] = set()
    for pattern in SCANNED:
        for p in root.glob(pattern):
            if p.is_file() and p.suffix in TEXT and "__pycache__" not in p.parts:
                out.add(p)
    return sorted(out)


def check(root: Path = ROOT) -> list[str]:
    problems = []
    for p in files(root):
        rel = p.relative_to(root)
        for n, v in password_values(p.read_text(encoding="utf-8", errors="replace")):
            problems.append(f"{rel}:{n}: a password-shaped value ({v[:3]}…); name the secret ({SECRET}) instead")
    for store in STORES:
        page = root / "docs" / "stores" / f"{store}.md"
        text = page.read_text(encoding="utf-8") if page.is_file() else ""
        if SECRET not in text:
            problems.append(f"docs/stores/{store}.md: the submission notes do not name the secret {SECRET}")
        if "review-home.md" not in text:
            problems.append(f"docs/stores/{store}.md: the submission notes do not point to review-home.md")
    return problems


def main() -> int:
    problems = check()
    for p in problems:
        print(f"::error::{p}")
    if problems:
        return 1
    print(f"review credentials: {len(files())} files, no password written down; every store names {SECRET}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
