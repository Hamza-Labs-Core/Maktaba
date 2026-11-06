#!/usr/bin/env python3
"""The pipeline's log lint (Epic 53 story 53.01, T05; the Go side is tools/log-lint).

A log call's event name (``msg``) is a constant, never runtime data: an f-string, a
``%`` format, ``.format()`` or a ``+`` join as the first argument of
``<logger>.debug/info/warning/warn/error/exception/critical/fatal(...)`` fails. Values
go in keyword fields: ``log.info("video_imported", duration_s=12.4)``.

    python tools/lint_log.py [PATH ...]     # default: src/
"""

from __future__ import annotations

import ast
import sys
from collections.abc import Iterator
from pathlib import Path

LEVELS = {"debug", "info", "warning", "warn", "error", "exception", "critical", "fatal", "msg"}
#: names a logger goes by in this tree (``_log``, ``log``, ``logger``, ``self._log``...)
LOGGER_NAMES = {"log", "_log", "logger", "_logger", "LOG", "LOGGER", "slog"}


def _logger_call(node: ast.Call) -> bool:
    f = node.func
    if not isinstance(f, ast.Attribute) or f.attr not in LEVELS:
        return False
    target = f.value
    if isinstance(target, ast.Name):
        return target.id in LOGGER_NAMES
    if isinstance(target, ast.Attribute):
        return target.attr in LOGGER_NAMES
    # get_logger().info(...), log.bind(...).info(...)
    if isinstance(target, ast.Call) and isinstance(target.func, (ast.Name, ast.Attribute)):
        name = target.func.id if isinstance(target.func, ast.Name) else target.func.attr
        return name in {"get_logger", "bind", "new"}
    return False


def _dynamic(arg: ast.expr) -> str | None:
    if isinstance(arg, ast.JoinedStr):
        return "an f-string"
    if isinstance(arg, ast.BinOp) and isinstance(arg.op, ast.Mod):
        return "a % format"
    if isinstance(arg, ast.BinOp) and isinstance(arg.op, ast.Add):
        return "a + join"
    if (
        isinstance(arg, ast.Call)
        and isinstance(arg.func, ast.Attribute)
        and arg.func.attr == "format"
        and isinstance(arg.func.value, ast.Constant)
    ):
        return "a .format() call"
    return None


def check_source(source: str, name: str = "<src>") -> Iterator[tuple[int, str]]:
    tree = ast.parse(source, name)
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and node.args and _logger_call(node):
            why = _dynamic(node.args[0])
            if why:
                yield node.lineno, f"the log event name is {why}: use a constant and keyword fields"


def main(argv: list[str]) -> int:
    roots = [Path(a) for a in argv] or [Path(__file__).resolve().parents[1] / "src"]
    bad = 0
    for root in roots:
        files = [root] if root.is_file() else sorted(root.rglob("*.py"))
        for path in files:
            for line, why in check_source(path.read_text(encoding="utf-8"), str(path)):
                print(f"{path}:{line}: {why}")  # noqa: T201
                bad += 1
    if bad:
        print(f"lint-log: {bad} log call(s) with runtime data in the event name")  # noqa: T201
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
