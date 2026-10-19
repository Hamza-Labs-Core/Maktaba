"""The settings of the TV-side stages that are not the planner's: the dizqueTV export, the
exact upload dates, the channel audit and the repair of broken downloads.

Each is a frozen config whose defaults are the behaviour the stand-alone mediaintel tool has
without its flags (:mod:`maktaba_pipeline.rules` lists them as sections ``dizquetv``,
``exactdates``, ``audit`` and ``repair``). What writes to the share or hits YouTube is off
until it is switched on here.
"""

from __future__ import annotations

from dataclasses import dataclass

from maktaba_pipeline.settings import Help, register

__all__ = [
    "ChannelAuditConfig",
    "DizqueExportConfig",
    "ExactDatesConfig",
    "LiveSlot",
    "MaktabaTvConfig",
    "RepairConfig",
    "channel_numbers",
    "live_slots",
]

DZ = "dizqueTV export"
EX = "Exact dates"
AU = "Channel audit"
RP = "Repair"
TV = "Maktaba TV"


@dataclass(frozen=True)
class LiveSlot:
    """A live broadcast a channel carries at set times (Al Jazeera's news on the hour on
    the news channel): from ``start`` every ``every_minutes`` until ``end`` (00:00: until
    midnight), ``minutes`` long, on ``days`` (``mon`` ... ``sun``; none: every day)."""

    channel: int = 0
    #: a YouTube live page (``https://www.youtube.com/@aljazeera/live``) or a stream address
    url: str = ""
    #: what the guide shows
    title: str = ""
    start: int = 0
    end: int = 0
    every_minutes: int = 60
    minutes: int = 10
    days: tuple[str, ...] = ()


@dataclass(frozen=True)
class DizqueExportConfig:
    #: delete what this tool made earlier and the plan no longer has (its channels, its
    #: ``maktaba-plan · `` shows and filler lists); never anything else
    prune: bool = False
    #: with prune: also delete every other channel that is not in the plan (a fresh lineup)
    clear_channels: bool = False
    #: take over a channel, show or filler list of the same number / name that this tool did
    #: not make (it is left alone otherwise and reported as a conflict)
    replace_existing: bool = False
    #: use the Ramadan grids
    ramadan: bool = False
    #: a rewrite keeps what is on air and each show's queue where the channel left it
    keep_airing: bool = True
    #: only these channel numbers (comma separated); empty: all. Nothing is pruned then.
    only_channels: str = ""
    #: live broadcasts in channels' grids (none: the grids as planned)
    live: tuple[LiveSlot, ...] = ()


@dataclass(frozen=True)
class MaktabaTvConfig:
    """How Maktaba's own scheduler airs the planned channels (what dizqueTV does for the
    dizqueTV export: filler lists by weight and cooldown, the Ramadan grids, live
    broadcasts, the channel's look)."""

    #: at most this many clips fill one gap (0: as many as fit, as dizqueTV); the rest
    #: of the gap is the slate
    max_filler_per_gap: int = 0
    #: a slot's own filler pool (its shows' promos, its topics) is tried before the
    #: channel's weighted filler lists; off: the channel's lists only, as dizqueTV
    slot_filler_first: bool = True
    #: the Ramadan grid during the Hijri month of Ramadan (a channel without one keeps
    #: its regular grid)
    ramadan_grids: bool = True
    #: days added to the computed Hijri date (the tabular calendar can be a day off the
    #: sighted month)
    hijri_adjust_days: int = 0
    #: live broadcasts in the channels' grids; empty: the dizqueTV export's (rules
    #: dizquetv.live), so one list serves both
    live: tuple[LiveSlot, ...] = ()
    #: even loudness across programmes and filler
    loudnorm: bool = True
    loudnorm_target_lufs: float = -16.0


def live_slots(tv: MaktabaTvConfig | None, dz: DizqueExportConfig | None) -> tuple[LiveSlot, ...]:
    """The live broadcasts both Maktaba TV and the dizqueTV export carry: Maktaba TV's own
    list, else the dizqueTV export's (``rules.dizquetv.live`` keeps working)."""
    if tv is not None and tv.live:
        return tv.live
    return dz.live if dz is not None else ()


def channel_numbers(text: str) -> set[int]:
    """``"101, 105"`` -> ``{101, 105}`` (anything that is not a number is ignored)."""
    return {int(x) for x in (p.strip() for p in text.split(",")) if x.isdigit()}


@dataclass(frozen=True)
class ExactDatesConfig:
    #: off: no video is asked of YouTube
    enabled: bool = False
    #: ``series``: members of a series (and videos of a playlist); ``all``: every linked video
    scope: str = "series"
    #: seconds between two requests (randomised 0.5x to 1.5x): be polite
    delay_s: float = 3.0
    lang: str = "ar"
    #: ``firefox``, ``chrome`` ...: a signed-in browser session lifts most limits
    cookies_from_browser: str = ""
    #: at most this many videos a run (0: all); the rest waits for the next run
    max_per_run: int = 0


@dataclass(frozen=True)
class ChannelAuditConfig:
    enabled: bool = True
    #: what airs in this many days from now is weighed
    window_days: int = 28
    #: audits kept (older ones are deleted)
    keep: int = 5


@dataclass(frozen=True)
class RepairConfig:
    #: off: nothing is downloaded and no file on the share is touched
    enabled: bool = False
    cookies_from_browser: str = ""
    max_height: int = 1080
    h264_first: bool = True
    #: keep the broken file as ``<name>.broken`` beside the new one
    keep_broken_copy: bool = True
    #: at most this many files a run (0: all)
    max_per_run: int = 0
    download_timeout_minutes: int = 60


