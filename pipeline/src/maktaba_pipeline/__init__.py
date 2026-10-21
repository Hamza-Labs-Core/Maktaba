"""Maktaba transcription/indexing pipeline (stub).

This module is a stub created by Story 22.1 so the CI pipeline has a
real Python package to lint and test against. Real pipeline code lands
across Epics 1–6.
"""

__version__ = "0.0.1"

# HF_HOME / CAMELTOOLS_DATA from MAKTABA_MODEL_CACHE_DIR, before any library reads them
from .model_dirs import apply_env as _apply_model_dirs  # noqa: E402

_apply_model_dirs()


def stub_status() -> str:
    """Return a placeholder status string.

    Replaced by real pipeline entry points in later stories.
    """
    return "stub"
