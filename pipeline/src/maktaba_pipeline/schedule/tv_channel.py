"""What a planned channel is given, whichever system airs it: its filler lists with their
weights (:func:`filler_lists`), the clips a list holds (:func:`filler_items`) and its look
(:func:`channel_art`: the logo and corner watermark files).

Shared by the dizqueTV export (the integrations package's exporter) and
Maktaba's own ``write_channels``, so both air the same. Pure, apart from checking that the
picture files exist.
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

from .channel_planner import PlannerConfig

__all__ = ["channel_art", "filler_items", "filler_lists"]


def filler_lists(
    key: str, kind: str, pools: Sequence[str], cfg: PlannerConfig | Mapping[str, Any]
) -> list[dict[str, Any]]:
    """:func:`filler_collections` from a channel's key, kind and filler pools and the
    planner's settings (a :class:`PlannerConfig` or a plan's ``config``): the same lists
    for dizqueTV and for Maktaba TV's own scheduler."""

    def weight(name: str) -> int:
        if isinstance(cfg, Mapping):
            return int(cfg.get(name, getattr(PlannerConfig(), name)))
        return int(getattr(cfg, name))

    if kind == "quran":
        return [{"pool": p, "weight": weight("dz_weight_topic"), "cooldown": 0} for p in pools]
    want = (
        (f"{key}.topic", weight("dz_weight_topic")),
        (f"{key}.promos", weight("dz_weight_promos")),
        (f"{key}.breaks", weight("dz_weight_breaks")),
        ("library.general", weight("dz_weight_general")),
    )
    return [{"pool": p, "weight": w, "cooldown": 0} for p, w in want if p in pools]


def filler_items(
    plan: Mapping[str, Any], items: Sequence[Mapping[str, Any]]
) -> Sequence[Mapping[str, Any]]:
    """A dizqueTV filler list needs variety, not the whole pool: the first
    ``dz_filler_max_items`` of it (the pool is in priority order: own promos, then
    on-topic clips). A whole pool can be 75,000 clips, past dizqueTV's 50 MB request limit."""
    cap = int((plan.get("config") or {}).get("dz_filler_max_items") or 2000)
    return items[: max(1, cap)]


def channel_art(plan: Mapping[str, Any], key: str) -> dict[str, Any]:
    """The channel's logo and corner watermark from the plan's settings, as local files
    (``apply`` uploads them): ``{"icon": path, "watermark": {...}}``, only what is set."""
    cfg = plan.get("config") or {}
    out: dict[str, Any] = {}
    if folder := str(cfg.get("dz_logo_dir") or ""):
        for name in (f"{key}.png", "default.png"):
            if (Path(folder) / name).is_file():
                out["icon"] = str(Path(folder) / name)
                break
    wm = str(cfg.get("dz_watermark_file") or "")
    if folder_wm := str(cfg.get("dz_watermark_dir") or ""):
        own = [Path(folder_wm) / f"{key}{ext}" for ext in (".gif", ".png")]
        wm = next((str(p) for p in own if p.is_file()), wm)
    if wm and Path(wm).is_file():
        margin = float(cfg.get("dz_watermark_margin", 2.0))
        out["watermark"] = {
            "enabled": True, "url": wm, "width": float(cfg.get("dz_watermark_width", 12.0)),
            "verticalMargin": margin, "horizontalMargin": margin, "duration": 0,
            "position": str(cfg.get("dz_watermark_position") or "top-right"),
            "fixedSize": False, "animated": wm.lower().endswith(".gif"),
        }  # fmt: skip
    return out
