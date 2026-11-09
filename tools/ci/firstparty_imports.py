"""The pipeline code Maktaba's first-party plugin stages run (Epic 54), read from the plugins'
own imports, for the path filters of the jobs that prove those stages (.github/ci-paths.yml).

A first-party stage's code is its plugin's package (``plugins/first-party/<name>/src``): the
manifest's ``entry`` (``module:IMPL``) is what the worker imports in-process
(``job_stages.impl = 'inproc'``) and what the runner imports as a plugin, so both sides of the
parity suite (``plugins / first-party``) run that package and every ``maktaba_pipeline``
module it imports. A change to one of those modules changes what the stage does, so the job
that proves the stage has to run for it:

- :func:`packages_used` is every ``maktaba_pipeline`` package (``<pkg>/**``) or top-level
  module (``<mod>.py``) any first-party plugin imports, at package granularity: the
  ``plugins`` filter covers each;
- :func:`stage_files` is the exact files a named stage's entry module imports (with the
  package ``__init__`` files the import runs): the ``runner_stages`` filter covers each for
  the stages its Windows test runs.

Direct imports only: the pipeline's own transitive graph reaches most of the package (the
engine alone imports almost all of it), and a filter that broad would run the parity suite
for every pipeline change.

    python3 tools/ci/firstparty_imports.py                      # the plugins filter's globs
    python3 tools/ci/firstparty_imports.py thumb_small convert_mp4   # a stage's files

Standard library only (the lint job runs it with no project installed).
"""

from __future__ import annotations

import ast
import sys
import tomllib
from collections.abc import Iterable
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
SRC = ROOT / "pipeline" / "src"
PIPELINE = "maktaba_pipeline"
PIPELINE_DIR = SRC / PIPELINE


def plugin_sources() -> dict[str, Path]:
    """Each first-party plugin package (``maktaba_fp_<name>``) and its folder."""
    out: dict[str, Path] = {}
    for init in sorted(FIRST_PARTY.glob("*/src/*/__init__.py")):
        out[init.parent.name] = init.parent
    return out


def _module_file(module: str, base: Path) -> Path | None:
    """The file a dotted module name is under ``base`` (a package's ``__init__.py`` or a
    module's ``.py``), or None when it is no module there (an attribute of one)."""
    p = base.joinpath(*module.split("."))
    if (p / "__init__.py").is_file():
        return p / "__init__.py"
    if p.with_suffix(".py").is_file():
        return p.with_suffix(".py")
    return None


def _imports(path: Path, package: str) -> set[str]:
    """Every module ``path`` imports, absolute, as far as a file decides it: ``from a.b
    import c`` names ``a.b.c`` too (a submodule, or an attribute the caller drops).
    ``package`` is the dotted package ``path`` is in, for its relative imports."""
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    out: set[str] = set()
    for n in ast.walk(tree):
        if isinstance(n, ast.Import):
            out |= {a.name for a in n.names}
        elif isinstance(n, ast.ImportFrom):
            if n.level:
                parts = package.split(".")
                base = parts[: len(parts) - (n.level - 1)]
                mod = ".".join([*base, *([n.module] if n.module else [])])
            else:
                mod = n.module or ""
            out.add(mod)
            out |= {f"{mod}.{a.name}" for a in n.names if a.name != "*"}
    return out


def _package_of(path: Path, root: Path) -> str:
    return ".".join(path.relative_to(root).parent.parts)


def _pipeline_files(modules: Iterable[str]) -> set[Path]:
    """The ``maktaba_pipeline`` files the named modules are, with each package's
    ``__init__.py`` on the way (an import runs them)."""
    out: set[Path] = set()
    for m in modules:
        parts = m.split(".")
        if parts[0] != PIPELINE:
            continue
        for i in range(1, len(parts) + 1):
            f = _module_file(".".join(parts[:i]), SRC)
            if f is not None:
                out.add(f)
    return out


def packages_used() -> set[str]:
    """Repo paths (globs) of the ``maktaba_pipeline`` code the first-party plugins import:
    ``pipeline/src/maktaba_pipeline/<pkg>/**`` for a package, the file for a top-level
    module, and the root package's ``__init__.py``, which every such import runs."""
    modules: set[str] = set()
    for folder in plugin_sources().values():
        root = folder.parent
        for f in sorted(folder.rglob("*.py")):
            modules |= _imports(f, _package_of(f, root))
    out: set[str] = set()
    for f in _pipeline_files(modules):
        rel = f.relative_to(PIPELINE_DIR)
        if len(rel.parts) == 1:
            out.add(f.relative_to(ROOT).as_posix())
        else:
            out.add(f"{(PIPELINE_DIR / rel.parts[0]).relative_to(ROOT).as_posix()}/**")
    return out


def stage_entries() -> dict[str, tuple[str, Path]]:
    """Each first-party stage's entry module and the plugin source folder it is in, from
    the manifests' ``[[stage]]`` tables."""
    out: dict[str, tuple[str, Path]] = {}
    for manifest in sorted(FIRST_PARTY.glob("*/manifest.toml")):
        doc = tomllib.loads(manifest.read_text(encoding="utf-8"))
        for st in doc.get("stage") or []:
            entry = str(st.get("entry") or "")
            if entry:
                out[str(st["name"])] = (entry.partition(":")[0], manifest.parent / "src")
    return out


def stage_files(stages: Iterable[str]) -> set[str]:
    """Repo paths of the ``maktaba_pipeline`` files the named stages' entry modules import,
    following the plugin package's own modules (each plugin's code, never another's)."""
    entries = stage_entries()
    found: set[str] = set()
    for stage in stages:
        if stage not in entries:
            raise KeyError(f"no first-party stage {stage!r} has an entry")
        module, root = entries[stage]
        own = module.split(".")[0]
        todo, seen = [module], set()
        while todo:
            m = todo.pop()
            f = _module_file(m, root)
            if f is None or f in seen:
                continue
            seen.add(f)
            for imp in _imports(f, _package_of(f, root)):
                head = imp.split(".")[0]
                if head == PIPELINE:
                    found.add(imp)
                elif head == own:
                    todo.append(imp)
                    # the package __init__ files on the way run too
                    parts = imp.split(".")
                    todo += [".".join(parts[:i]) for i in range(1, len(parts))]
    return {f.relative_to(ROOT).as_posix() for f in _pipeline_files(found)}


def main(argv: list[str]) -> int:
    paths = stage_files(argv) if argv else packages_used()
    for p in sorted(paths):
        print(p)  # noqa: T201
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
