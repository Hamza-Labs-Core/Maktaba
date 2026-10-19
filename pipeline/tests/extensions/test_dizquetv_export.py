"""The code the engine's dizqueTV stage and the mediaintel exporter share."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.integrations.dizquetv import image_upload_body
from maktaba_pipeline.integrations.dizquetv_export import (
    ReadbackMismatch,
    hosted_link,
    removals,
    resolve_slots,
    write_channel,
)

HOUR = 3_600_000


def sched(*slots: tuple[int, str], pad: int = 1) -> dict[str, Any]:
    return {
        "period": 24 * HOUR,
        "maxDays": 365,
        "pad": pad,
        "slots": [{"time": t, "showId": s, "order": "next"} for t, s in slots],
    }


def test_slots_resolve_to_shows_and_a_lane_without_a_show_is_flex() -> None:
    got = resolve_slots(sched((0, "lane:a"), (HOUR, "lane:b")), {"a": "ID1"}, -120)
    assert [(s["showId"], s["order"]) for s in got["slots"]] == [
        ("custom.ID1", "next"),
        ("flex.", "shuffle"),
    ]
    assert got["timeZoneOffset"] == -120


def test_adjacent_slots_of_a_show_merge_and_the_last_one_wraps_into_the_first() -> None:
    s = sched((0, "lane:a"), (HOUR, "lane:a"), (2 * HOUR, "lane:b"), (3 * HOUR, "lane:a"))
    got = resolve_slots(s, {"a": "A", "b": "B"}, 0)
    assert [x["time"] for x in got["slots"]] == [2 * HOUR, 3 * HOUR]  # 0:00 belongs to 3:00's run
    padded = resolve_slots({**s, "pad": HOUR}, {"a": "A", "b": "B"}, 0)
    assert len(padded["slots"]) == 4  # one episode a slot: every slot stays


def test_removals_touch_only_what_this_tool_made() -> None:
    gone = removals(
        {101},
        {101: {"maktabaPlan": {}}, 150: {"name": "old", "maktabaPlan": {}}, 7: {"name": "mine"}},
        {"p · kept": {"id": "s1"}, "p · old": {"id": "s2"}, "mine": {"id": "s3"}},
        {"p · list": {"id": "f1"}},
        {"p · kept"},
        "p · ",
    )
    assert [(r.kind, r.name) for r in gone] == [
        ("channel", "#150 old"),
        ("show", "p · old"),
        ("filler", "p · list"),
    ]
    assert gone[0].body == {"number": 150} and gone[1].endpoint == "/api/show/s2"
    every = removals({101}, {7: {"name": "mine"}}, {}, {}, set(), "p · ", clear_channels=True)
    assert [r.name for r in every] == ["#7 mine"]


def test_a_hosted_logo_must_answer() -> None:
    with pytest.raises(RuntimeError, match="not at"):
        hosted_link("https://h", __file__, lambda u: 404)


def test_hosted_link_is_versioned_by_the_picture(tmp_path: Any) -> None:
    f = tmp_path / "a b.png"
    f.write_bytes(b"one")
    first = hosted_link("https://h", str(f), lambda u: 200)
    assert first.startswith("https://h/a%20b.png?v=")
    f.write_bytes(b"two")
    assert hosted_link("https://h", str(f), lambda u: 200) != first


def test_the_image_form_names_the_file_and_its_type() -> None:
    ctype, body = image_upload_body(b"GIF89", "maktaba-wm.gif")
    boundary = ctype.split("boundary=")[1]
    assert f"--{boundary}".encode() in body and b'filename="maktaba-wm.gif"' in body
    assert b"Content-Type: image/gif" in body and b"GIF89" in body


class Dz:
    """``call(method, path, body)`` over a dict of channels and shows."""

    def __init__(self, old: dict[str, Any] | None = None, *, drop_icon: bool = False) -> None:
        self.old = old
        self.drop_icon = drop_icon
        self.saved: dict[str, Any] | None = None
        self.calls: list[tuple[str, str]] = []

    def call(self, method: str, path: str, body: Any = None) -> Any:
        self.calls.append((method, path))
        if path == "/api/channel/5" and method == "GET":
            return self.old
        if path == "/api/show/S":
            return {
                "name": "Lane",
                "content": [
                    {"ratingKey": "1", "duration": 600_000},
                    {"ratingKey": "2", "duration": 600_000},
                ],
            }
        if path.endswith("time-slots"):
            return {"programs": body["programs"], "startTime": "2026-01-01T00:00:00Z"}
        if path == "/api/channel":
            self.saved = body
            return {}
        if path == "/api/channel/programless/5":
            back = {k: v for k, v in (self.saved or {}).items() if k != "programs"}
            if self.drop_icon:
                back["icon"] = ""
            return back
        raise AssertionError(path)


def body(**extra: Any) -> dict[str, Any]:
    return {
        "number": 5,
        "name": "Five",
        "programs": "<computed>",
        "fillerCollections": [],
        "maktabaPlan": {"key": "k", "hash": "h"},
        "scheduleBackup": {
            "period": 24 * HOUR,
            "maxDays": 2,
            "slots": [{"time": 0, "showId": "custom.S", "order": "next"}],
        },  # fmt: skip
        **extra,
    }


def test_a_new_channel_is_computed_saved_and_read_back() -> None:
    dz = Dz()
    got = write_channel(dz.call, body(), "POST", exists=False, shows={})
    assert got.status == "written" and got.programmes >= 2
    assert dz.saved is not None and dz.saved["maktabaPlan"]["fingerprint"]
    assert ("POST", "/api/channel-tools/time-slots") in dz.calls


def test_a_channel_with_nothing_playable_is_skipped() -> None:
    dz = Dz()
    b = body()
    b["scheduleBackup"]["slots"] = [{"time": 0, "showId": "flex.", "order": "shuffle"}]
    assert write_channel(dz.call, b, "POST", exists=False, shows={}).status == "skipped"
    assert dz.saved is None


def test_the_same_timeline_is_left_as_it_airs() -> None:
    first = Dz()
    write_channel(first.call, body(), "POST", exists=False, shows={})
    assert first.saved is not None
    old = {**first.saved, "startTime": "2025-12-01T00:00:00Z"}
    again = Dz(old)
    got = write_channel(
        again.call, body(name="Renamed"), "PUT", exists=True, shows={}, keep_airing=True
    )
    assert got.status == "written"  # the name differs: saved …
    assert again.saved is not None
    assert again.saved["programs"] == old["programs"]  # … but with the programmes as they air
    assert again.saved["startTime"] == "2025-12-01T00:00:00Z"
    assert ("POST", "/api/channel-tools/time-slots") not in again.calls
    final = Dz(again.saved)  # nothing differs any more: no save at all (a save restarts streams)
    same = write_channel(final.call, body(name="Renamed"), "PUT", exists=True, shows={})
    assert same.status == "unchanged" and final.saved is None


def test_a_logo_that_did_not_stick_is_a_failure() -> None:
    dz = Dz(drop_icon=True)
    with pytest.raises(ReadbackMismatch, match="logo"):
        write_channel(dz.call, body(icon="http://x/l.png"), "POST", exists=False, shows={})
