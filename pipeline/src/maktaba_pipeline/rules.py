"""The rules as settings: the sections (detect, groups, planner, tag), their help, the schema
the API and the web settings page are built from, and a tolerant reader for stored changes.

Each section is a frozen config dataclass whose defaults are the built-in rules; what a
user changed is stored as ``{setting: value}`` (Maktaba: ``app_settings`` key
``rules.<section>``; mediaintel: ``settings.json`` beside the cache).

``python -m maktaba_pipeline.rules --schema [FILE]`` writes the schema (every setting's
kind, default, help and limits) the Go API serves; a test keeps the checked-in copy
current.
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import os
import sys
import typing
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from . import settings
from .classify import settings_help as _detect_help  # noqa: F401 — registers the help
from .classify.books import BooksConfig
from .classify.clip_groups import GroupConfig
from .classify.display_title import DisplayTitleConfig
from .classify.series_detector import DetectorConfig
from .classify.tag_check import TagCheckConfig
from .classify.topic_tags import TaggerConfig
from .cluster.machines_settings import ClusterMachinesConfig
from .engine.failure_classes import QueueHealthConfig
from .enrich.config import EnrichConfig
from .relay import RelayConfig
from .schedule import settings_help as _planner_help  # noqa: F401
from .schedule.channel_planner import PlannerConfig
from .schedule.tv_settings import (
    ChannelAuditConfig,
    DizqueExportConfig,
    ExactDatesConfig,
    MaktabaTvClusterConfig,
    MaktabaTvConfig,
    MaktabaTvOutputConfig,
    MaktabaTvRemoteConfig,
    MaktabaTvSimulcastConfig,
    PlexLiveConfig,
    RepairConfig,
    TvStripConfig,
)
from .sharepath.auto import StorageConfig
from .stt.placement import TranscriptionConfig
from .tvrelease.config import MaktabaTvStagingConfig
from .updates_settings import UpdatesConfig

__all__ = ["SECTIONS", "config", "read", "schema"]

_G = "Grouping"
settings.register(GroupConfig, {
    "filler_max_minutes": settings.Help(_G, "Clip up to (min)", "Longer leftovers are "
                                        "stand-alone programmes (منفردات), shorter ones clips "
                                        "(filler).", minimum=0),
    "min_playlist": settings.Help(_G, "Playlist group from (videos)", minimum=1),
    "min_topic": settings.Help(_G, "Topic group from (videos)", minimum=1),
    "qa_phrases": settings.Help(_G, "Question / fatwa phrases", "A title with one of these goes "
                                "to \"فتاوى ومسائل\". A phrase matches the start of a word (ما "
                                "رأي → ما رأيك); end it with a space to match it only as a "
                                "whole word (\"حكم \" is not حكمة)."),
    "promo_words": settings.Help(_G, "Promo words", "A title with one of these advertises a "
                                 "show; it is linked to the show it names."),
})  # fmt: skip

#: section → (config class, title, what it decides)
SECTIONS: dict[str, tuple[type, str, str]] = {
    "planner": (PlannerConfig, "Channels", "how channels, slots, fillers and the Quran channel "
                "are planned"),
    "groups": (GroupConfig, "Grouping", "clip and stand-alone collections, promos, questions "
               "and fatwas"),
    "detect": (DetectorConfig, "Detection", "how shows, seasons and talk shows are found"),
    "tag": (TaggerConfig, "Tagging", "topic, format, audience and occasion tags, and what "
            "transcripts add to them"),
    "tagcheck": (TagCheckConfig, "Tag check", "a language model checks what each series is "
                 "about"),
    "books": (BooksConfig, "Books", "the book a lesson series explains decides its topics"),
    "dizquetv": (DizqueExportConfig, "dizqueTV export", "how the planned channels are written "
                 "into dizqueTV: removing what the plan dropped, rewriting a channel that is "
                 "on air"),
    "tv": (MaktabaTvConfig, "Maktaba TV", "how Maktaba's own channels air: filler between "
           "programmes, the Ramadan grids, live broadcasts, loudness, the guide's days "
           "ahead"),
    "tv_output": (MaktabaTvOutputConfig, "Maktaba TV output", "how a channel on air is "
                  "encoded: encoder, picture, bitrates, how many channels and NVENC "
                  "sessions at once"),
    "tv_cluster": (MaktabaTvClusterConfig, "Maktaba TV machines", "which machines encode "
                   "channels and how the home spreads them; one encode per channel"),
    "tv_remote": (MaktabaTvRemoteConfig, "Maktaba TV away from home", "who may watch "
                  "channels away from home through Cloudflare Stream, how many at once, the "
                  "monthly budget and the storage guard"),
    "tv_simulcast": (MaktabaTvSimulcastConfig, "Simulcast", "sending channels on to YouTube, "
                     "Facebook or any RTMP destination through Cloudflare: on or off, "
                     "destinations per channel, the monthly output-hours, the margins around "
                     "a scheduled window"),
    "tv_staging": (MaktabaTvStagingConfig, "TV staging", "every TV change is prepared next "
                   "to what is on the air, checked and approved before it goes on the air; "
                   "when channels switch, rollback, the hot standby"),
    "tv_strip": (TvStripConfig, "Channel strip", "the news-style bar across a channel's "
                 "picture: what is on now and next, and a clock rotating between cities"),
    "enrich": (EnrichConfig, "Finding metadata", "Find metadata and Re-enrich: Maktaba's "
               "own intel (transcripts, chapters, the book, file and folder names, sibling "
               "episodes) and a web search a language model reads; the daily caps"),
    "display_titles": (DisplayTitleConfig, "Display titles", "short, clean on-air titles "
                       "for the guide, the channel strip and the dizqueTV export"),
    "exactdates": (ExactDatesConfig, "Exact dates", "exact upload dates fetched from YouTube "
                   "for series members (off until switched on)"),
    "audit": (ChannelAuditConfig, "Channel audit", "is what each channel airs about the channel"),
    "plexlive": (PlexLiveConfig, "Plex Live TV", "Maktaba's channels in Plex: adding the tuner "
                 "and its guide, refreshing the guide and the channel list"),
    "repair": (RepairConfig, "Broken files", "downloads that cannot be read are fetched from "
               "YouTube again (off until switched on)"),
    "relay": (RelayConfig, "Extension relay", "machines run the Plex stages through the home, "
              "which adds the Plex token they never see; limits per machine"),
    "storage": (StorageConfig, "Canonical share paths", "the home registers the storage "
                "servers it mounts and converts the stored media paths to smb://server/"
                "share/... by itself: when, how it checks, what it pauses, the backups"),
    "updates": (UpdatesConfig, "Updates", "new signed releases installed by themselves in the "
                "quiet window, on the home first and then on every machine; channel, pin, "
                "defer, skip"),
    "queue_health": (QueueHealthConfig, "Queue health", "when a queue turns red, how often "
                     "a failure is tried, what waits on the model, a share or disk space "
                     "and how often those are checked, which failures are of which class"),
    "cluster": (ClusterMachinesConfig, "Machines", "every machine of the cluster as the same "
                "Maktaba: one sign-in for all of them, when a machine stops waiting for the "
                "home"),
    "transcription": (TranscriptionConfig, "Transcription", "which machines transcribe: "
                      "while the second listen is on, those that run it, unless machines "
                      "without it may too"),
}  # fmt: skip


#: section → {setting: environment variable}: the machine's own default for a setting
#: nobody changed. ``MAKTABA_OLLAMA_URL`` is the language model server the supervisor
#: hands every role (Maktaba.app's bundled Ollama on its own port, or ``[llm] url`` in
#: server.toml); every LLM stage reads its server from ``rules.tagcheck.url``.
ENV_DEFAULTS: dict[str, dict[str, str]] = {
    "tagcheck": {"url": "MAKTABA_OLLAMA_URL"},
}


def config(
    section: str, changes: Mapping[str, Any] | None, env: Mapping[str, str] | None = None
) -> tuple[Any, dict[str, str]]:
    """The section's config with the stored ``changes`` applied, each on its own: a value
    that cannot be used (an unknown name, a wrong shape) is skipped and returned with why,
    so one bad edit never stops a stage; the default applies for it.

    A setting in :data:`ENV_DEFAULTS` that was not changed takes its variable from
    ``env`` (default: this process's environment) when it is set: a value the user set
    always wins."""
    cls = SECTIONS[section][0]
    good: dict[str, Any] = {}
    bad: dict[str, str] = {}
    for name, value in (changes or {}).items():
        try:
            settings.apply(cls, {name: value})
            good[name] = value
        except settings.SettingsError as e:
            bad[name] = str(e)
    env = os.environ if env is None else env
    for name, var in ENV_DEFAULTS.get(section, {}).items():
        value = (env.get(var) or "").strip().rstrip("/")
        if name not in good and value:
            good[name] = value
    return settings.apply(cls, good), bad


async def read(db: Any, section: str) -> tuple[Any, dict[str, Any], dict[str, str]]:
    """The section's config from Maktaba's settings (``app_settings`` key
    ``rules.<section>``): (config, the stored changes, the ones skipped and why). A
    database without the table gives the defaults."""
    from .engine.sql import loads

    try:
        row = await db.fetchrow("SELECT value FROM app_settings WHERE key = $1", f"rules.{section}")
    except Exception as exc:
        # an old or bare database has no app_settings table: the defaults. Any other
        # error (a dropped connection, a timeout) is raised: running a stage, or
        # fingerprinting its inputs, with the defaults instead of the rules as set
        # would quietly write a different plan.
        if not _missing_table(exc):
            raise
        row = None
    stored = loads(row["value"]) if row is not None else None
    changes = stored if isinstance(stored, dict) else {}
    cfg, bad = config(section, changes)
    return cfg, changes, bad


def _missing_table(exc: BaseException) -> bool:
    """Postgres ``undefined_table`` (SQLSTATE 42P01) or SQLite's "no such table"."""
    if getattr(exc, "sqlstate", None) == "42P01" or type(exc).__name__ == "UndefinedTableError":
        return True
    return "no such table" in str(exc).lower()


def schema() -> dict[str, Any]:
    """Every section and setting with its kind, default, help and limits (no values)."""
    out = []
    for key, (cls, title, what) in SECTIONS.items():
        fields = settings.describe(cls())
        for f in fields:
            for k in ("value", "changed"):
                f.pop(k, None)
        help_ = settings._HELP.get(cls, {})  # noqa: SLF001 — the registry this module feeds
        hints = typing.get_type_hints(cls)
        for f in fields:
            h = help_.get(f["name"])
            if h is not None and h.time_columns:
                f["time_columns"] = list(h.time_columns)
            if f["kind"] == "rows":
                f["column_kinds"] = _column_kinds(hints[f["name"]], h)
                optional = _column_optional(hints[f["name"]])
                if any(optional):
                    f["column_optional"] = optional
        out.append({"key": key, "title": title, "what": what, "fields": fields})
    return {"sections": out}


def _column_kinds(tp: Any, h: settings.Help | None) -> list[str]:
    """The kind of each column of a list-of-rows setting (time, int, float, str, list …)."""
    item = typing.get_args(tp)[0]
    if dataclasses.is_dataclass(item):
        ih = typing.get_type_hints(item)
        return [
            "time" if f.name in ("start", "end") and ih[f.name] is int
            else settings._kind(ih[f.name])  # noqa: SLF001
            for f in dataclasses.fields(item)
        ]  # fmt: skip
    times = set(h.time_columns) if h else set()
    return [
        "time" if i in times else settings._kind(t)  # noqa: SLF001
        for i, t in enumerate(typing.get_args(item))
    ]


def _column_optional(tp: Any) -> list[bool]:
    """Which columns of a list-of-rows setting a row may leave out: those with a
    default, so a row saved before the column was added still applies."""
    item = typing.get_args(tp)[0]
    if not dataclasses.is_dataclass(item):
        return []
    missing = dataclasses.MISSING
    return [
        f.default is not missing or f.default_factory is not missing
        for f in dataclasses.fields(item)
    ]


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="python -m maktaba_pipeline.rules")
    p.add_argument("--schema", nargs="?", const="-", metavar="FILE",
                   help="write the rules schema (JSON) to FILE, or stdout")  # fmt: skip
    a = p.parse_args(argv)
    if a.schema is None:
        p.print_help()
        return 2
    text = json.dumps(schema(), ensure_ascii=False, indent=1) + "\n"
    if a.schema == "-":
        sys.stdout.write(text)
    else:
        Path(a.schema).write_text(text, encoding="utf-8", newline="\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
