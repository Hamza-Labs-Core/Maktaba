"""The TOML of the word review lists (Epic 52), read where Python has no tomllib (3.10 on
the CI and release runners): tables, arrays of tables, strings, integers, booleans, string
arrays and arrays of inline tables, one value per line or inline tables one per line. Every
file it reads is also read by tomllib on 3.11+, where `load` uses tomllib itself."""

from __future__ import annotations

import re
from typing import Any

__all__ = ["load", "loads"]

_KEY = r'(?:"[^"]*"|[A-Za-z0-9_-]+)'


def _value(s: str) -> Any:
    s = s.strip()
    if s.startswith('"'):
        return s[1:-1].replace('\\"', '"').replace("\\\\", "\\")
    if s in ("true", "false"):
        return s == "true"
    if s.startswith("{"):
        return _inline(s)
    if s.startswith("["):
        inner = s[1:-1].strip().rstrip(",")
        return [_value(x) for x in _split(inner)] if inner else []
    return int(s)


def _split(s: str) -> list[str]:
    out, depth, cur, q = [], 0, "", False
    for ch in s:
        if ch == '"':
            q = not q
        if not q and ch in "[{":
            depth += 1
        if not q and ch in "]}":
            depth -= 1
        if ch == "," and depth == 0 and not q:
            out.append(cur)
            cur = ""
            continue
        cur += ch
    if cur.strip():
        out.append(cur)
    return out


def _inline(s: str) -> dict[str, Any]:
    out = {}
    for part in _split(s.strip()[1:-1]):
        k, _, v = part.partition("=")
        out[k.strip().strip('"')] = _value(v)
    return out


def _path(s: str) -> list[str]:
    return [p.strip('"') for p in re.findall(_KEY, s)]


def _strip_comment(line: str) -> str:
    q = False
    for i, ch in enumerate(line):
        if ch == '"':
            q = not q
        if ch == "#" and not q:
            return line[:i]
    return line


def loads(text: str) -> dict[str, Any]:
    root: dict[str, Any] = {}
    cur = root
    pending = ""
    for raw in text.splitlines():
        line = _strip_comment(raw).strip()
        if pending:
            pending += " " + line
            if pending.count("[") > pending.count("]"):
                continue
            line, pending = pending, ""
        if not line:
            continue
        if line.startswith("[["):
            node = root
            *head, last = _path(line[2:-2])
            for k in head:
                node = node.setdefault(k, {})
            cur = {}
            node.setdefault(last, []).append(cur)
            continue
        if line.startswith("["):
            cur = root
            for k in _path(line[1:-1]):
                cur = cur.setdefault(k, {})
            continue
        k, _, v = line.partition("=")
        if v.strip().startswith("[") and v.count("[") > v.count("]"):
            pending = line
            continue
        cur[k.strip().strip('"')] = _value(v)
    return root


def load(f: Any) -> dict[str, Any]:
    data = f.read()
    text = data.decode("utf-8") if isinstance(data, bytes) else data
    try:
        import tomllib
    except ModuleNotFoundError:
        return loads(text)
    return tomllib.loads(text)
