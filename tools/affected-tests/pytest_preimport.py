"""Import every maktaba_pipeline module before the first test.

Loaded with ``-p pytest_preimport`` (``PYTHONPATH=tools/affected-tests``) by the coverage
map job only. Code that runs at import time then runs before any test's coverage context
starts, so it is not pinned on whichever test happened to import the module first.
"""

from __future__ import annotations

import importlib
import pkgutil


def pytest_sessionstart(session: object) -> None:
    try:
        root = importlib.import_module("maktaba_pipeline")
    except Exception:
        return
    for mod in pkgutil.walk_packages(root.__path__, prefix="maktaba_pipeline."):
        try:
            importlib.import_module(mod.name)
        except Exception:  # an optional extra not installed: its tests skip too
            continue
