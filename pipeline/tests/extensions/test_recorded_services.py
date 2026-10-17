"""Regression tests against responses recorded from real services (read-only smoke).

``tests/fixtures/recorded/*.json`` hold GET responses captured from a real Plex
server, a real dizqueTV and a real yt-dlp flat listing, with tokens, URLs, titles
and file paths replaced. They pin the shapes the extension code depends on: Plex's
``MediaContainer`` envelope and string rating keys, dizqueTV's ``/api/channels``
being a list of bare numbers, yt-dlp's ``_type: url`` flat entries and the
``timestamp`` that ``approximate_date`` adds.
"""

from __future__ import annotations

import json
from collections.abc import Mapping
from datetime import date
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.extensions.dizquetv.channels import changes_from, plan_actions
from maktaba_pipeline.extensions.dizquetv.shows import (
    DesiredShow,
    read_remote,
)
from maktaba_pipeline.extensions.dizquetv.shows import compute_changes as show_changes
from maktaba_pipeline.extensions.links import Link
from maktaba_pipeline.extensions.plex.collection import (
    Desired,
    PlexIndex,
    _read_remote,
    compute_changes,
)
from maktaba_pipeline.integrations.dizquetv import PREFIX, DizqueTV, read_state
from maktaba_pipeline.integrations.plex import PlexClient, TransportError
from maktaba_pipeline.integrations.youtube import FetchError, _entries, list_channel

from .test_dizquetv import make_plan

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


def test_plex_recorded_envelopes_parse(plex: PlexFixture) -> None:
    c, tr, rec = plex
    assert len(c.machine_identifier()) == 40
    sections = c.sections()
    assert {s.key for s in sections} == {rec["collection_section"], rec["movies_section"]}
    assert all(s.type == "movie" and s.locations for s in sections)
    movies = next(s for s in sections if s.key == rec["movies_section"])
    items = c.section_items(movies)
    assert items and all(i.rating_key.isdigit() and i.file.startswith("/") for i in items)
    assert all(i.section_key == movies.key and i.duration_ms for i in items)
    assert c.item_section(items[0].rating_key) == movies.key
    assert set(c.metadata([i.rating_key for i in items[:2]])) == {i.rating_key for i in items[:2]}
    # the token travels in a header and never in a URL
    assert all(h.get("X-Plex-Token") == TOKEN for _, _, h in tr.seen)
    assert not any(TOKEN in path for _, path, _ in tr.seen)
    assert {m for m, _, _ in tr.seen} == {"GET"}


def test_plex_recorded_collections_and_children(plex: PlexFixture) -> None:
    c, _, rec = plex
    cols = c.collections(rec["collection_section"])
    assert cols and all(col.rating_key.isdigit() and col.title for col in cols)
    children = c.collection_children(rec["collection_id"])
    assert children and all(k.isdigit() for k in children)
    assert c.collections(rec["movies_section"]) == []


def test_plex_recorded_path_index_is_case_insensitive(plex: PlexFixture) -> None:
    c, _, rec = plex
    movies = next(s for s in c.sections() if s.key == rec["movies_section"])
    items = c.section_items(movies)
    idx = PlexIndex()
    for it in items:
        idx.exact[it.file] = it
        idx.folded.setdefault(it.file.casefold(), it)
    assert idx.find(items[0].file) == items[0]
    assert idx.find(items[0].file.upper()) == items[0]


def test_plex_recorded_compute_changes_creates_then_detects_foreign_title(
    plex: PlexFixture,
) -> None:
    c, _, rec = plex
    movies = rec["movies_section"]
    section = next(s for s in c.sections() if s.key == movies)
    keys = tuple(i.rating_key for i in c.section_items(section))
    remote = _read_remote(c, movies, None, "Smoke")
    created = compute_changes(Desired("Smoke", keys[:3], movies), remote)
    assert created.meta["action"] == "create" and list(created.added) == list(keys[:3])

    # a collection that exists in Plex and was not made by this tool is never touched
    col_sec = rec["collection_section"]
    existing = c.collections(col_sec)[0]
    foreign = _read_remote(c, col_sec, None, existing.title)
    assert foreign.foreign_title and foreign.ours is None
    conflict = compute_changes(Desired(existing.title, ("1",), col_sec), foreign)
    assert conflict.meta["action"] == "conflict"

    # when the cached link says the tool made it, the same title is ours and diffed
    link = Link("plex_collection", "season", "s", existing.rating_key, existing.title, "h", True)
    ours = _read_remote(c, col_sec, link, existing.title)
    assert ours.ours is not None and not ours.foreign_title
    same = compute_changes(Desired(existing.title, ours.ours.children, col_sec), ours)
    assert not (same.added or same.removed or same.reordered or same.changed)


# ------------------------------------------------------------------ dizqueTV

DizqueFixture = tuple[DizqueTV, dict[str, Any]]


@pytest.fixture
def dizque() -> DizqueFixture:
    rec = _load("dizquetv.json")
    return DizqueTV("http://dizque.invalid", transport=RecordedTransport(rec["responses"])), rec


def test_dizquetv_channels_are_bare_numbers_and_state_reads(dizque: DizqueFixture) -> None:
    dz, rec = dizque
    assert all(isinstance(n, int) for n in dz.get("/api/channels"))
    assert dz.plex_server_name() == "Test-Server"
    st = read_state(dz)
    assert rec["first_channel"] in st.channels
    assert st.channels[rec["first_channel"]]["number"] == rec["first_channel"]
    assert st.server == "Test-Server"
    # shows the tool did not create are listed with keys=None and never read in full
    foreign = [v for k, v in st.shows.items() if not k.startswith(PREFIX)]
    assert foreign and all(v["keys"] is None for v in foreign)


def test_dizquetv_recorded_show_diff(dizque: DizqueFixture) -> None:
    dz, rec = dizque
    doc = rec["responses"][f"/api/show/{rec['first_show']}"]["body"]
    keys = tuple(str(p["ratingKey"]) for p in doc["content"])
    link = Link("dizquetv_show", "season", "s", rec["first_show"], doc["name"], "h", True)
    remote = read_remote(dz, link, doc["name"])
    assert remote.ours is not None and remote.ours.keys == keys and not remote.foreign_name
    assert not show_changes(DesiredShow(doc["name"], keys), remote).added
    assert show_changes(DesiredShow(doc["name"], keys[::-1]), remote).reordered == keys[::-1]
    # without our link the same name is someone else's show: left alone
    foreign = read_remote(dz, None, doc["name"])
    assert foreign.foreign_name
    assert show_changes(DesiredShow(doc["name"], keys), foreign).meta["action"] == "conflict"


def test_dizquetv_plan_against_recorded_state(dizque: DizqueFixture) -> None:
    dz, rec = dizque
    st = read_state(dz)
    fresh = plan_actions(make_plan(["1", "2"], number=900), st)
    assert {(a.verb, a.kind) for a in fresh} == {
        ("create", "filler"),
        ("create", "show"),
        ("create", "channel"),
    }
    assert changes_from(fresh).added
    taken = plan_actions(make_plan(["1", "2"], number=rec["first_channel"]), st)
    assert ("conflict", "channel") in {(a.verb, a.kind) for a in taken}  # a channel not ours


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
