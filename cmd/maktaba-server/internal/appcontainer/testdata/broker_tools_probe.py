"""Inside a runner group's AppContainer (story 54.23, CI's windows-2022 job only): the
group's tools (ffmpeg, ffprobe) on a file of its library on a share, through the handle its
launcher's broker opens, started as the media stages start them. Writes its findings as
JSON to the path given; never raises past a finding."""

from __future__ import annotations

import asyncio
import importlib.util
import json
import os
import subprocess
import sys
import time

out_path, winfiles_path, lib, root, other_share, ffmpeg, ffprobe, run, outside = sys.argv[1:10]
video = lib + "\\show\\ep2.mp4"
second = lib + "\\show\\ep3.mp4"
found: dict[str, object] = {}


def attempt(name: str, fn) -> None:  # type: ignore[no-untyped-def]
    try:
        found[name] = ["ok", fn()]
    except PermissionError as exc:
        found[name] = ["refused", str(exc)]
    except Exception as exc:  # noqa: BLE001
        found[name] = ["error", f"{type(exc).__name__}: {exc}"]


def tool(argv: list[str], **kwargs) -> dict[str, object]:  # type: ignore[no-untyped-def]
    start = time.perf_counter()
    p = subprocess.run(argv, capture_output=True, check=False, **kwargs)
    return {
        "code": p.returncode,
        "seconds": time.perf_counter() - start,
        "stdout_bytes": len(p.stdout),
        "stdout_head": p.stdout[:400].decode("utf-8", "replace"),
        "stderr": p.stderr[-400:].decode("utf-8", "replace"),
    }


spec = importlib.util.spec_from_file_location("winfiles", winfiles_path)
assert spec and spec.loader
winfiles = importlib.util.module_from_spec(spec)
spec.loader.exec_module(winfiles)
found["installed"] = winfiles.install_from_env()

thumb = os.path.join(run, "thumb.jpg")


def thumbnail() -> dict[str, object]:
    # chapter_thumbs' shape: a seek before the input, a file in the run folder
    r = tool(
        [
            ffmpeg,
            "-hide_banner",
            "-nostdin",
            "-threads",
            "1",
            "-ss",
            "10",
            "-i",
            video,
            "-frames:v",
            "1",
            "-vf",
            "scale=320:-2",
            "-q:v",
            "2",
            "-y",
            thumb,
        ]
    )
    r["jpeg_bytes"] = os.path.getsize(thumb) if os.path.exists(thumb) else 0
    return r


attempt("thumbnail", thumbnail)


async def small() -> dict[str, object]:
    # thumb_small's shape, as run_media starts it: asyncio, stdin DEVNULL, the JPEG on stdout
    start = time.perf_counter()
    proc = await asyncio.create_subprocess_exec(
        ffmpeg,
        "-v",
        "error",
        "-nostdin",
        "-i",
        video,
        "-vf",
        "scale=320:-2",
        "-frames:v",
        "1",
        "-q:v",
        "5",
        "-f",
        "image2pipe",
        "-vcodec",
        "mjpeg",
        "pipe:1",
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    out, err = await proc.communicate()
    return {
        "code": proc.returncode,
        "seconds": time.perf_counter() - start,
        "jpeg": out[:2] == b"\xff\xd8",
        "stderr": err[-400:].decode("utf-8", "replace"),
    }


attempt("thumbnail_pipe", lambda: asyncio.run(small()))


def probe() -> dict[str, object]:
    start = time.perf_counter()
    p = subprocess.run(
        [ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", video],
        capture_output=True,
        check=False,
    )
    return {
        "code": p.returncode,
        "seconds": time.perf_counter() - start,
        "duration": float(p.stdout.strip() or 0),
        "stderr": p.stderr[-400:].decode("utf-8", "replace"),
    }


attempt("probe", probe)


def audio() -> dict[str, object]:
    # decode_audio's shape: the transcription input, PCM on stdout
    r = tool(
        [
            ffmpeg,
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-i",
            video,
            "-map",
            "0:a:0",
            "-vn",
            "-ac",
            "1",
            "-ar",
            "16000",
            "-f",
            "s16le",
            "-",
        ]
    )
    r.pop("stdout_head", None)
    return r


attempt("audio", audio)


def pipe_control() -> dict[str, object]:
    # the control for fd: — the same handle as pipe:0, which ffmpeg never seeks: an mp4
    # whose index is at its end
    with open(video, "rb") as f:
        p = subprocess.run(
            [
                ffprobe,
                "-v",
                "error",
                "-show_entries",
                "format=duration",
                "-of",
                "csv=p=0",
                "pipe:0",
            ],
            stdin=f,
            capture_output=True,
            check=False,
        )
    return {
        "code": p.returncode,
        "stdout": p.stdout.decode().strip(),
        "stderr": p.stderr[-300:].decode("utf-8", "replace"),
    }


attempt("pipe_control", pipe_control)

# refused before anything starts
attempt("second_file", lambda: tool([ffmpeg, "-i", video, "-i", second, "-f", "null", "-"]))
attempt("library_folder", lambda: tool([ffprobe, "-v", "error", lib]))
attempt("share_root", lambda: tool([ffprobe, "-v", "error", root]))
attempt("other_share", lambda: tool([ffprobe, "-v", "error", other_share + "\\secret.mp4"]))
attempt("output_on_share", lambda: tool([ffmpeg, "-i", run + "\\none.mp4", lib + "\\show\\x.jpg"]))
attempt("not_a_handle_tool", lambda: tool([sys.executable, "-c", "pass", video]))

# a tool that reaches for the share by itself (a path the runner never saw: a concat list in
# its run folder) gets nothing: an AppContainer reaches no share
listing = os.path.join(run, "list.txt")
with open(listing, "w", encoding="utf-8") as f:
    f.write(f"file '{second}'\n")
attempt(
    "tool_reaches_share",
    lambda: tool(
        [
            ffmpeg,
            "-v",
            "error",
            "-nostdin",
            "-f",
            "concat",
            "-safe",
            "0",
            "-i",
            listing,
            "-frames:v",
            "1",
            "-f",
            "null",
            "-",
        ]
    ),
)
# and writes nowhere but its granted folders
stray = os.path.join(outside, "stray.jpg")
attempt(
    "write_outside",
    lambda: {
        **tool([ffmpeg, "-v", "error", "-nostdin", "-i", video, "-frames:v", "1", "-y", stray]),
        "made": os.path.exists(stray),
    },
)

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(found, f, indent=1)
