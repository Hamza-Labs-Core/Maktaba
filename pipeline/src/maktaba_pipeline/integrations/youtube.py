"""YouTube channel / playlist listing through yt-dlp (ported from ``scan_youtube.py``).

yt-dlp is an optional extra (``mediaintel``): it is imported lazily by
:class:`YtDlpFetcher` only, so importing this module never needs it. All the
listing logic runs against the small :class:`YouTubeFetcher` protocol, which
tests replace with a fake.

For each channel the listing is:

1. the tabs ``videos``, ``shorts``, ``streams`` and every playlist with its item
   order, in the original language (``lang``, default Arabic: YouTube
   auto-translates titles for the request locale);
2. approximate month-level dates from a second, English listing of the tabs.

Nothing is downloaded.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from typing import Any, Literal, Protocol

from ..discovery.ytdlp_sidecars import is_real_playlist, parse_upload_date

__all__ = [
    "BOT_MARKERS",
    "TABS",
    "BotCheck",
    "ChannelListing",
    "ChannelUnavailable",
    "FetchError",
    "PlaylistListing",
    "VideoEntry",
    "VideoInfo",
    "VideoInfoFetcher",
    "YouTubeFetcher",
    "YtDlpFetcher",
    "classify_error",
    "exact_info",
    "list_channel",
]

TABS = ("videos", "shorts", "streams")

#: YouTube asked to sign in ("confirm you're not a bot"): stop and back off
BOT_MARKERS = ("not a bot", "روبوت", "Sign in to confirm")
_GONE_MARKERS = ("does not exist", "unavailable", "terminated", "404")


class FetchError(Exception):
    """The fetcher could not extract a URL (message is yt-dlp's)."""


class BotCheck(RuntimeError):
    """YouTube's bot check: stop the run and back off."""


class ChannelUnavailable(Exception):
    """The channel could not be listed; ``status`` is the ``youtube_channels.status``."""

    def __init__(self, status: Literal["gone", "error"], message: str) -> None:
        super().__init__(message)
        self.status = status
        self.message = message


class YouTubeFetcher(Protocol):
    def extract(
        self, url: str, *, lang: str, flat: bool = True, approximate: bool = False
    ) -> dict[str, Any] | None:
        """yt-dlp ``extract_info`` result for ``url``.

        Returns None when the page has no such tab (a channel without shorts).
        Raises :class:`FetchError` for every other failure.
        """
        ...


class VideoInfoFetcher(Protocol):
    def video_info(
        self, youtube_id: str, *, lang: str, cookies_from_browser: str | None = None
    ) -> dict[str, Any] | None:
        """yt-dlp's page-level info for one video (``upload_date``, ``description`` ...), or
        None when the video cannot be read (private, removed). Raises :class:`BotCheck`
        on YouTube's bot check."""
        ...


class YtDlpFetcher:
    """The real fetcher; needs ``pip install yt-dlp`` (the ``mediaintel`` extra)."""

    def __init__(self) -> None:
        import yt_dlp  # type: ignore[import-untyped,unused-ignore]

        self._yt_dlp: Any = yt_dlp

    def extract(
        self, url: str, *, lang: str, flat: bool = True, approximate: bool = False
    ) -> dict[str, Any] | None:
        ex: dict[str, dict[str, list[str]]] = {
            "youtube": {"lang": [lang], "skip": ["dash", "hls", "translated_subs"]}
        }
        if approximate:
            ex["youtubetab"] = {"approximate_date": [""]}
        opts: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "skip_download": True,
            "ignoreerrors": False,
            "extractor_args": ex,
            "socket_timeout": 30,
            "retries": 5,
        }
        if flat:
            opts["extract_flat"] = "in_playlist"
        try:
            with self._yt_dlp.YoutubeDL(opts) as ydl:
                info = ydl.extract_info(url, download=False)
        except self._yt_dlp.utils.DownloadError as exc:
            msg = str(exc)
            if "does not have a" in msg and "tab" in msg:  # no shorts / streams tab
                return None
            raise FetchError(msg) from exc
        return info if isinstance(info, dict) else None

    def video_info(
        self, youtube_id: str, *, lang: str, cookies_from_browser: str | None = None
    ) -> dict[str, Any] | None:
        opts: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "skip_download": True,
            "ignoreerrors": False,
            "extractor_args": {
                "youtube": {"lang": [lang], "skip": ["dash", "hls", "translated_subs"]}
            },
            "socket_timeout": 30,
            "retries": 5,
        }
        if cookies_from_browser:
            opts["cookiesfrombrowser"] = (cookies_from_browser,)
        try:
            with self._yt_dlp.YoutubeDL(opts) as ydl:
                info = ydl.extract_info(
                    f"https://www.youtube.com/watch?v={youtube_id}", download=False, process=False
                )
        except self._yt_dlp.utils.DownloadError as exc:
            if classify_error(str(exc)) == "bot_check":
                raise BotCheck(str(exc)) from exc
            return None
        return info if isinstance(info, dict) else None


