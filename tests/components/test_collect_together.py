"""The two components test folders collect in one pytest session (issue #670).

``pipeline/tests/components`` is the pipeline's package ``tests.components``. This folder
(the repo root's ``tests/components``) is the package ``components``: the root ``tests/``
is no package (it has no ``__init__.py``), so the two never claim one module name. With
the root ``tests/`` a package too, both were ``tests.components`` and one session that
collected both failed (``ImportPathMismatchError`` on their conftests, then the root's
modules not found).
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def test_both_components_folders_collect_in_one_session() -> None:
    r = subprocess.run(
        [sys.executable, "-m", "pytest", "--collect-only", "-q", "-p", "no:cacheprovider",
         "tests/components", "../tests/components/test_rig_logs.py"],
        cwd=ROOT / "pipeline", capture_output=True, text=True, timeout=120, check=False,
    )
    out = r.stdout + r.stderr
    assert r.returncode == 0, out[-3000:]
    assert "tests/components/test_catalog.py::" in out, out[-2000:]
    assert "test_rig_logs.py::" in out, out[-2000:]
