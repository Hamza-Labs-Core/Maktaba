"""The settings file of a mediaintel cache: ``settings.json`` beside the database.

Sections: ``detect`` (series detection), ``groups`` (the group step) and
``planner`` (channel planning). Each holds only what was changed from the
built-in defaults; the settings page of ``serve`` edits it.
"""

from __future__ import annotations

import sqlite3
from pathlib import Path
from typing import Any

import maktaba_pipeline.classify.settings_help  # noqa: F401 — registers the help
import maktaba_pipeline.schedule.settings_help  # noqa: F401
from maktaba_pipeline import settings
from maktaba_pipeline.classify.series_detector import DetectorConfig
from maktaba_pipeline.schedule.channel_planner import PlannerConfig

from . import group_clips

#: section → (config class, title, what it decides)
SECTIONS: dict[str, tuple[type, str, str]] = {
    "planner": (PlannerConfig, "Channels", "how channels, slots, fillers and the Quran channel "
                "are planned (plan-channels)"),
    "groups": (group_clips.GroupConfig, "Grouping", "clip and stand-alone collections, promos, "
               "questions and fatwas (group)"),
    "detect": (DetectorConfig, "Detection", "how shows, seasons and talk shows are found "
               "(detect)"),
}  # fmt: skip


G = "Grouping"
settings.register(group_clips.GroupConfig, {
    "filler_max_minutes": settings.Help(G, "Clip up to (min)", "Longer leftovers are stand-alone "
                                        "programmes (منفردات), shorter ones clips (filler)."),
    "min_playlist": settings.Help(G, "Playlist group from (videos)", minimum=1),
    "min_topic": settings.Help(G, "Topic group from (videos)", minimum=1),
    "qa_phrases": settings.Help(G, "Question / fatwa phrases", "A title with one of these goes to "
                                "\"فتاوى ومسائل\". A phrase matches the start of a word (ما رأي "
                                "→ ما رأيك); end it with a space to match it only as a whole "
                                "word (\"حكم \" is not حكمة)."),
    "promo_words": settings.Help(G, "Promo words", "A title with one of these advertises a show; "
                                 "it is linked to the show it names."),
})  # fmt: skip


def path_of(db_file: str | Path) -> Path:
    return Path(db_file).with_name("settings.json")


def file_of(where: sqlite3.Connection | Path) -> Path | None:
    """The settings file of a cache connection (None for an in-memory one), or ``where``."""
    if isinstance(where, Path):
        return where
    db = next((r[2] for r in where.execute("PRAGMA database_list") if r[1] == "main"), "")
    return path_of(db) if db else None


def changed(where: sqlite3.Connection | Path, section: str) -> dict[str, Any]:
    file = file_of(where)
    return settings.load(file).get(section, {}) if file is not None else {}


def get(where: sqlite3.Connection | Path, section: str) -> Any:
    """The config of ``section`` with the file's changes applied."""
    return settings.apply(SECTIONS[section][0], changed(where, section))


def note(conn: sqlite3.Connection, section: str) -> None:
    """Print the changed settings a step runs with, so the log says why a result differs."""
    if ch := changed(conn, section):
        print(f"  settings ({section}): " + ", ".join(sorted(ch)), flush=True)
