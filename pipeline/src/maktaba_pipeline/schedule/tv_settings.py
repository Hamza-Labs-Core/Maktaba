"""The settings of the TV-side stages that are not the planner's: the dizqueTV export, the
exact upload dates, the channel audit and the repair of broken downloads.

Each is a frozen config whose defaults are the behaviour the stand-alone mediaintel tool has
without its flags (:mod:`maktaba_pipeline.rules` lists them as sections ``dizquetv``,
``exactdates``, ``audit`` and ``repair``). What writes to the share or hits YouTube is off
until it is switched on here.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field

from maktaba_pipeline.settings import Help, register

__all__ = [
    "ChannelAuditConfig",
    "DizqueExportConfig",
    "ExactDatesConfig",
    "LiveSlot",
    "MaktabaTvConfig",
    "MaktabaTvOutputConfig",
    "PlexLiveConfig",
    "RepairConfig",
    "channel_numbers",
    "live_slots",
]

DZ = "dizqueTV export"
EX = "Exact dates"
AU = "Channel audit"
RP = "Repair"
TV = "Maktaba TV"
TVG = "Guide"
TVO = "Maktaba TV output"
TVN = "Hardware encoder"
TVC = "Channels on air"
TCL = "Streaming machines"
TCP = "Placement"
PL = "Plex Live TV"


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
    #: every channel's schedule is planned this many days ahead (what the guide shows)
    horizon_days: int = 7
    #: a channel is topped up when less than this many days are planned
    topup_below_days: int = 3


@dataclass(frozen=True)
class MaktabaTvOutputConfig:
    """How the streaming service encodes a channel on air. Read when a channel starts; a
    setting not changed here keeps the streaming host's ``MAKTABA_CHANNEL_*`` value, else
    this default."""

    #: ``auto``: on a Mac VideoToolbox when its hardware encoder answers a test encode,
    #: elsewhere NVENC when an NVIDIA GPU does; else libx264
    encoder: str = "auto"
    resolution: str = "1920x1080"
    fps: int = 30
    video_kbps: int = 6000
    audio_codec: str = "aac"
    audio_kbps: int = 192
    segment_seconds: int = 4
    #: channels this host keeps on air at once (one encode each, however many watch);
    #: 0: 16 with VideoToolbox, else 8
    max_active_channels: int = 0
    #: seconds a channel stays on air after its last viewer leaves
    warm_grace_seconds: int = 60
    #: NVENC sessions at once across all channels; past it a block is encoded by libx264
    nvenc_max_sessions: int = 6
    #: VideoToolbox sessions at once (0: no cap)
    videotoolbox_max_sessions: int = 0


@dataclass(frozen=True)
class MaktabaTvClusterConfig:
    """Maktaba TV across the cluster (Epic 32): which machines encode channels and how the
    home spreads them. One encode per channel in the whole cluster; the home stays the one
    address clients see and forwards each channel to the machine that encodes it."""

    #: machines other than the home may stream (each also needs "Stream on this machine")
    nodes_stream: bool = False
    #: ``home_first``: the home until it is full, then the least loaded machine;
    #: ``balanced``: always the least loaded machine
    policy: str = "home_first"
    #: home_first: the percent of the home's channels in use before others take channels
    spill_at: int = 100
    #: ``proxy``: the home forwards every HLS segment; ``redirect``: segments are sent
    #: straight from the encoding machine (playlists stay on the home)
    hls_mode: str = "proxy"
    #: move the channels of a machine that stopped answering to another one
    failover: bool = True
    #: seconds without a heartbeat before a machine counts as down
    dead_after_seconds: int = 15
    #: channel slug -> the machine name it prefers (one name)
    pins: Mapping[str, tuple[str, ...]] = field(default_factory=dict)


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
class PlexLiveConfig:
    """Maktaba's channels in Plex Live TV (the Plex extension's ``plex_livetv`` stage):
    what dizqueTV's "auto refresh guide / channels" do, plus adding the tuner and its
    XMLTV guide to Plex once. Nothing here deletes or replaces a DVR in Plex."""

    #: add Maktaba's tuner and a DVR with its XMLTV guide to Plex when Plex has none
    #: (off: only the "Add Maktaba to Plex" button does it; it changes the user's Plex DVRs)
    auto_register: bool = False
    #: ask Plex to reload Maktaba's guide when the schedule grew or changed
    auto_refresh_guide: bool = True
    #: push the channel map (which channels Plex shows) when the lineup changed
    auto_refresh_channels: bool = True
    #: a guide reload caused only by the schedule growing waits until this long after the
    #: last one (a channel change or "Refresh Plex now" reloads at once)
    guide_refresh_min_minutes: int = 60
    #: the tuner address Plex should use (http://host:port of the streaming service);
    #: empty: MAKTABA_PUBLIC_BASE_URL, else what /api/tv/info reported
    tuner_url: str = ""
    #: the XMLTV guide address Plex should read; empty: <tuner address>/xmltv.xml
    xmltv_url: str = ""
    #: the DVR's guide language (Plex's three-letter code)
    language: str = "ara"
    #: the guide's name in Plex
    guide_title: str = "Maktaba"


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
        "horizon_days": Help(
            TVG,
            "Guide days ahead",
            "Every channel's schedule is planned this many days ahead; the guide (XMLTV) "
            "Plex and the apps read shows as far.",
            minimum=1,
            maximum=31,
        ),
        "topup_below_days": Help(
            TVG,
            "Top up below (days)",
            "A channel with less than this many days planned is extended back to the days "
            "ahead above. Checked every few minutes.",
            minimum=1,
            maximum=31,
        ),
    },
)
register(
    MaktabaTvOutputConfig,
    {
        "encoder": Help(
            TVO,
            "Video encoder",
            "auto: on a Mac, Apple VideoToolbox when its hardware encoder answers a test "
            "encode; elsewhere the NVIDIA GPU (NVENC) when it does; else the processor "
            "(libx264). nvenc, videotoolbox or software force one.",
            choices=("auto", "nvenc", "videotoolbox", "software"),
        ),
        "resolution": Help(
            TVO,
            "Picture size",
            "Every programme is scaled to this (width x height); black bars keep its shape.",
            choices=("3840x2160", "2560x1440", "1920x1080", "1280x720", "1024x576", "854x480"),
        ),
        "fps": Help(TVO, "Frames per second", minimum=10, maximum=60),
        "video_kbps": Help(
            TVO,
            "Video bitrate (kbit/s)",
            "Per channel. 6000 suits 1080p; 3000 suits 720p.",
            minimum=300,
            maximum=50000,
        ),
        "audio_codec": Help(
            TVO,
            "Audio codec",
            "aac plays everywhere; ac3 for players that want Dolby Digital.",
            choices=("aac", "ac3"),
        ),
        "audio_kbps": Help(TVO, "Audio bitrate (kbit/s)", minimum=64, maximum=640),
        "segment_seconds": Help(
            TVO,
            "HLS segment (seconds)",
            "Shorter starts a channel sooner; longer is steadier on a slow network.",
            minimum=1,
            maximum=10,
        ),
        "max_active_channels": Help(
            TVC,
            "Channels on air at once",
            "On each streaming host. Viewers of the same channel share its one encode; a "
            "channel nobody watches makes way for a new one. 0: 16 with VideoToolbox (Apple "
            "Silicon has several media engines), else 8.",
            minimum=0,
            maximum=64,
        ),
        "warm_grace_seconds": Help(
            TVC,
            "Keep on air after the last viewer (seconds)",
            "So zapping back is instant.",
            minimum=0,
            maximum=3600,
        ),
        "nvenc_max_sessions": Help(
            TVN,
            "Most NVENC sessions",
            "A GeForce card encodes only a few streams at once. Past this many a block is "
            "encoded by the processor (libx264); the channel goes back to the GPU at its "
            "next programme once a session is free. The next programme starts 3 seconds "
            "early, so a channel holds two sessions for a moment.",
            minimum=1,
            maximum=64,
        ),
        "videotoolbox_max_sessions": Help(
            TVN,
            "Most VideoToolbox sessions",
            "Apple VideoToolbox sessions at once on a Mac; past this many a block is encoded "
            "by the processor (libx264) and the channel goes back at its next programme. 0: "
            "no limit.",
            minimum=0,
            maximum=64,
        ),
    },
)
register(
    MaktabaTvClusterConfig,
    {
        "nodes_stream": Help(
            TCL,
            "Other machines may stream",
            "Machines that joined this home may encode channels too, so more channels can "
            "be on air at once. Each machine also needs \"Stream on this machine\" on the "
            "Live TV page. Off: the home encodes every channel.",
        ),
        "policy": Help(
            TCP,
            "Where channels go",
            "home_first: the home until it is full (see below), then the machine with the "
            "most room. balanced: always the machine with the most room. A channel on air "
            "is never moved to even out the load.",
            choices=("home_first", "balanced"),
        ),
        "spill_at": Help(
            TCP,
            "Home full at (%)",
            "home_first: the share of the home's channels in use before other machines "
            "take new channels.",
            minimum=1,
            maximum=100,
        ),
        "hls_mode": Help(
            TCP,
            "Web video path",
            "proxy: the home forwards every piece of video (one address, works everywhere). "
            "redirect: the browser fetches the video pieces from the encoding machine "
            "itself; only when every viewer can reach every machine.",
            choices=("proxy", "redirect"),
        ),
        "failover": Help(
            TCP,
            "Move channels off a machine that stops",
            "A channel being watched on a machine that stops answering continues on another "
            "one at the same point of its schedule.",
        ),
        "dead_after_seconds": Help(
            TCP,
            "Machine down after (seconds)",
            "Without a heartbeat for this long a machine counts as down.",
            minimum=5,
            maximum=300,
        ),
        "pins": Help(
            TCP,
            "Preferred machine per channel",
            "Channel (its slug) -> the machine that should encode it while it is up and has "
            "room. Set it on the channel's page.",
        ),
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
register(
    PlexLiveConfig,
    {
        "auto_register": Help(
            PL,
            "Add Maktaba to Plex by itself",
            "When Plex has no DVR with Maktaba's tuner, add the tuner and a DVR that reads "
            'Maktaba\'s XMLTV guide. Off: only the "Add Maktaba to Plex" button does it. '
            "An existing DVR (dizqueTV's, an antenna's) is never changed or removed.",
        ),
        "auto_refresh_guide": Help(
            PL,
            "Refresh the guide in Plex",
            "Ask Plex to reload Maktaba's guide after the channels change and when the "
            'schedule grows (what dizqueTV\'s "auto refresh guide" does).',
        ),
        "auto_refresh_channels": Help(
            PL,
            "Refresh the channels in Plex",
            "Push the channel list to Plex's tuner after the channels change, so a new, "
            'renumbered or removed channel shows in Plex (dizqueTV\'s "auto refresh channels").',
        ),
        "guide_refresh_min_minutes": Help(
            PL,
            "Guide reload at most every (min)",
            "A reload caused only by the schedule growing waits this long after the last "
            'one. A channel change or "Refresh Plex now" reloads at once.',
            minimum=1,
        ),
        "tuner_url": Help(
            PL,
            "Tuner address for Plex",
            "http://host:port of Maktaba's streaming service as the Plex server reaches it. "
            "Empty: the public address Maktaba was started with (public_url).",
        ),
        "xmltv_url": Help(
            PL, "Guide address for Plex", "Empty: the tuner address followed by /xmltv.xml."
        ),
        "language": Help(PL, "Guide language", "Plex's three-letter code: ara, eng ..."),
        "guide_title": Help(PL, "Guide name in Plex"),
    },
)