register(
    DizqueExportConfig,
    {
        "prune": Help(
            DZ,
            "Remove what the plan no longer has",
            "Delete the channels, shows and filler lists this tool made earlier and the plan "
            "does not have now. Only what carries this tool's mark is touched.",
        ),
        "clear_channels": Help(
            DZ,
            "Also remove other channels not in the plan",
            "With the setting above: every channel that is not in the plan goes, "
            "including the ones made by hand. For a fresh lineup.",
        ),
        "replace_existing": Help(
            DZ,
            "Take over what exists",
            "A channel, show or filler list that is there under the same number or name but "
            "was not made by this tool is replaced. Off: it is left alone and reported.",
        ),
        "ramadan": Help(
            DZ, "Use the Ramadan grids", "A channel without one uses its regular grid."
        ),
        "keep_airing": Help(
            DZ,
            "Rewrite without a break",
            "A rewrite lets the programme on air finish, goes on with each show where it "
            "stopped, drops no programme and leaves no gap; a channel whose schedule and "
            "programmes did not change is not saved at all (nobody watching is restarted). "
            "Off: the timeline is rebuilt from the start.",
        ),
        "only_channels": Help(
            DZ,
            "Only these channels",
            "Channel numbers separated by commas (101, 105). Empty: all of them.",
        ),
        "live": Help(
            DZ,
            "Live broadcasts",
            "A live stream a channel carries at set times, such as the news on the hour: "
            "channel number, the YouTube live page (https://www.youtube.com/@aljazeera/live) "
            "or a stream address, the guide title, the first start (HH:MM), the end (00:00: "
            "until midnight), how often (minutes), how long (minutes) and the days (mon ... "
            "sun; empty: every day). What airs before it finishes first or makes way: "
            "no programme is cut and the broadcast starts on time.",
        ),
    },
)
register(
    MaktabaTvConfig,
    {
        "max_filler_per_gap": Help(
            TV,
            "Most clips in a gap",
            "A gap between programmes is filled with clips chosen by their lists' weights; "
            "past this many the rest of the gap shows the slate. 0: as many as fit.",
            minimum=0,
        ),
        "slot_filler_first": Help(
            TV,
            "A slot's own filler first",
            "Pad a slot with its own shows' promos and topic clips before the channel's "
            "filler lists. Off: the channel's lists only (what dizqueTV does).",
        ),
        "ramadan_grids": Help(
            TV,
            "Ramadan grids in Ramadan",
            "During the Hijri month of Ramadan a channel airs its Ramadan grid. A channel "
            "without one keeps its regular grid.",
        ),
        "hijri_adjust_days": Help(
            TV,
            "Hijri date adjustment (days)",
            "Added to the computed Hijri date when the month starts a day earlier or later "
            "where you are.",
        ),
        "live": Help(
            TV,
            "Live broadcasts",
            "A live stream a channel carries at set times: channel number, the YouTube live "
            "page or a stream address, the guide title, the first start (HH:MM), the end "
            "(00:00: until midnight), how often (minutes), how long (minutes) and the days "
            "(mon ... sun; empty: every day). No programme runs into it: what would not end "
            "before it is not started, filler fills up to it and the show goes on after. "
            "Empty: the dizqueTV export's live broadcasts.",
        ),
        "loudnorm": Help(TV, "Even loudness", "Normalise every programme's loudness."),
        "loudnorm_target_lufs": Help(TV, "Loudness target (LUFS)"),
    },
)
register(
    ExactDatesConfig,
    {
        "enabled": Help(
            EX,
            "Fetch exact upload dates",
            "Ask YouTube for the exact upload date of each series member whose date is only "
            "approximate (month level). Slow and polite; a bot check stops the run and the "
            "next one goes on.",
        ),
        "scope": Help(
            EX,
            "Which videos",
            "series: members of a series and videos in a playlist. all: every linked video.",
            choices=("series", "all"),
        ),
        "delay_s": Help(EX, "Seconds between requests", minimum=0),
        "lang": Help(EX, "Language", "The language YouTube is asked in (titles are translated)."),
        "cookies_from_browser": Help(
            EX,
            "Use a signed-in browser",
            "firefox, chrome ...: yt-dlp uses that browser's YouTube session.",
        ),
        "max_per_run": Help(EX, "Most videos a run", "0: all of them.", minimum=0),
    },
)
register(
    ChannelAuditConfig,
    {
        "enabled": Help(
            AU,
            "Audit the channels",
            "After the channels are written, check that "
            "what each airs is about the channel (reads dizqueTV, writes nothing).",
        ),
        "window_days": Help(
            AU, "Days weighed", "What airs from now for this many days.", minimum=1
        ),
        "keep": Help(AU, "Audits kept", minimum=1),
    },
)
register(
    RepairConfig,
    {
        "enabled": Help(
            RP,
            "Repair broken downloads",
            "A file whose probe failed and that came from YouTube is downloaded again, "
            "checked, and swapped in on the share. Off: nothing is downloaded or changed.",
        ),
        "cookies_from_browser": Help(RP, "Use a signed-in browser", "firefox, chrome ..."),
        "max_height": Help(RP, "Tallest picture", "At most this many pixels high.", minimum=144),
        "h264_first": Help(
            RP,
            "H.264 first",
            "Take H.264 when YouTube has it (what the rest of the library is and what "
            "the GPU decodes); YouTube's 1080p is often AV1.",
        ),
        "keep_broken_copy": Help(
            RP, "Keep the broken file", "As <name>.broken beside the new one."
        ),
        "max_per_run": Help(RP, "Most files a run", "0: all of them.", minimum=0),
        "download_timeout_minutes": Help(RP, "Download time limit (minutes)", minimum=1),
    },
)
