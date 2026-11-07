"""The pipeline wheel's build hook (Epic 54, story 54.27 §2).

Maktaba's own stages are first-party plugins under ``plugins/`` at the repo root
(``plugins/sdk/python`` and ``plugins/first-party/<name>/src``). They ship inside this
wheel, the one Python artifact a release signs, so a standard build copies every package
those folders hold into it. An editable install (``uv sync``) leaves them where they are:
``dev-mode-dirs`` puts the folders on ``sys.path``, so an edit is seen at once.

The release's packaging script builds from a copy of this folder; it copies ``plugins/``
beside it (``deploy/packaging/node/build-release.sh``), so ``../plugins`` resolves there too.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

try:
    from hatchling.builders.hooks.plugin.interface import BuildHookInterface
except ImportError:  # the tests import package_dirs outside a build
    BuildHookInterface = object  # type: ignore[assignment,misc]


def package_dirs(root: Path) -> dict[str, str]:
    """Every Python package of the plugin folders: source path -> path in the wheel."""
    plugins = (root / ".." / "plugins").resolve()
    out: dict[str, str] = {}
    if not plugins.is_dir():
        return out
    sources = [plugins / "sdk" / "python"]
    first = plugins / "first-party"
    if first.is_dir():
        sources += sorted(p / "src" for p in first.iterdir() if (p / "src").is_dir())
    for src in sources:
        if not src.is_dir():
            continue
        for pkg in sorted(src.iterdir()):
            if (pkg / "__init__.py").is_file():
                out[str(pkg)] = pkg.name
    return out


class CustomBuildHook(BuildHookInterface):  # type: ignore[type-arg]
    def initialize(self, version: str, build_data: dict[str, Any]) -> None:
        if version == "editable":
            return
        build_data["force_include"].update(package_dirs(Path(self.root)))
