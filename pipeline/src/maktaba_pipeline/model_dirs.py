"""Where this machine keeps its model files (Epic 36, story 36.4).

``MAKTABA_MODEL_CACHE_DIR`` puts every model the pipeline downloads under one folder
(Maktaba.app: ``~/Library/Application Support/Maktaba/models``)::

    <dir>/huggingface/      HF_HOME: Whisper, the second listen, the embedder
    <dir>/camel_tools/      CAMELTOOLS_DATA: the CAMeL Tools MSA dictionary
    <dir>/share-copies/     copies of the home's published models (stt.model_share)

An ``HF_HOME`` or ``CAMELTOOLS_DATA`` already set wins. Unset, nothing changes: the
libraries' own defaults (``~/.cache/huggingface``, ``~/.camel_tools``,
``~/.maktaba/models``).

Not to be confused with ``MAKTABA_MODELS_DIR``, the *share's* models folder that the
home publishes the gated model to for the other machines (:mod:`.stt.model_share`).

The libraries read these variables when they are first imported, so :func:`apply_env`
runs when the package is (``maktaba_pipeline/__init__.py``). Standard library only.
"""

from __future__ import annotations

import os
from collections.abc import MutableMapping
from pathlib import Path

__all__ = ["ENV_MODEL_CACHE_DIR", "apply_env", "cache_dir", "share_copies_dir"]

ENV_MODEL_CACHE_DIR = "MAKTABA_MODEL_CACHE_DIR"


def cache_dir(env: MutableMapping[str, str] | None = None) -> Path | None:
    """``MAKTABA_MODEL_CACHE_DIR`` (``~`` expanded), or None when unset."""
    env = os.environ if env is None else env
    raw = (env.get(ENV_MODEL_CACHE_DIR) or "").strip()
    return Path(raw).expanduser() if raw else None


def share_copies_dir(env: MutableMapping[str, str] | None = None) -> Path | None:
    """Where copies of the share's models go, or None for the default."""
    root = cache_dir(env)
    return None if root is None else root / "share-copies"


def apply_env(env: MutableMapping[str, str] | None = None) -> None:
    """Point ``HF_HOME`` and ``CAMELTOOLS_DATA`` under ``MAKTABA_MODEL_CACHE_DIR``
    (each only when not set already)."""
    env = os.environ if env is None else env
    root = cache_dir(env)
    if root is None:
        return
    if not (env.get("HF_HOME") or "").strip():
        env["HF_HOME"] = str(root / "huggingface")
    if not (env.get("CAMELTOOLS_DATA") or "").strip():
        env["CAMELTOOLS_DATA"] = str(root / "camel_tools")
