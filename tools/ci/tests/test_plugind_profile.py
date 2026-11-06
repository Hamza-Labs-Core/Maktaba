"""Story 54.03 T08: the macOS Seatbelt profile of maktaba-plugind allows no network but the
two unix-socket literals, denies by default, and never allows a new process."""

from __future__ import annotations

import re
from pathlib import Path

PROFILE = Path(__file__).resolve().parents[3] / "plugind" / "resources" / "plugind.sb"


def rules() -> list[str]:
    """The profile's top-level forms, comments dropped and spaces folded."""
    text = "\n".join(line.split(";", 1)[0] for line in PROFILE.read_text().splitlines())
    out, depth, start = [], 0, 0
    for i, ch in enumerate(text):
        if ch == "(":
            if depth == 0:
                start = i
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                out.append(re.sub(r"\s+", " ", text[start : i + 1]))
    return out


def test_denies_by_default() -> None:
    assert "(deny default)" in PROFILE.read_text()


def test_network_allows_only_the_two_unix_sockets() -> None:
    allows = [r for r in rules() if r.startswith("(allow network")]
    assert sorted(allows) == sorted(
        [
            '(allow network-outbound (remote unix-socket (path-literal (param "API_SOCKET"))))',
            '(allow network-inbound (local unix-socket (path-literal (param "DISPATCH_SOCKET"))))',
        ]
    ), allows
    assert "(deny network*)" in rules()
    text = PROFILE.read_text().lower()
    for word in ("tcp", "udp", "ip ", "localhost", "port", "*:"):
        code = "\n".join(line.split(";", 1)[0] for line in text.splitlines())
        assert word not in code, word


def test_no_process_is_ever_allowed() -> None:
    assert "(deny process-fork)" in rules()
    assert "(deny process-exec*)" in rules()
    assert not [r for r in rules() if r.startswith("(allow process-exec") or r.startswith("(allow process-fork")]


def test_writes_only_the_run_and_cache_folders() -> None:
    writes = [r for r in rules() if "file-write" in r and r.startswith("(allow")]
    for r in writes:
        assert ('"RUN_DIR"' in r or '"CACHE_DIR"' in r or '"/dev/null"' in r), r
