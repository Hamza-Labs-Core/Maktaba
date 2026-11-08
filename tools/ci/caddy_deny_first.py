"""No Caddyfile hides a terminal directive behind its handle blocks (Epic 50: #927, #930).

Caddy sorts the directives of a block by a fixed order, not by the order they are written in,
and `handle` sorts before `respond`, `reverse_proxy`, `file_server`, `abort`, `error` and
`php_fastcgi`. So in a block that also has `handle` blocks, a bare one of these runs only for a
request no handle took, and never once a catch-all `handle { }` is there. Twice a deny rule
written that way never ran: the aop fallback's 403 for addresses outside Cloudflare's ranges
(origin-aop.caddy, fixed in #927) and the witness's 401 without its token (Caddyfile.witness,
fixed in #930). `caddy validate` accepts both.

The rule, over every Caddyfile and snippet git tracks: in one block, `handle` (or
`handle_path`) and a bare terminal directive never sit side by side. Put the terminal directive
in a `handle` of its own, first (handle blocks are exclusive and keep their written order), or
use a `route` block, whose directives run as written. `route` blocks are exempt.

    python3 tools/ci/caddy_deny_first.py [ROOT]        every tracked Caddyfile under ROOT
    python3 tools/ci/caddy_deny_first.py --files F...  just these files
"""

from __future__ import annotations

import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path, PurePosixPath

HANDLES = {"handle", "handle_path"}
# the terminal directives Caddy orders after handle (caddyconfig/httpcaddyfile/directives.go)
TERMINAL = {"respond", "reverse_proxy", "file_server", "abort", "error", "php_fastcgi"}
# blocks whose directives run in the order written
LITERAL = {"route"}
# the test fixtures hold the two files that broke the rule, on purpose
EXCLUDE = ("tools/ci/tests/fixtures/",)


def is_caddyfile(path: str) -> bool:
    name = PurePosixPath(path).name
    return name.startswith("Caddyfile") or name.endswith((".caddy", "-Caddyfile"))


def tokens(text: str) -> list[tuple[int, str]]:
    """The Caddyfile's tokens with their line numbers; "\\n" ends a line. Quotes and backticks
    group a token, a # at a token's start comments the rest of the line, and only a { or }
    standing alone opens or closes a block ({$ENV} and {placeholder} are words)."""
    out: list[tuple[int, str]] = []
    line, i, n = 1, 0, len(text)
    while i < n:
        c = text[i]
        if c == "\n":
            out.append((line, "\n"))
            line += 1
            i += 1
        elif c in " \t\r":
            i += 1
        elif c == "#":
            while i < n and text[i] != "\n":
                i += 1
        else:
            start_line, tok = line, []
            while i < n and text[i] not in " \t\r\n":
                if text[i] in "\"`":
                    q = text[i]
                    i += 1
                    while i < n and text[i] != q:
                        if text[i] == "\\" and q == '"' and i + 1 < n:
                            i += 1
                        if text[i] == "\n":
                            line += 1
                        tok.append(text[i])
                        i += 1
                    i += 1
                    tok.append("\0")  # a quoted token is never a brace
                else:
                    tok.append(text[i])
                    i += 1
            out.append((start_line, "".join(tok)))
    return out


@dataclass
class Block:
    name: str
    line: int
    label: str = ""
    children: list[tuple[str, int]] = field(default_factory=list)


def problems_in(text: str, path: str) -> list[str]:
    stack: list[Block] = [Block("", 0)]  # the top level: site blocks, snippets, global options
    found: list[str] = []
    at_start = True  # the next token begins a directive
    current: tuple[str, int] | None = None  # the directive whose arguments are being read
    words: list[str] = []  # the current line's tokens, for the block's label

    def close(b: Block) -> None:
        if b.name in LITERAL or b.name.startswith("@"):
            return
        handles = [ln for d, ln in b.children if d in HANDLES]
        if not handles:
            return
        for d, ln in b.children:
            if d in TERMINAL:
                found.append(
                    f"{path}:{ln}: a bare {d} beside handle (line {handles[0]}) in the "
                    f"{b.label or 'top-level'} block at line {b.line}: Caddy orders handle "
                    f"first, so it runs only for a request no handle took (never behind a "
                    f"catch-all). Put it in a handle of its own, first, or use a route block."
                )

    for ln, tok in tokens(text):
        if tok == "\n":
            at_start, current, words = True, None, []
        elif tok == "{":
            name = current[0] if current else ""
            stack.append(Block(name, ln, " ".join(words).replace("\0", "")))
            at_start, current, words = True, None, []
        elif tok == "}":
            if len(stack) > 1:
                close(stack.pop())
            at_start, current, words = True, None, []
        elif at_start:
            current = (tok, ln)
            stack[-1].children.append(current)
            at_start, words = False, [tok]
        else:
            words.append(tok)
    while len(stack) > 1:
        close(stack.pop())
    close(stack[0])
    return found


def tracked(root: Path) -> list[str]:
    out = subprocess.run(["git", "-C", str(root), "ls-files"], check=True, capture_output=True, text=True)
    return sorted(p for p in out.stdout.splitlines() if is_caddyfile(p) and not p.startswith(EXCLUDE))


def main(argv: list[str]) -> int:
    if len(argv) > 1 and argv[1] == "--files":
        files = [(Path(p), p) for p in argv[2:]]
    else:
        root = Path(argv[1]) if len(argv) > 1 else Path(__file__).resolve().parents[2]
        files = [(root / p, p) for p in tracked(root)]
    if not files:
        print("caddy deny-first: no Caddyfile found", file=sys.stderr)
        return 1
    found = [p for f, name in files for p in problems_in(f.read_text(), name)]
    for p in found:
        print(p, file=sys.stderr)
    if found:
        return 1
    print(f"caddy deny-first: ok ({len(files)} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
