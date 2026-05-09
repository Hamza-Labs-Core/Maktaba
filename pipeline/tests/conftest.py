"""Pipeline pytest setup — wires the shared testtier plugin.

The shared plugin lives at ``shared/testtier/py/maktaba_testtier`` so
other Python packages in the repo can pull it in too. We don't
currently install it as a uv dependency (no separate distribution,
no version churn) — instead we add the parent directory to
``sys.path`` here so ``import maktaba_testtier`` works.
"""

from __future__ import annotations

import sys
from pathlib import Path

# pipeline/tests/conftest.py → repo root is three parents up.
_REPO_ROOT = Path(__file__).resolve().parents[2]
_SHARED_PY = _REPO_ROOT / "shared" / "testtier" / "py"

if str(_SHARED_PY) not in sys.path:
    sys.path.insert(0, str(_SHARED_PY))

# Register the plugin's hooks + autouse fixture by re-exporting them
# from the conftest scope. pytest discovers `pytest_*` hooks and
# `@pytest.fixture` definitions on conftest.py at collection time.
from maktaba_testtier.netguard import unit_netguard  # noqa: F401,E402
from maktaba_testtier.softcap import (  # noqa: F401,E402
    pytest_runtest_makereport,
)
