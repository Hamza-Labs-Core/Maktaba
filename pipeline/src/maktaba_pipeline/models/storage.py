"""On-disk model storage and active-model state.

Models live under a single root — ``$MAKTABA_MODELS_DIR`` if set, else
``~/.cache/maktaba/models`` — one subdirectory per model id. A model
counts as *installed* only once a completion marker is written, so a
crashed mid-download (files present, marker absent) is never mistaken
for a finished install.

Storage also persists which model is *active* (the one the pipeline
loads into memory) per type, in an ``active.json`` at the root. This is
the bit of config the API's ``activate_model`` flips.
"""

from __future__ import annotations

import json
import os
import shutil
from dataclasses import dataclass
from pathlib import Path

from .registry import _humanize

__all__ = ["InstalledModel", "Storage"]

# Written into a model dir once every file is in place. Its presence is
# the single source of truth for "installed".
_COMPLETE_MARKER = ".maktaba-complete"

# Maps model type -> active model id. Lives at the storage root.
_ACTIVE_FILE = "active.json"

_DEFAULT_ROOT = Path.home() / ".cache" / "maktaba" / "models"


@dataclass(frozen=True, slots=True)
class InstalledModel:
    """An installed model's id, location, and on-disk footprint."""

    id: str
    path: Path
    size_bytes: int

    @property
    def size_human(self) -> str:
        return _humanize(self.size_bytes)


class Storage:
    """Manages the model directory tree and active-model state.

    Pass ``root`` to point at an explicit directory (tests use a
    ``tmp_path``); otherwise the root is resolved from the environment.
    """

    def __init__(self, *, root: Path | None = None) -> None:
        if root is None:
            env = os.environ.get("MAKTABA_MODELS_DIR")
            root = Path(env) if env else _DEFAULT_ROOT
        self._root = Path(root)

    def models_dir(self) -> Path:
        """The storage root (not created until something is written)."""
        return self._root

    def model_path(self, model_id: str) -> Path:
        """Directory a given model's files live in."""
        return self._root / model_id

    # --- installed-state -------------------------------------------------

    def mark_installed(self, model_id: str) -> None:
        """Write the completion marker, flipping the model to installed."""
        d = self.model_path(model_id)
        d.mkdir(parents=True, exist_ok=True)
        (d / _COMPLETE_MARKER).write_text("ok", encoding="utf-8")

    def is_installed(self, model_id: str) -> bool:
        """True only if the completion marker is present."""
        return (self.model_path(model_id) / _COMPLETE_MARKER).exists()

    def installed_size(self, model_id: str) -> int:
        """Total bytes on disk for a model (0 if absent)."""
        d = self.model_path(model_id)
        if not d.exists():
            return 0
        return sum(p.stat().st_size for p in d.rglob("*") if p.is_file())

    def list_installed(self) -> list[InstalledModel]:
        """Every installed (marked) model, in directory-name order."""
        if not self._root.exists():
            return []
        out: list[InstalledModel] = []
        for child in sorted(self._root.iterdir()):
            if child.is_dir() and self.is_installed(child.name):
                out.append(
                    InstalledModel(
                        id=child.name,
                        path=child,
                        size_bytes=self.installed_size(child.name),
                    )
                )
        return out

    def delete(self, model_id: str) -> bool:
        """Remove a model's files. Returns False if it wasn't present.

        Also clears the active slot if the deleted model was active.
        """
        d = self.model_path(model_id)
        if not d.exists():
            return False
        shutil.rmtree(d)
        self._clear_active_if(model_id)
        return True

    # --- active-model state ---------------------------------------------

    def _active(self) -> dict[str, str]:
        path = self._root / _ACTIVE_FILE
        if not path.exists():
            return {}
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            return {}
        return {str(k): str(v) for k, v in data.items()} if isinstance(data, dict) else {}

    def _write_active(self, mapping: dict[str, str]) -> None:
        self._root.mkdir(parents=True, exist_ok=True)
        (self._root / _ACTIVE_FILE).write_text(json.dumps(mapping, indent=2), encoding="utf-8")

    def set_active(self, model_type: str, model_id: str) -> None:
        """Mark ``model_id`` as the active model for ``model_type``."""
        mapping = self._active()
        mapping[model_type] = model_id
        self._write_active(mapping)

    def active_for(self, model_type: str) -> str | None:
        """The active model id for a type, or None."""
        return self._active().get(model_type)

    def is_active(self, model_id: str) -> bool:
        """True if ``model_id`` is active for any type."""
        return model_id in self._active().values()

    def _clear_active_if(self, model_id: str) -> None:
        mapping = self._active()
        changed = {t: m for t, m in mapping.items() if m != model_id}
        if changed != mapping:
            self._write_active(changed)
