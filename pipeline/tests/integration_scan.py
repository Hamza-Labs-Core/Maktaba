"""Which test modules can hold an ``integration`` test, decided by reading their source.

The integration tier runs ``pytest -m integration --integration-files``: collecting the
whole tree (5,000+ items) to select a fifth of them cost the tier most of its pytest time
on CI, where it runs beside the Go modules' builds. With the flag, ``tests/conftest.py``
skips every test module this scan rules out before it is imported, and ``-m integration``
still selects the tests within the rest, so the scan only has to over-include.

A module is kept when it could carry the marker in any of these ways:

- its source names ``integration`` anywhere (``pytest.mark.integration``, ``pytestmark``,
  a comment: a stray word only keeps a module that did not need to be);
- one of its functions takes, or one of its strings names (``usefixtures``,
  ``getfixturevalue``), a fixture that is marked: a fixture whose decorator names
  ``integration`` (the ``db`` fixture's ``postgres`` param), or one taking such a fixture,
  transitively;
- it uses a name some test helper module binds to an expression naming ``integration``
  (a mark kept in a helper: ``needs_pg = pytest.mark.integration``);
- a ``conftest.py`` above it defines an autouse fixture that is marked.

``tests/test_integration_scan.py`` proves it: the tests collected with the flag equal
``-m integration`` over the whole tree. A new way of marking a test that this scan does
not see fails that test, naming the tests it lost.
"""

from __future__ import annotations

import ast
from dataclasses import dataclass, field
from pathlib import Path

MARKER = "integration"


@dataclass
class _Module:
    path: Path
    source: str
    tree: ast.Module
    #: fixture name -> (names of its parameters, string constants in it, autouse)
    fixtures: dict[str, tuple[set[str], set[str], bool]] = field(default_factory=dict)
    marked_fixtures: set[str] = field(default_factory=set)
    #: every function parameter, string constant and name used in the module
    params: set[str] = field(default_factory=set)
    strings: set[str] = field(default_factory=set)
    names: set[str] = field(default_factory=set)
    #: top-level names bound to an expression that names the marker
    mark_aliases: set[str] = field(default_factory=set)


def _is_fixture_decorator(node: ast.expr) -> bool:
    target = node.func if isinstance(node, ast.Call) else node
    if isinstance(target, ast.Attribute):
        return target.attr == "fixture"
    return isinstance(target, ast.Name) and target.id == "fixture"


def _fixture_name(func: ast.FunctionDef | ast.AsyncFunctionDef, deco: ast.expr) -> str:
    if isinstance(deco, ast.Call):
        for kw in deco.keywords:
            value = kw.value
            if (
                kw.arg == "name"
                and isinstance(value, ast.Constant)
                and isinstance(value.value, str)
            ):
                return value.value
    return func.name


def _autouse(deco: ast.expr) -> bool:
    if not isinstance(deco, ast.Call):
        return False
    return any(
        kw.arg == "autouse" and isinstance(kw.value, ast.Constant) and kw.value.value is True
        for kw in deco.keywords
    )


def _arg_names(func: ast.FunctionDef | ast.AsyncFunctionDef) -> set[str]:
    a = func.args
    return {x.arg for x in [*a.posonlyargs, *a.args, *a.kwonlyargs]}


def _strings(node: ast.AST) -> set[str]:
    return {
        n.value for n in ast.walk(node) if isinstance(n, ast.Constant) and isinstance(n.value, str)
    }


def _read(path: Path) -> _Module:
    source = path.read_text(encoding="utf-8")
    tree = ast.parse(source, filename=str(path))
    mod = _Module(path, source, tree)
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            mod.params |= _arg_names(node)
            for deco in node.decorator_list:
                if _is_fixture_decorator(deco):
                    name = _fixture_name(node, deco)
                    mod.fixtures[name] = (_arg_names(node), _strings(node), _autouse(deco))
                    if MARKER in (ast.get_source_segment(source, deco) or ""):
                        mod.marked_fixtures.add(name)
        elif isinstance(node, ast.Constant) and isinstance(node.value, str):
            mod.strings.add(node.value)
        elif isinstance(node, ast.Name):
            mod.names.add(node.id)
        elif isinstance(node, ast.Attribute):
            mod.names.add(node.attr)
        elif isinstance(node, ast.alias):
            mod.names.add(node.asname or node.name.rsplit(".", 1)[-1])
    for stmt in tree.body:
        if (
            isinstance(stmt, (ast.Assign, ast.AnnAssign))
            and stmt.value is not None
            and MARKER in (ast.get_source_segment(source, stmt.value) or "")
        ):
            targets = stmt.targets if isinstance(stmt, ast.Assign) else [stmt.target]
            mod.mark_aliases |= {t.id for t in targets if isinstance(t, ast.Name)}
    return mod


def _is_test_module(path: Path) -> bool:
    return path.name.startswith("test_") or path.name.endswith("_test.py")


def integration_modules(tests_dir: Path) -> set[Path]:
    """The test modules under ``tests_dir`` (resolved paths) that may hold an integration test."""
    tests_dir = tests_dir.resolve()
    modules = [_read(p) for p in sorted(tests_dir.rglob("*.py"))]

    # marked fixtures, closed over the fixtures that take or look one up
    marked: set[str] = set().union(*(m.marked_fixtures for m in modules))
    while True:
        grown = {
            name
            for m in modules
            for name, (params, strings, _) in m.fixtures.items()
            if name not in marked and (params | strings) & marked
        }
        if not grown:
            break
        marked |= grown

    helpers = [m for m in modules if not _is_test_module(m.path)]
    aliases: set[str] = set().union(*(m.mark_aliases for m in helpers))
    autouse_dirs = {
        m.path.parent
        for m in modules
        if m.path.name == "conftest.py"
        and any(n in marked and auto for n, (_, _, auto) in m.fixtures.items())
    }

    kept: set[Path] = set()
    for m in modules:
        if not _is_test_module(m.path):
            continue
        if (
            MARKER in m.source
            or (m.params | m.strings) & marked
            or m.names & aliases
            or any(d == m.path.parent or d in m.path.parents for d in autouse_dirs)
        ):
            kept.add(m.path)
    return kept
