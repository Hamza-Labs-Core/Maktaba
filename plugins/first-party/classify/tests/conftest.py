"""tv.maktaba.classify's tests run from the pipeline's environment, with its test fixtures."""

from __future__ import annotations

import sys
from pathlib import Path

PIPELINE = Path(__file__).resolve().parents[4] / "pipeline"
if str(PIPELINE) not in sys.path:
    sys.path.insert(0, str(PIPELINE))

from tests.engine.fixtures_db import db, pg_template, sqlite_template  # noqa: E402,F401
