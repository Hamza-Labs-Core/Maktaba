"""Downloading a broken file again and swapping it in: no yt-dlp, ffmpeg or network."""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from maktaba_pipeline.library_mgmt import repair


@dataclass
class Proc:
    returncode: int = 0
    stdout: bytes = b""
    stderr: bytes = b""


class FakeTools:
    """``subprocess.run`` for yt-dlp, ffmpeg and ffprobe."""

    def __init__(self, *, ytdlp: Proc | None = None, container: str = "mp4", good: bool = True):
        self.ytdlp = ytdlp or Proc()
        self.container = container
        self.good = good
        self.calls: list[list[str]] = []

    def __call__(self, args: list[str], **kw: Any) -> Proc:
        self.calls.append(list(args))
        if "yt_dlp" in args:
            if self.ytdlp.returncode == 0:
                out = Path(args[args.index("-o") + 1].replace("%(ext)s", self.container))
                out.write_bytes(b"new video bytes")
            return self.ytdlp
        if args[0] == "ffmpeg":
            Path(args[-1]).write_bytes(b"remuxed")
            return Proc()
        if not self.good:  # ffprobe on a file that does not read
            return Proc(1, b"", b"x.mp4: moov atom not found")
        doc = {"format": {"duration": "600.5"}, "streams": [{"width": 1920, "height": 1080}]}
        return Proc(0, json.dumps(doc).encode())


def swap(tmp_path: Path, tools: FakeTools, **kw: Any) -> tuple[repair.RepairOutcome, Path]:
    target = tmp_path / "share" / "video.mp4"
    target.parent.mkdir()
    target.write_bytes(b"broken")
    work = tmp_path / "work"
    work.mkdir()
    got = repair.fetch_and_swap(
        str(target), "yt0000000AB", work, run=tools, ffmpeg="ffmpeg", ffprobe="ffprobe", **kw
    )
    return got, target


def test_h264_comes_first_for_an_mp4_and_the_height_is_the_setting() -> None:
    f = repair.format_for("mp4")
    assert f.startswith("bv*[ext=mp4][vcodec^=avc1][height<=1080]+ba[ext=m4a]/")
    assert f == repair.FORMAT_MP4
    assert "avc1" not in repair.format_for("mp4", h264_first=False)
    assert repair.format_for("mkv", max_height=720) == "bv*[height<=720]+ba/b[height<=720]/b"
    assert repair.format_for("mkv") == repair.FORMAT


def test_download_args_write_locally_under_a_plain_name(tmp_path: Path) -> None:
    args = repair.download_args(
        "a-B_9", Path("Y:/x/مفاتح ⧸ ｜ [1].mp4"), "firefox", tmp_path, max_height=720
    )
    assert args[-1] == "https://www.youtube.com/watch?v=a-B_9"
    assert Path(args[args.index("-o") + 1]) == tmp_path / "v-a-B_9.%(ext)s"
    assert "bv*[ext=mp4][vcodec^=avc1][height<=720]" in args[args.index("-f") + 1]
    assert args[args.index("--cookies-from-browser") + 1] == "firefox"


def test_check_reads_a_file_now_and_says_why_it_cannot() -> None:
    ok = repair.check("a.mp4", run=FakeTools(), ffprobe="ffprobe")
    assert ok.readable and ok.duration == 600.5 and (ok.width, ok.height) == (1920, 1080)
    bad = repair.check("a.mp4", run=FakeTools(good=False), ffprobe="ffprobe")
    assert not bad.readable and bad.why.startswith("broken file")


def test_a_broken_file_is_replaced_and_kept_as_broken(tmp_path: Path) -> None:
    got, target = swap(tmp_path, FakeTools())
    assert got.ok and "repaired: 10.0 min, 1920x1080" in got.message
    assert target.read_bytes() == b"new video bytes"
    assert got.backup is not None and got.backup.read_bytes() == b"broken"
    assert got.backup.name == "video.mp4.broken"
    assert got.probed is not None and got.size == len(b"new video bytes")
    assert not list(target.parent.glob(".*repair*"))  # nothing half-done stays beside it


def test_a_second_backup_never_overwrites_the_first(tmp_path: Path) -> None:
    (tmp_path / "a.mp4.broken").write_bytes(b"old")
    assert repair.backup_name(tmp_path / "a.mp4").name == "a.mp4.broken2"


def test_the_broken_copy_can_be_dropped(tmp_path: Path) -> None:
    got, target = swap(tmp_path, FakeTools(), keep_broken=False)
    assert got.ok and got.backup is None
    assert [p.name for p in target.parent.iterdir()] == ["video.mp4"]


def test_a_failed_download_leaves_the_file_as_it_was(tmp_path: Path) -> None:
    tools = FakeTools(ytdlp=Proc(1, b"", b"ERROR: Video unavailable\n"))
    got, target = swap(tmp_path, tools)
    assert not got.ok and "not downloaded: ERROR: Video unavailable" in got.message
    assert target.read_bytes() == b"broken"


def test_a_new_copy_that_does_not_read_either_is_not_swapped_in(tmp_path: Path) -> None:
    got, target = swap(tmp_path, FakeTools(good=False))
    assert not got.ok and "does not read either" in got.message
    assert target.read_bytes() == b"broken"


def test_another_container_is_remuxed_into_the_files_own(tmp_path: Path) -> None:
    tools = FakeTools(container="webm")
    got, target = swap(tmp_path, tools)
    assert got.ok and target.read_bytes() == b"remuxed"
    assert any(c[0] == "ffmpeg" and "copy" in c for c in tools.calls)


def test_only_finished_downloads_count(tmp_path: Path) -> None:
    (tmp_path / "v-a.f137.mp4.part").write_bytes(b"x")
    (tmp_path / "v-a.webm").write_bytes(b"x")
    assert [p.name for p in repair.made(tmp_path)] == ["v-a.webm"]
