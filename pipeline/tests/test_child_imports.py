"""Every Python child the server's supervisor starts imports cleanly in a fresh
interpreter, in the order that child imports its modules.

The 0.3.96 update: ``python -m maktaba_plugin_runner`` imported
``maktaba_pipeline.plugins.firstparty`` first; ``firstparty`` imported
``engine.graph``, whose module-level ``_fp(...)`` imported ``firstparty.stage_def``
while ``firstparty`` was still initialising (a circular import). Every plugin runner
crash-looped. The test suite never saw it: by the time a test imported ``firstparty``,
another module had already imported ``engine.graph`` in the same interpreter.

Each check here runs in its own ``python -I`` subprocess, so nothing a test imported
before hides an import cycle. The children's modules are read from the supervisor's Go
sources (every ``"-m", "maktaba_..."`` it launches), so a new kind of child is covered
without editing this file.
"""

from __future__ import annotations

import ast
import re
import subprocess
import sys
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[2]
SUPERVISOR = REPO / "cmd" / "maktaba-server" / "internal" / "supervisor"
SRC = REPO / "pipeline" / "src"

#: a ``python -m <module>`` command line in the supervisor's Go sources
_MODULE = re.compile(r'"-m",\s*"(maktaba_[A-Za-z0-9_]+)"')

#: modules a child imports only on another OS (a runner's Windows pipes)
_OTHER_OS = {"win32": set(), "other": {"maktaba_plugin_runner.winpipe"}}


def supervised_modules() -> list[str]:
    """The modules the supervisor runs with ``python -m`` (its non-test Go sources)."""
    found: set[str] = set()
    for go in sorted(SUPERVISOR.glob("*.go")):
        if go.name.endswith("_test.go"):
            continue
        found.update(_MODULE.findall(go.read_text(encoding="utf-8")))
    return sorted(found)


def _resolve(module: str, level: int, package: str) -> str:
    if level == 0:
        return module
    base = package.split(".")
    if level > 1:
        base = base[: -(level - 1)]
    return ".".join([*base, module] if module else base)


def _is_module(name: str) -> bool:
    if not name.startswith("maktaba_"):
        return False
    p = SRC.joinpath(*name.split("."))
    return p.with_suffix(".py").is_file() or (p / "__init__.py").is_file()


def entry_imports(module: str) -> list[str]:
    """The maktaba modules ``<module>/__main__.py`` imports, at its top and inside its
    functions (the runner imports ``firstparty`` only in ``main``), in source order."""
    path = SRC / module / "__main__.py"
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    skip = _OTHER_OS["win32" if sys.platform == "win32" else "other"]
    out: list[str] = []
    nodes = [n for n in ast.walk(tree) if isinstance(n, (ast.Import, ast.ImportFrom))]
    for node in sorted(nodes, key=lambda n: (n.lineno, n.col_offset)):
        names: list[str] = []
        if isinstance(node, ast.Import):
            names = [a.name for a in node.names]
        elif isinstance(node, ast.ImportFrom):
            base = _resolve(node.module or "", node.level, module)
            # ``from a.b import c``: a.b, then c (a submodule, or a name in a.b)
            names = [base, *(f"{base}.{a.name}" for a in node.names)]
        for name in names:
            # a module of this source tree (``from . import GROUPS`` names no module)
            if not _is_module(name) or name in skip or name in out:
                continue
            out.append(name)
    return out


def _import_check(code: str) -> subprocess.CompletedProcess[str]:
    # -I: no PYTHONPATH, no user site, not the current folder: the venv's packages alone
    return subprocess.run(
        [sys.executable, "-I", "-c", code],
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    )


def test_the_supervisor_launches_known_modules() -> None:
    mods = supervised_modules()
    # the two kinds of Python child today; a new one is covered by the tests below on its own
    assert {"maktaba_pipeline", "maktaba_plugin_runner"} <= set(mods), mods
    for m in mods:
        main = SRC / m / "__main__.py"
        assert main.is_file(), f"the supervisor runs python -m {m}: no {main}"


@pytest.mark.parametrize("module", supervised_modules())
def test_a_childs_imports_in_its_own_order(module: str) -> None:
    """What ``python -m <module>`` imports, in that order, in a fresh interpreter: the
    package, its ``__main__``, then each module ``__main__`` imports inside ``main``."""
    code = f"import {module}\nimport {module}.__main__\n"
    for name in entry_imports(module):
        code += f"import {name}\n"
    res = _import_check(code)
    assert res.returncode == 0, f"python -m {module}'s imports fail:\n{res.stderr}"


def _first_imports() -> list[str]:
    return sorted({name for m in supervised_modules() for name in entry_imports(m)})


@pytest.mark.parametrize("name", _first_imports())
def test_each_entry_import_alone_first(name: str) -> None:
    """Each module a child's entry point imports, imported first and alone: an import
    cycle shows whichever of its modules is imported first (``firstparty`` in 0.3.96)."""
    res = _import_check(f"import {name}\n")
    assert res.returncode == 0, f"importing {name} first fails:\n{res.stderr}"


def test_the_runner_imports_firstparty() -> None:
    # the import the 0.3.96 runners died on stays among the ones checked
    assert "maktaba_pipeline.plugins.firstparty" in entry_imports("maktaba_plugin_runner")
