"""The import contract of Maktaba's first-party plugins (Epic 54, story 54.27 §1).

A plugin's code imports the SDK, ``maktaba_fp_lib`` (``_lib``), its own package and the
pipeline's library modules (the engine's types, the domain code a not yet moved stage
still shares), never another first-party plugin. ``maktaba_pipeline`` imports a plugin only
through the stage registry's entry (``engine/stages/registry.py``: the in-process path of
``job_stages.impl = 'inproc'``); once the removal release drops that path (story 54.36 §4),
``REMOVED`` makes any import of a plugin from the pipeline fail.

``python3 tools/plugins/import_contracts.py`` fails naming the file and both packages.
"""

from __future__ import annotations

import ast
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
PIPELINE = ROOT / "pipeline" / "src"

#: true once the in-process path is gone (story 54.36 §4): the pipeline imports no plugin
REMOVED = False
#: the one place the pipeline reaches a plugin's code while the in-process path exists
ENTRY = {"maktaba_pipeline.engine.stages.registry"}


def packages() -> dict[str, Path]:
    out: dict[str, Path] = {}
    if FIRST_PARTY.is_dir():
        for p in sorted(FIRST_PARTY.iterdir()):
            for pkg in sorted((p / "src").glob("*/__init__.py")):
                out[pkg.parent.name] = pkg.parent
    return out


def imported(path: Path) -> set[str]:
    tree = ast.parse(path.read_text(encoding="utf-8"))
    out: set[str] = set()
    for n in ast.walk(tree):
        if isinstance(n, ast.Import):
            out |= {a.name.split(".")[0] for a in n.names}
        elif isinstance(n, ast.ImportFrom) and n.module and n.level == 0:
            out.add(n.module.split(".")[0])
    return out


def check() -> list[str]:
    pkgs = packages()
    problems: list[str] = []
    for name, folder in pkgs.items():
        for f in sorted(folder.rglob("*.py")):
            for other in sorted(imported(f) & set(pkgs) - {name, "maktaba_fp_lib"}):
                problems.append(f"{f.relative_to(ROOT)}: {name} imports {other} (another first-party plugin)")
    for f in sorted(PIPELINE.rglob("*.py")):
        rel = f.relative_to(PIPELINE).with_suffix("")
        mod = ".".join(rel.parts)
        hits = imported(f) & set(pkgs)
        if hits and (REMOVED or mod not in ENTRY):
            for other in sorted(hits):
                problems.append(f"{f.relative_to(ROOT)}: maktaba_pipeline imports {other}")
    return problems


def main() -> int:
    problems = check()
    for p in problems:
        print(f"import contract: {p}", file=sys.stderr)  # noqa: T201
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
