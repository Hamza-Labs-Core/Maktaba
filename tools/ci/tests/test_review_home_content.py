"""Epic 58 story 58.04: the review home's content set. The shipped manifest is public domain or
CC BY only, every item has both titles, a source with its checksum and a licence, and the test
files cover every path a reviewer's TV hits; fetch verifies each source and makes the derived
files with the manifest's ffmpeg arguments."""

from __future__ import annotations

import copy
import hashlib
import http.server
import json
import shutil
import sys
import threading
from pathlib import Path
from typing import Any

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "review_home"))

import content  # noqa: E402


def test_the_shipped_manifest_is_valid() -> None:
    m = content.load()
    assert content.validate(m) == []
    covered = {c for it in m["items"] for c in it["covers"]}
    assert covered == content.COVERS
    # a short film library, a lecture series, an audiobook (the story's content), each item titled twice
    assert {it["library"] for it in m["items"]} == {"films", "films-older", "lectures", "audiobooks"}
    for it in m["items"]:
        assert content.has_arabic(it["title_ar"]) and content.has_latin(it["title_en"])
        assert it["licence"] in content.LICENCES


def test_the_arabic_subtitles_match_the_english_cues() -> None:
    subs = ROOT / "deploy" / "review-home" / "content" / "subtitles"
    en = [ln for ln in (subs / "cosmos-laundromat.en.vtt").read_text(encoding="utf-8").splitlines() if "-->" in ln]
    ar = [ln for ln in (subs / "cosmos-laundromat.ar.vtt").read_text(encoding="utf-8").splitlines() if "-->" in ln]
    assert en == ar and len(en) > 50


def _broken(edit: Any) -> list[str]:
    m = copy.deepcopy(content.load())
    edit(m)
    return content.validate(m)


@pytest.mark.parametrize(
    ("edit", "want"),
    [
        (lambda m: m["items"][0].update(title_ar="Spring"), "needs an Arabic title"),
        (lambda m: m["items"][0].update(licence="CC-BY-NC-4.0"), "not public domain or CC BY"),
        (lambda m: m["items"][0].update(licence_url="https://creativecommons.org/licenses/by/3.0/"), "is not CC-BY-4.0's"),
        (lambda m: m["items"][0]["sources"][0].update(sha1="abc"), "sha1 must be 40 hex"),
        (lambda m: m["items"][0]["sources"][0].update(url="http://archive.org/x.mp4"), "must be https"),
        (lambda m: m["items"][0].update(file="spring.mp3"), "not a type the home scans"),
        (lambda m: m["items"][0].update(file="../spring.mp4"), "not one plain file name"),
        (lambda m: m["items"][0].update(attribution=" "), "needs an attribution"),
        (lambda m: m["items"][1].update(covers=[]), "no item covers ['4k-hevc-hdr10']"),
        (lambda m: m["items"][1]["derive"].insert(0, "{src3}"), "names {src3}"),
        (lambda m: m["items"][1]["derive"].append("-y"), "ends with {out}"),
        (lambda m: m["items"][4]["subtitles"][1].update(path="subtitles/missing.vtt"), "does not exist"),
        (lambda m: m["items"][4].pop("derive"), "more than one input needs a derive step"),
        (lambda m: m["libraries"][0].update(age_tier=12), "age_tier 12"),
        (lambda m: m["channel"].update(library="nowhere"), "channel: library"),
        (lambda m: m["items"].append(copy.deepcopy(m["items"][0])), "named twice"),
    ],
)
def test_validate_names_each_problem(edit: Any, want: str) -> None:
    errs = _broken(edit)
    assert any(want in e for e in errs), errs


class _Files(http.server.BaseHTTPRequestHandler):
    files: dict[str, bytes] = {}
    hits: list[str] = []

    def do_GET(self) -> None:  # noqa: N802 - http.server's name
        self.hits.append(self.path)
        body = self.files.get(self.path)
        if body is None:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_: Any) -> None:
        pass


@pytest.fixture
def server() -> Any:
    _Files.files, _Files.hits = {}, []
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Files)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    yield f"http://127.0.0.1:{srv.server_address[1]}", _Files
    srv.shutdown()


def _item(base: str, iid: str, name: str, body: bytes, **kw: Any) -> dict[str, Any]:
    return {
        "id": iid,
        "library": "films",
        "file": name,
        "sources": [
            {"url": f"{base}/{iid}.src", "page": f"{base}/{iid}", "size": len(body), "sha1": hashlib.sha1(body).hexdigest()}  # noqa: S324
        ],
        "title_en": iid,
        "title_ar": "عنوان",
        "licence": "CC-BY-4.0",
        "licence_url": "https://creativecommons.org/licenses/by/4.0/",
        "attribution": f"{iid} by someone",
        **kw,
    }


def test_fetch_verifies_copies_derives_and_keeps(server: Any, tmp_path: Path) -> None:
    base, files = server
    files.files = {"/copy.src": b"plain bytes", "/made.src": b"to be derived"}
    m = {
        "libraries": [{"id": "films", "dir": "films"}],
        "items": [
            _item(base, "copy", "copy.src", b"plain bytes"),
            _item(base, "made", "made.mkv", b"to be derived", derive=["-i", "{src0}", "-c", "copy", "{out}"]),
        ],
    }
    calls: list[list[str]] = []

    def fake_ffmpeg(args: list[str], check: bool) -> None:
        calls.append(args)
        src, out = Path(args[args.index("-i") + 1]), Path(args[-1])
        assert out.suffix == ".mkv", "the partial file keeps the container's suffix"
        shutil.copyfile(src, out)

    made = content.fetch(m, tmp_path, run=fake_ffmpeg)
    assert [p.name for p in made] == ["copy.src", "made.mkv"]
    assert (tmp_path / "films" / "copy.src").read_bytes() == b"plain bytes"
    assert (tmp_path / "films" / "made.mkv").read_bytes() == b"to be derived"
    assert calls[0][:6] == ["ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y"]
    assert json.loads((tmp_path / "manifest.json").read_text(encoding="utf-8"))["items"][0]["id"] == "copy"
    assert "copy by someone" in (tmp_path / "LICENCES.md").read_text(encoding="utf-8")
    # a second run downloads nothing and makes nothing
    hits = len(files.hits)
    content.fetch(m, tmp_path, run=fake_ffmpeg)
    assert len(files.hits) == hits and len(calls) == 1


def test_fetch_refuses_a_source_whose_checksum_differs(server: Any, tmp_path: Path) -> None:
    base, files = server
    files.files = {"/bad.src": b"not what the manifest says"}
    m = {"libraries": [{"id": "films", "dir": "films"}], "items": [_item(base, "bad", "bad.src", b"what it says")]}
    m["items"][0]["sources"][0]["size"] = len(b"not what the manifest says")
    with pytest.raises(RuntimeError, match="the manifest says"):
        content.fetch(m, tmp_path)
    assert not (tmp_path / "films" / "bad.src").exists()
    assert not list((tmp_path / ".sources").rglob("*.part"))


def test_derive_args_fill_every_placeholder() -> None:
    args = content.derive_args(["-i", "{src0}", "-i", "{sub1}", "x={src0}", "{out}"], [Path("/s/a")], [Path("/v/0"), Path("/v/1")], Path("/o.mkv"))
    assert args[6:] == ["-i", "/s/a", "-i", "/v/1", "x=/s/a", "/o.mkv"]