@dataclass(frozen=True, slots=True)
class VideoInfo:
    """What a per-video fetch gives: the exact upload date and the description."""

    upload_date: date | None
    description: str | None


@dataclass(frozen=True, slots=True)
class VideoEntry:
    youtube_id: str
    title: str
    duration_s: float | None
    tab: str
    #: approximate, month-level (from the English listing); None when unknown
    upload_date: date | None = None


@dataclass(frozen=True, slots=True)
class PlaylistListing:
    playlist_id: str
    title: str
    is_real: bool
    #: playlist items in order (position = index + 1)
    items: tuple[str, ...]


@dataclass(slots=True)
class ChannelListing:
    channel_id: str
    url: str
    title: str | None
    videos: list[VideoEntry] = field(default_factory=list)
    playlists: list[PlaylistListing] = field(default_factory=list)
    #: playlist entries are also videos (tab ``playlist``), kept apart from the tabs
    playlist_videos: list[VideoEntry] = field(default_factory=list)


def exact_info(info: dict[str, Any]) -> VideoInfo:
    """The exact upload date and the description out of :meth:`VideoInfoFetcher.video_info`."""
    desc = info.get("description")
    return VideoInfo(
        parse_upload_date(info.get("upload_date")),
        desc if isinstance(desc, str) and desc else None,
    )


def classify_error(message: str) -> Literal["bot_check", "gone", "error"]:
    if any(m in message for m in BOT_MARKERS):
        return "bot_check"
    if any(m in message for m in _GONE_MARKERS):
        return "gone"
    return "error"


def _entries(info: dict[str, Any] | None, tab: str) -> list[VideoEntry]:
    out: list[VideoEntry] = []
    for e in (info or {}).get("entries") or []:
        if not isinstance(e, dict):
            continue
        vid, title = e.get("id"), e.get("title")
        if not isinstance(vid, str) or not isinstance(title, str) or len(vid) != 11:
            continue
        dur = e.get("duration")
        out.append(
            VideoEntry(vid, title, float(dur) if isinstance(dur, int | float) else None, tab)
        )
    return out


def _extract(
    fetcher: YouTubeFetcher, url: str, *, lang: str, approximate: bool = False
) -> dict[str, Any] | None:
    try:
        return fetcher.extract(url, lang=lang, approximate=approximate)
    except FetchError as exc:
        if classify_error(str(exc)) == "bot_check":
            raise BotCheck(str(exc)) from exc
        raise


def list_channel(
    fetcher: YouTubeFetcher,
    channel_id: str,
    url: str,
    *,
    lang: str = "ar",
    playlists: bool = True,
) -> ChannelListing:
    """List a channel's tabs and playlists.

    Raises :class:`BotCheck` on YouTube's bot check and :class:`ChannelUnavailable`
    when the channel is gone or cannot be read.
    """
    base = url.rstrip("/")
    try:
        about = _extract(fetcher, base + "/videos", lang=lang)
        channel = (about or {}).get("channel") or (about or {}).get("uploader")
        listing = ChannelListing(channel_id, base, channel if isinstance(channel, str) else None)
        by_id: dict[str, VideoEntry] = {}
        for tab in TABS:
            info = about if tab == "videos" else _extract(fetcher, f"{base}/{tab}", lang=lang)
            for v in _entries(info, tab):
                by_id.setdefault(v.youtube_id, v)

        # approximate dates (English listing of the tabs)
        approx: dict[str, date] = {}
        for tab in TABS:
            info = _extract(fetcher, f"{base}/{tab}", lang="en", approximate=True)
            for ent in (info or {}).get("entries") or []:
                ts = ent.get("timestamp") if isinstance(ent, dict) else None
                if (
                    isinstance(ent, dict)
                    and isinstance(ent.get("id"), str)
                    and isinstance(ts, int | float)
                ):
                    approx[ent["id"]] = datetime.fromtimestamp(ts, UTC).date()
        listing.videos = [
            VideoEntry(v.youtube_id, v.title, v.duration_s, v.tab, approx.get(v.youtube_id))
            for v in by_id.values()
        ]

        if playlists:
            pl = _extract(fetcher, base + "/playlists", lang=lang)
            for p in (pl or {}).get("entries") or []:
                pid = p.get("id") if isinstance(p, dict) else None
                if not isinstance(pid, str):
                    continue
                try:
                    items = _extract(
                        fetcher, f"https://www.youtube.com/playlist?list={pid}", lang=lang
                    )
                except FetchError:
                    continue  # one unreadable playlist must not lose the channel
                if not items:
                    continue
                entries = [e for e in items.get("entries") or [] if isinstance(e, dict)]
                ids = tuple(e["id"] for e in entries if isinstance(e.get("id"), str))
                title = items.get("title") or p.get("title") or pid
                listing.playlists.append(
                    PlaylistListing(pid, str(title), is_real_playlist(pid), ids)
                )
                listing.playlist_videos.extend(_entries(items, "playlist"))
    except FetchError as exc:
        raise ChannelUnavailable(
            "gone" if classify_error(str(exc)) == "gone" else "error", str(exc)[:500]
        ) from exc
    return listing
