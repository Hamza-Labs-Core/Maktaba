"""Server-side extensions: optional integrations that add stages to the queue graph.

Built-in extensions are listed in :data:`BUILTIN`; third parties can ship a
Python entry point in the group ``maktaba.extensions`` whose target is an
:class:`~maktaba_pipeline.extensions.base.Extension` instance (or a zero-argument
factory returning one).
"""

from __future__ import annotations

import importlib
from importlib import metadata

from ..log import get_logger
from .base import Extension, Manifest, SettingField

__all__ = ["BUILTIN", "ENTRY_POINT_GROUP", "Extension", "Manifest", "SettingField", "discover"]

ENTRY_POINT_GROUP = "maktaba.extensions"

#: ``module:attribute`` of each built-in extension instance.
BUILTIN: tuple[str, ...] = (
    "maktaba_pipeline.extensions.plex:extension",
    "maktaba_pipeline.extensions.dizquetv:extension",
    "maktaba_pipeline.extensions.cloudflare_stream:extension",
)

_log = get_logger()


def _instance(obj: object) -> Extension:
    if hasattr(obj, "manifest"):
        return obj  # type: ignore[return-value]
    return obj()  # type: ignore[operator,no-any-return]


def discover(*, include_entry_points: bool = True) -> list[Extension]:
    """Built-in extensions plus entry-point extensions, de-duplicated by name."""
    found: dict[str, Extension] = {}
    for target in BUILTIN:
        module, _, attr = target.partition(":")
        try:
            ext = _instance(getattr(importlib.import_module(module), attr))
        except ImportError:  # a built-in that is not installed is simply absent
            _log.warning("extension_missing", target=target)
            continue
        found[ext.manifest.name] = ext
    if include_entry_points:
        for ep in metadata.entry_points(group=ENTRY_POINT_GROUP):
            try:
                ext = _instance(ep.load())
            except Exception:  # noqa: BLE001 - one broken third-party extension must not stop the worker
                _log.exception("extension_load_failed", entry_point=ep.name)
                continue
            found.setdefault(ext.manifest.name, ext)
    return list(found.values())
