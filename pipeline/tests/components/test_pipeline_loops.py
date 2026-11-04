"""T11 (Epic 51, story 51.06): the pipeline's loops and the catalog name the same loops, of
the same components (the pipeline half of F1; the catalog job runs it)."""

from __future__ import annotations

import ast
import re
from pathlib import Path

import pytest

from maktaba_pipeline.components.catalog import default
from maktaba_pipeline.components.runner import PIPELINE_LOOPS

pytestmark = pytest.mark.unit

PACKAGE = Path(__file__).resolve().parents[2] / "src" / "maktaba_pipeline"
RUNTIME = PACKAGE / "runtime.py"


def test_the_loop_table_is_the_catalogs() -> None:
    cat = {name: c.id for name, c in default().loops_of("pipeline").items()}
    assert cat == PIPELINE_LOOPS


def declared_loops(root: Path = PACKAGE) -> tuple[dict[str, str], list[str]]:
    """Every loop a ``<runner>.declare(...)`` call in ``root``'s modules declares (read from
    the source of every module, not a list of files), with where; and every call whose
    name is not a string literal."""
    found: dict[str, str] = {}
    bad: list[str] = []
    for path in sorted(root.rglob("*.py")):
        src = path.read_text(encoding="utf-8")
        if ".declare(" not in src:
            continue
        for node in ast.walk(ast.parse(src, filename=str(path))):
            if not (
                isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute)
                and node.func.attr == "declare"
            ):
                continue
            where = f"{path.relative_to(root.parent)}:{node.lineno}"
            arg = node.args[0] if node.args else None
            if isinstance(arg, ast.Constant) and isinstance(arg.value, str):
                found.setdefault(arg.value, where)
            else:
                bad.append(where)
    return found, bad


def test_every_loop_the_package_declares_is_in_the_table() -> None:
    found, bad = declared_loops()
    assert not bad, f"declare() with a name that is not a string literal: {bad}"
    declared = set(found)
    assert declared, "the pipeline declares no loop"
    unknown = {n: w for n, w in found.items() if n not in PIPELINE_LOOPS}
    assert not unknown, f"loops in no catalog component: {unknown}"
    # the switchable components' loops are all the runner's to start and stop
    cat = default()
    switchable = {n for n, cid in PIPELINE_LOOPS.items() if (c := cat.get(cid)) and c.switchable}
    assert switchable <= declared


def test_a_loop_declared_in_a_new_file_is_found(tmp_path: Path) -> None:
    pkg = tmp_path / "pkg"
    (pkg / "sub").mkdir(parents=True)
    (pkg / "sub" / "new.py").write_text(
        "def wire(components, fn, name):\n"
        "    components.declare('brand-new-loop', fn)\n"
        "    components.declare(name, fn)\n",
        encoding="utf-8",
    )
    found, bad = declared_loops(pkg)
    assert found == {"brand-new-loop": "pkg/sub/new.py:2"}
    assert bad == ["pkg/sub/new.py:3"]
    assert "brand-new-loop" not in PIPELINE_LOOPS


def test_every_named_task_is_a_catalog_loop() -> None:
    src = RUNTIME.read_text(encoding="utf-8")
    # the core loops the runtime starts itself, by task name or writer name
    started = set(re.findall(r'name="([a-z-]+)"', src)) | set(
        re.findall(r'writer\(\s*"([a-z-]+)"', src)
    )
    core = {n for n, cid in PIPELINE_LOOPS.items() if cid == "core.pipeline"}
    assert core <= started
