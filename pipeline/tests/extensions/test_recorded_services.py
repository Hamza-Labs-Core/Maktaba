"""Regression tests against responses recorded from real services (read-only smoke).

``tests/fixtures/recorded/*.json`` hold GET responses captured from a real Plex
server and a real yt-dlp flat listing, with tokens, URLs, titles and file paths
replaced. They pin the shapes the code depends on: Plex's ``MediaContainer``
envelope, yt-dlp's ``_type: url`` flat entries and the ``timestamp`` that
``approximate_date`` adds.
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from datetime import date
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.integrations.plex import PlexClient, TransportError
from maktaba_pipeline.integrations.youtube import FetchError, _entries, list_channel

_DIR = Path(__file__).resolve().parents[1] / "fixtures" / "recorded"
TOKEN = "fixture-token-not-real"


def _load(name: str) -> dict[str, Any]:
    data: dict[str, Any] = json.loads((_DIR / name).read_text(encoding="utf-8"))
    return data


class RecordedTransport:
    """Replays recorded GET responses by path; records the headers it was sent."""

    def __init__(self, responses: Mapping[str, Any]) -> None:
        self.responses = responses
        self.seen: list[tuple[str, str, Mapping[str, str]]] = []

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        path = "/" + url.split("//", 1)[1].split("/", 1)[1]
        self.seen.append((method, path, headers))
        if method != "GET" or path not in self.responses:
            raise TransportError(f"not recorded: {method} {path}")
        r = self.responses[path]
        return int(r["status"]), json.dumps(r["body"]).encode()


# ------------------------------------------------------------------ Plex

PlexFixture = tuple[PlexClient, RecordedTransport, dict[str, Any]]


@pytest.fixture
def plex() -> PlexFixture:
    rec = _load("plex.json")
    tr = RecordedTransport(rec["responses"])
    return PlexClient("http://plex.invalid:32400", TOKEN, transport=tr), tr, rec


def test_plex_recorded_envelope_parses(plex: PlexFixture) -> None:
    c, tr, _ = plex
    assert len(c.machine_identifier()) == 40
    c.ping()
    # the token travels in a header and never in a URL
    assert all(h.get("X-Plex-Token") == TOKEN for _, _, h in tr.seen)
    assert not any(TOKEN in path for _, path, _ in tr.seen)
    assert {m for m, _, _ in tr.seen} == {"GET"}


# ------------------------------------------------------------------ yt-dlp


class RecordedFetcher:
    """Serves the recorded flat listing for the videos tab and nothing for the other tabs."""

    def __init__(self) -> None:
        rec = _load("youtube_flat.json")
        self.plain, self.approx = rec["videos"], rec["videos_approximate"]
        self.calls: list[tuple[str, str, bool]] = []

    def extract(
        self, url: str, *, lang: str, flat: bool = True, approximate: bool = False
    ) -> dict[str, Any] | None:
        self.calls.append((url, lang, approximate))
        if url.endswith("/videos"):
            doc: dict[str, Any] = self.approx if approximate else self.plain
            return doc
        if url.endswith("/playlists"):
            return {"_type": "playlist", "entries": []}
        if url.endswith(("/shorts", "/streams")):
            return None  # a channel without that tab
        raise FetchError(f"unexpected url {url}")


def test_youtube_flat_entries_parse() -> None:
    rec = _load("youtube_flat.json")["videos"]
    vids = _entries(rec, "videos")
    assert len(vids) == len(rec["entries"]) > 0
    assert all(len(v.youtube_id) == 11 and v.duration_s for v in vids)


def test_youtube_listing_merges_approximate_dates() -> None:
    f = RecordedFetcher()
    listing = list_channel(f, "UCx", "https://www.youtube.com/@example", lang="ar")
    assert listing.videos and {v.tab for v in listing.videos} == {"videos"}
    assert all(isinstance(v.upload_date, date) for v in listing.videos)
    # titles are listed in the requested language, the dates in English with approximate_date
    assert ("https://www.youtube.com/@example/videos", "ar", False) in f.calls
    assert ("https://www.youtube.com/@example/videos", "en", True) in f.calls
