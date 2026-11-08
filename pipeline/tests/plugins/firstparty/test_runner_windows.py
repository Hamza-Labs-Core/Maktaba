"""A first-party runner on Windows (Epic 54, story 54.23): in its group's AppContainer,
reached through the supervisor's pipes. Windows itself runs in CI (the plugins-sandbox job's
runner step); these check the logic on every OS: the policy the launcher grants from, the
refusal outside an AppContainer, the tools guard, the subtitle names guard, the back pipe's
waiting connections, the worker's way in, and the run folder."""

from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import textwrap
import threading
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.native import PluginRefused

from maktaba_plugin_runner import sandbox, winfiles, winpipe, winproc


def test_the_policy_is_printed_for_the_launcher(tmp_path: Path) -> None:
    """``--policy``'s document: the group's folders, its tools and their installations, the
    interpreter's own paths, and the Job Object's process limit (one runner; room for tools
    only when the group may run any)."""
    tool = tmp_path / "ffmpeg" / "bin" / "ffmpeg"
    tool.parent.mkdir(parents=True)
    tool.write_text("")
    p = sandbox.Policy(
        read=["/media/a"],
        write=["/data/thumbnails"],
        write_named=["/media/a"],
        exec=[str(tool)],
        net=False,
        run_dir="/data/run/runners",
    )
    doc = sandbox.policy_doc(p)
    assert doc["read"] == ["/media/a"] and doc["write"] == ["/data/thumbnails"]
    assert doc["write_named"] == ["/media/a"] and doc["exec"] == [str(tool)]
    assert doc["run_dir"] == "/data/run/runners" and doc["net"] is False
    assert str(tmp_path / "ffmpeg") in doc["tool_dirs"]
    assert sys.prefix in doc["python"] or os.path.realpath(sys.prefix) in doc["python"]
    assert doc["processes"] >= 2  # the runner and its tools
    assert sandbox.policy_doc(sandbox.Policy())["processes"] in (1, 2)  # no tools: no child
    json.dumps(doc)  # what the launcher reads


def test_a_tool_at_a_drive_root_is_not_granted_the_whole_drive() -> None:
    """A tool's installation is never a filesystem root (the launcher grants it read)."""
    assert sandbox.tool_prefixes("/ffmpeg") == [os.path.realpath("/ffmpeg")]
    if sys.platform == "win32":
        assert sandbox.tool_prefixes("C:\\bin\\ffmpeg.exe") == ["C:\\bin\\ffmpeg.exe"]


def test_outside_an_appcontainer_a_windows_runner_does_not_run(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """No runner runs unsandboxed: a Windows runner started outside its AppContainer, or
    holding a capability its group has not (the network for a group without net), refuses
    to serve (sandbox_unavailable)."""
    monkeypatch.setattr(sandbox, "_token_appcontainer", lambda: (False, []))
    with pytest.raises(sandbox.SandboxUnavailable) as exc:
        sandbox._apply_windows(sandbox.Policy())
    assert exc.value.step == "appcontainer"
    monkeypatch.setattr(sandbox, "_token_appcontainer", lambda: (True, [sandbox.INTERNET_CLIENT]))
    with pytest.raises(sandbox.SandboxUnavailable) as exc:
        sandbox._apply_windows(sandbox.Policy(net=False))
    assert exc.value.step == "appcontainer_capabilities"
    guarded: list[list[str]] = []
    monkeypatch.setattr(sandbox, "guard_exec", lambda tools: guarded.append(tools))
    assert sandbox._apply_windows(sandbox.Policy(net=True, exec=["/t"])).level == "full"
    assert guarded == [["/t"]]
    monkeypatch.setattr(sandbox, "_token_appcontainer", lambda: (True, []))
    assert sandbox._apply_windows(sandbox.Policy()).level == "full"


def test_apply_on_windows_is_the_appcontainer_check(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "platform", "win32")
    seen: list[sandbox.Policy] = []

    def checked(p: sandbox.Policy) -> sandbox.Applied:
        seen.append(p)
        return sandbox.Applied("full")

    monkeypatch.setattr(sandbox, "_apply_windows", checked)
    assert sandbox.apply(sandbox.Policy()).level == "full" and len(seen) == 1


GUARD = textwrap.dedent(
    """
    import json, os, subprocess, sys
    from maktaba_plugin_runner import sandbox
    allowed, other = sys.argv[1], sys.argv[2]
    sandbox.guard_exec([allowed])
    out = {}
    def probe(name, argv, **kw):
        try:
            subprocess.run(argv, check=True, capture_output=True, **kw)
            out[name] = "allowed"
        except Exception as exc:
            out[name] = "denied:" + getattr(exc, "code", type(exc).__name__)
    probe("granted", [allowed, "-c", "pass"])
    probe("other", [other, "-c", "pass"])
    probe("shell", "exit 0", shell=True)
    try:
        os.system("exit 0")
        out["system"] = "allowed"
    except Exception as exc:
        out["system"] = "denied:" + getattr(exc, "code", type(exc).__name__)
    print(json.dumps(out))
    """
)


def test_a_runner_runs_only_its_groups_tools(tmp_path: Path) -> None:
    """On Windows a system program is readable by every AppContainer, so the runner's own
    guard keeps a group with tools to its tools (the Job Object allows no child at all to a
    group without any): another program, a shell and os.system are refused."""
    other = tmp_path / "other-python"
    other.symlink_to(sys.executable)
    granted = os.path.realpath(sys.executable)
    out = subprocess.run(
        [sys.executable, "-c", GUARD, granted, str(other)],
        capture_output=True,
        text=True,
        timeout=60,
        env={**os.environ},
    )
    assert out.returncode == 0, out.stderr
    got = json.loads(out.stdout.strip().splitlines()[-1])
    assert got["granted"] == "allowed", got
    # a link to the granted program is the granted program (real paths are compared)
    assert got["other"] == "allowed", got
    assert got["shell"] == "denied:plugin_exec", got
    assert got["system"] == "denied:plugin_exec", got


def test_a_program_not_granted_is_refused() -> None:
    assert sandbox.exec_allowed(sys.executable, [sys.executable])
    assert not sandbox.exec_allowed("/bin/sh", [sys.executable])
    assert not sandbox.exec_allowed(None, [sys.executable])


def test_subtitle_names_compare_paths_as_the_os_does(monkeypatch: pytest.MonkeyPatch) -> None:
    """guard_names matches a library root the way the OS names it (Windows: any case), and
    the subtitle name as written."""
    check = sandbox._names_check(["/Media/A"], normcase=str.lower)
    with pytest.raises(PluginRefused):
        check("/media/a/show/episode.mkv")
    check("/media/a/show/episode.ar.srt")
    check("/elsewhere/file.mkv")


class _Pair:
    """An in-memory connection: what the runner reads, and what it wrote."""

    def __init__(self, first: bytes) -> None:
        self.reader = asyncio.StreamReader()
        self.reader.feed_data(first)
        self.reader.feed_eof()  # the other side sends nothing more (b"": a closed one)
        self.written = bytearray()
        self.closed = False

        pair = self

        class W:
            def write(self, b: bytes) -> None:
                pair.written += b

            async def drain(self) -> None:
                return None

            def close(self) -> None:
                pair.closed = True

            def is_closing(self) -> bool:
                return pair.closed

            async def wait_closed(self) -> None:
                return None

        self.writer = W()


def test_the_runner_waits_on_its_back_pipe_and_serves_what_it_is_handed() -> None:
    """The runner keeps connections waiting on its back pipe; one handed a caller (Ready)
    is served, and another is opened to wait in its place; one the supervisor closes is
    opened again (the supervisor restarting the broker)."""

    async def main() -> tuple[list[str], int]:
        dials: list[str] = []
        conns = [_Pair(b""), _Pair(winpipe.READY + b"caller"), _Pair(winpipe.READY + b"next")]
        handled: list[str] = []
        done = asyncio.Event()

        async def dial(name: str) -> Any:
            dials.append(name)
            if conns:
                c = conns.pop(0)
                return c.reader, c.writer
            await asyncio.sleep(3600)

        async def handle(r: asyncio.StreamReader, w: Any) -> None:
            handled.append((await r.read(6)).decode())
            if len(handled) == 2:
                done.set()

        task = asyncio.ensure_future(
            winpipe.serve_back(handle, "\\\\.\\pipe\\x-back", waiting=1, dial=dial, retry=0)
        )
        await asyncio.wait_for(done.wait(), 5)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        return handled, len(dials)

    handled, dials = asyncio.run(main())
    assert handled == ["caller", "next"]
    assert dials == 4  # the closed one, the two handed callers, and one waiting now


def test_a_dial_that_fails_is_tried_again() -> None:
    async def main() -> int:
        tries = 0
        served = asyncio.Event()

        async def dial(name: str) -> Any:
            nonlocal tries
            tries += 1
            if tries < 3:
                raise FileNotFoundError(name)
            if tries == 3:
                c = _Pair(winpipe.READY)
                return c.reader, c.writer
            await asyncio.sleep(3600)

        async def handle(r: Any, w: Any) -> None:
            served.set()

        task = asyncio.ensure_future(winpipe.serve_back(handle, "p", waiting=1, dial=dial, retry=0))
        await asyncio.wait_for(served.wait(), 5)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        return tries

    assert asyncio.run(main()) >= 3


def test_the_api_transport_opens_the_api_pipe(monkeypatch: pytest.MonkeyPatch) -> None:
    """A Windows runner's database calls go to the plugin API through its API pipe."""
    opened: list[str] = []

    async def connect(name: str, **_: Any) -> Any:
        opened.append(name)
        c = _Pair(b'HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\n{"ok":true}')
        return c.reader, c.writer

    monkeypatch.setattr(winpipe, "connect", connect)
    t = winpipe.PipeTransport("\\\\.\\pipe\\x-api", "tok")
    assert asyncio.run(t.call("ping", {})) == {"ok": True}
    assert opened == ["\\\\.\\pipe\\x-api"]


def test_a_runner_on_windows_serves_through_its_pipes(monkeypatch: pytest.MonkeyPatch) -> None:
    """Runner.serve and its transport take the pipes on Windows."""
    from maktaba_plugin_runner.server import Runner

    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.setenv(winpipe.BACK_ENV, "\\\\.\\pipe\\r-back")
    monkeypatch.setenv(winpipe.API_ENV, "\\\\.\\pipe\\r-api")
    served: list[str] = []

    async def serve_back(handle: Any, name: str, **_: Any) -> None:
        served.append(name)

    monkeypatch.setattr(winpipe, "serve_back", serve_back)
    r = Runner(group="light", api_socket="", stages={}, plugin_of={}, bundle="b")
    asyncio.run(r.serve(Path("unused.sock")))
    assert served == ["\\\\.\\pipe\\r-back"]
    t = r._transport("tok")
    assert isinstance(t, winpipe.PipeTransport) and t.pipe == "\\\\.\\pipe\\r-api"


def test_a_windows_runner_without_its_pipes_does_not_start(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    from maktaba_plugin_runner import __main__ as runner_main

    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.delenv(winpipe.BACK_ENV, raising=False)
    monkeypatch.delenv(winpipe.API_ENV, raising=False)
    assert runner_main.main(["--group", "light"]) == runner_main.EXIT_ENV


def test_the_run_folder_on_windows_is_never_under_tmp(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    from maktaba_plugin_runner import runner_dir

    monkeypatch.delenv("MAKTABA_RUNNER_DIR", raising=False)
    long = tmp_path / ("d" * 120)
    monkeypatch.setenv("MAKTABA_DATA_DIR", str(long))
    monkeypatch.setattr(sys, "platform", "win32")
    assert runner_dir() == long / "run" / "runners"


SHARE = "\\\\nas\\media\\shows"


def _shim(tmp_path: Path, answers: dict[str, dict[str, Any]]) -> tuple[Any, list[dict[str, Any]]]:
    """A broker shim over a fake broker: ``answers`` by operation; an ``open`` hands back a
    real descriptor of a temp file, as the launcher hands back a real handle."""
    asked: list[dict[str, Any]] = []
    target = tmp_path / "brokered.bin"
    target.write_bytes(b"video bytes")

    def request(req: dict[str, Any]) -> dict[str, Any]:
        asked.append(req)
        out = dict(answers.get(req["op"], {}))
        if req["op"] == "open" and "error" not in out:
            out["handle"] = os.open(target, os.O_RDONLY | getattr(os, "O_BINARY", 0))
        return out

    shim = winfiles._Shim(request, [SHARE], lambda handle, _flags: handle)
    return shim, asked


def test_a_share_path_goes_through_the_broker_and_nothing_else_does(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Story 54.23: a stage's calls on a path in a brokered share folder (any case, either
    slash) reach the launcher's broker, each with the audit event the plain call raises (so
    the runner's subtitle-name and job-scope guards see the path); a local path, and a share
    path outside the brokered folders, are the plain calls."""
    events: list[tuple[str, tuple[object, ...]]] = []
    monkeypatch.setattr(sys, "audit", lambda e, *a: events.append((e, a)))
    shim, asked = _shim(
        tmp_path,
        {
            "stat": {"size": 11, "mtime_ns": 5_000_000_000},
            "list": {"names": ["ep1.mkv", "ep1.en.srt"]},
        },
    )
    video = SHARE + "\\a\\ep1.mkv"
    with shim.open_(video, "rb") as f:
        assert f.read() == b"video bytes"
    assert asked[-1] == {"op": "open", "path": video}
    assert events[-1][0] == "open" and events[-1][1][0] == video
    st = shim.stat_("//NAS/Media/Shows/a/ep1.mkv")
    assert st.st_size == 11 and st.st_mtime == 5
    assert shim.exists_(video) and shim.isfile_(video) and not shim.isdir_(video)
    assert shim.listdir_(SHARE + "\\a") == ["ep1.mkv", "ep1.en.srt"]
    sub = SHARE + "\\a\\ep1.en.srt"
    with shim.open_(sub + ".maktaba-tmp", "wb") as f:
        f.write(b"1\n")
        assert asked[-1]["op"] == "list"  # nothing is sent before the file closes
    # the bytes go to the broker, which writes the file: the runner holds no write handle
    assert asked[-1] == {"op": "write", "path": sub + ".maktaba-tmp", "data": "MQo="}
    with shim.open_(sub + ".maktaba-tmp", "w", encoding="utf-8") as t:
        t.write("é")
    assert asked[-1]["data"] == "w6k="
    shim.replace_(sub + ".maktaba-tmp", sub)
    assert asked[-1] == {"op": "replace", "path": sub + ".maktaba-tmp", "to": sub}
    shim.unlink_(sub)
    assert asked[-1] == {"op": "remove", "path": sub}
    assert [e for e, _ in events] == [
        "open",
        "os.stat",
        "os.stat",
        "os.stat",
        "os.stat",
        "os.listdir",
        "open",
        "open",
        "os.rename",
        "os.remove",
    ]

    n = len(asked)
    local = tmp_path / "local.txt"
    with shim.open_(local, "w") as f:
        f.write("x")
    assert shim.exists_(str(local)) and shim.stat_(local).st_size == 1
    assert shim.under("\\\\nas\\media\\showsx\\a.mkv") is None
    assert shim.under("\\\\nas\\other\\a.mkv") is None
    assert shim.under(3) is None and shim.under(None) is None
    assert len(asked) == n  # none of these reached the broker


def test_what_the_broker_refuses_runs_in_the_worker(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A refusal is a PermissionError, which sends the job to the worker (sandbox_refused);
    a missing file is FileNotFoundError. Appending, exclusive creation or read-write on a
    share, a move on or off a share, and a tool handed a share path are refused before they
    reach the broker (a tool cannot use the runner's handles)."""
    from maktaba_pipeline.plugins.native import sandbox_refused

    monkeypatch.setattr(sys, "audit", lambda *_: None)
    shim, asked = _shim(
        tmp_path,
        {
            "write": {"error": "only subtitle files are written here", "errno": 13},
            "stat": {"error": "gone", "errno": 2},
        },
    )
    with pytest.raises(PermissionError) as refused, shim.open_(SHARE + "\\a\\notes.txt", "wb") as f:
        f.write(b"x")
    assert sandbox_refused(refused.value)
    with pytest.raises(FileNotFoundError):
        shim.stat_(SHARE + "\\a\\gone.mkv")
    assert not shim.exists_(SHARE + "\\a\\gone.mkv")
    assert not shim.isfile_(SHARE + "\\a\\gone.mkv")
    assert not shim.isdir_(SHARE + "\\a\\gone.mkv")
    assert not shim.lexists_(SHARE + "\\a\\gone.mkv")
    n = len(asked)
    for mode in ("ab", "xb", "r+b", "w+b"):
        with pytest.raises(PermissionError):
            shim.open_(SHARE + "\\a\\ep1.en.srt", mode)
    with pytest.raises(PermissionError):
        shim.replace_(str(tmp_path / "x.srt"), SHARE + "\\a\\x.en.srt")
    # a share path that reaches a process start unrouted (not one tool input) is refused
    with pytest.raises(PermissionError) as tool:
        shim.exec_hook(
            "subprocess.Popen",
            ("ffmpeg", ["ffmpeg", "-i", SHARE + "\\a\\ep1.mkv", "out.jpg"], None, None),
        )
    assert sandbox_refused(tool.value)
    shim.exec_hook(
        "subprocess.Popen", ("ffmpeg", ["ffmpeg", "-i", str(tmp_path / "a.mkv")], None, None)
    )
    assert len(asked) == n


def test_a_refused_look_is_never_an_absent_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A stat the broker refuses raises from ``exists``/``isfile``/``isdir`` instead of
    answering False: thumb_small looks for a sidecar with ``isfile``, and False there made
    the job end done with no thumbnail instead of running in the worker."""
    from maktaba_pipeline.discovery.ytdlp_sidecars import find_sidecar_thumbnail
    from maktaba_pipeline.plugins.native import sandbox_refused

    monkeypatch.setattr(sys, "audit", lambda *_: None)
    shim, _ = _shim(tmp_path, {"stat": {"error": "a link or another reparse point", "errno": 13}})
    video = SHARE + "\\a\\ep1.mp4"
    for look in (shim.exists_, shim.lexists_, shim.isfile_, shim.isdir_):
        with pytest.raises(PermissionError) as refused:
            look(video)
        assert sandbox_refused(refused.value)
    monkeypatch.setattr(os.path, "isfile", shim.isfile_)
    with pytest.raises(PermissionError):
        find_sidecar_thumbnail(video)


def test_a_share_folder_is_never_scanned_as_empty(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """``os.scandir`` (and so ``os.walk`` and ``Path.glob``) is not brokered: on a brokered
    folder it raises the refusal, so the job runs in the worker instead of finding nothing;
    a local folder is scanned as before, and the broker is never asked."""
    from maktaba_pipeline.plugins.native import sandbox_refused

    monkeypatch.setattr(sys, "audit", lambda *_: None)
    shim, asked = _shim(tmp_path, {})
    with pytest.raises(PermissionError) as refused:
        shim.scandir_(SHARE + "\\a")
    assert sandbox_refused(refused.value)
    (tmp_path / "local.mkv").write_bytes(b"x")
    with shim.scandir_(str(tmp_path)) as it:
        assert "local.mkv" in [e.name for e in it]
    assert shim.lexists_(str(tmp_path / "local.mkv"))
    assert asked == []


def test_a_share_this_runner_is_not_brokered_is_refused_never_missing(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A runner reaches no share by itself: any share path outside its brokered folders
    (all of them, for a runner with no broker) raises the refusal from every look, so the
    plain calls never answer False or empty there; the broker is never asked, and local
    paths, long local paths and devices go to the plain calls."""
    from maktaba_pipeline.plugins.native import sandbox_refused

    monkeypatch.setattr(sys, "audit", lambda *_: None)
    for roots in ([SHARE], []):
        shim, asked = _shim(tmp_path, {})
        shim.roots = [winfiles._key(r) for r in roots]
        for other in (
            "\\\\other\\media\\a.mkv",
            "//other/media/a.mkv",
            "\\\\?\\UNC\\other\\m\\a.mkv",
        ):
            for look in (
                shim.exists_,
                shim.lexists_,
                shim.isfile_,
                shim.isdir_,
                shim.stat_,
                shim.listdir_,
                shim.scandir_,
                shim.open_,
            ):
                with pytest.raises(PermissionError) as refused:
                    look(other)
                assert sandbox_refused(refused.value)
        if not roots:
            with pytest.raises(PermissionError):
                shim.exists_(SHARE + "\\a\\ep1.mp4")
        assert asked == []
        local = tmp_path / "local.mkv"
        local.write_bytes(b"x")
        assert shim.exists_(str(local)) and shim.isfile_(str(local))
        assert not shim.exists_(str(tmp_path / "gone.mkv"))
    assert winfiles.is_share("\\\\nas\\m") and winfiles.is_share("\\\\?\\unc\\nas\\m")
    assert not winfiles.is_share("\\\\?\\C:\\m") and not winfiles.is_share("\\\\.\\pipe\\x")
    assert not winfiles.is_share("C:\\m")


def test_the_broker_pipe_carries_one_line_each_way() -> None:
    """The runner's side of the broker's pipe: a request line out, the answer line back
    (read in pieces as a pipe hands them), and a closed pipe is a refusal."""

    class Pipe:
        def __init__(self, replies: list[bytes]) -> None:
            self.sent = b""
            self.replies = replies

        def write(self, b: bytes) -> int:
            self.sent += b
            return len(b)

        def read(self, _n: int) -> bytes:
            return self.replies.pop(0) if self.replies else b""

    pipe = Pipe([b'{"handle": 4', b'2}\n{"names": []}\n'])
    b = winfiles.Broker("\\\\.\\pipe\\x", opener=lambda _p: pipe)
    assert b({"op": "open", "path": "p"}) == {"handle": 42}
    assert b({"op": "list", "path": "p"}) == {"names": []}
    assert pipe.sent.count(b"\n") == 2 and json.loads(pipe.sent.split(b"\n")[0]) == {
        "op": "open",
        "path": "p",
    }
    with pytest.raises(PermissionError):
        b({"op": "stat", "path": "p"})
    assert winfiles.install_from_env({}) is False


VIDEO = SHARE + "\\a\\ep1.mkv"


def test_a_tool_reads_its_one_share_input_through_a_brokered_handle(tmp_path: Path) -> None:
    """Story 54.23: a tool (ffmpeg, ffprobe) given one file of a brokered share folder as its
    input reads it through a read handle the broker opened for that file, handed to it as
    its standard input: the argument becomes ``fd:`` (ffmpeg's protocol for a descriptor,
    seekable when it is a file) and the share path reaches no process. Its other arguments
    stay as they were; a tool on no share path is left alone."""
    shim, asked = _shim(tmp_path, {})
    for argv, at in (
        (["ffmpeg", "-v", "error", "-nostdin", "-i", VIDEO, "-frames:v", "1", "pipe:1"], 5),
        (["C:\\tools\\FFmpeg.EXE", "-ss", "12", "-i", VIDEO.upper(), "-y", "C:\\run\\t.jpg"], 4),
        (["ffprobe", "-v", "error", "-show_format", "-of", "json", VIDEO], 6),
        (["ffprobe", "-i", "//NAS/media/shows/a/ep1.mkv", "-show_streams"], 2),
    ):
        routed = shim.tool_args(argv)
        assert routed is not None
        new, path = routed
        assert new == [*argv[:at], "fd:", *argv[at + 1 :]]
        assert winfiles._key(path) == winfiles._key(VIDEO)
    assert shim.tool_args(["ffmpeg", "-i", str(tmp_path / "a.mkv"), "out.jpg"]) is None
    assert shim.tool_args(["ffmpeg", "-i", "https://example.org/a.mkv", "out.jpg"]) is None
    assert asked == []  # deciding opens nothing


@pytest.mark.parametrize(
    ("argv", "why"),
    [
        (["ffmpeg", "-i", VIDEO, "-i", SHARE + "\\a\\ep2.mkv", "o.mkv"], "a second share file"),
        (["ffmpeg", "-i", "C:\\x.mkv", VIDEO], "a share path as the output"),
        (["ffmpeg", "-i", "C:\\x.mkv", "-vf", "subtitles=" + VIDEO, "o.mkv"], "inside an option"),
        (["ffmpeg", "-i", "file:" + VIDEO, "o.jpg"], "behind a protocol"),
        (["ffmpeg", "-i", VIDEO, "-i", "pipe:0", "o.mkv"], "standard input taken"),
        (["ffmpeg", "-i", VIDEO, "-i", "fd:", "o.mkv"], "standard input taken"),
        (["ffmpeg", "-i", "\\\\nas\\other\\a.mkv", "o.jpg"], "another share"),
        (["ffmpeg", "-i", "\\\\nas\\media\\showsx\\a.mkv", "o.jpg"], "another share"),
        (["yt-dlp", "-o", "x", VIDEO], "not a tool that reads a descriptor"),
        (["python", VIDEO], "not a tool that reads a descriptor"),
        (["ffprobe", VIDEO, "-show_format"], "a positional input not last"),
    ],
)
def test_a_tool_on_a_share_path_it_cannot_read_through_a_handle_is_refused(
    tmp_path: Path, argv: list[str], why: str
) -> None:
    """Anything but one share file as one tool's input is refused before a broker call, as
    before (sandbox_refused: the worker runs that job): a second share file (one standard
    input), a share path as an output or inside another argument, another share (one the
    group is not brokered), a tool that is not ffmpeg or ffprobe."""
    from maktaba_pipeline.plugins.native import sandbox_refused

    shim, asked = _shim(tmp_path, {})
    with pytest.raises(PermissionError) as refused:
        shim.tool_args(argv)
    assert sandbox_refused(refused.value), why
    assert asked == []


def _fake_tool(tmp_path: Path, name: str) -> Path:
    """A tool named like ffmpeg: it prints its arguments, then what its standard input holds
    from offset 6 (it seeks, as ffmpeg seeks a file for an mp4's index at its end)."""
    tool = tmp_path / "bin" / name
    tool.parent.mkdir(exist_ok=True)
    tool.write_text(
        f"#!{sys.executable}\n"
        "import json, os, sys\n"
        "os.lseek(0, 6, os.SEEK_SET)\n"
        "print(json.dumps([sys.argv[1:], os.read(0, 64).decode()]))\n"
    )
    tool.chmod(0o755)
    return tool


@pytest.mark.skipif(sys.platform == "win32", reason="the fake tool is a script with a #! line")
def test_a_tool_started_on_a_share_input_gets_the_handle_as_its_stdin(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The routing where stages start tools: ``subprocess.run`` and asyncio's
    ``create_subprocess_exec`` (with ``stdin=DEVNULL``, as the media helpers pass) start the
    tool with ``fd:`` and the brokered handle as its standard input, which it can seek; the
    runner's own copy of the handle is closed once the tool has its. A caller that needs the
    tool's standard input itself (``stdin=PIPE``, ``input=``) is refused."""
    from maktaba_pipeline.plugins.native import sandbox_refused

    shim, asked = _shim(tmp_path, {})
    monkeypatch.setattr(subprocess.Popen, "__init__", shim.popen_init(subprocess.Popen.__init__))
    monkeypatch.setattr(sys, "audit", lambda *_: None)
    tool = str(_fake_tool(tmp_path, "ffmpeg"))
    before = len(os.listdir("/dev/fd")) if os.path.isdir("/dev/fd") else None

    out = subprocess.run([tool, "-i", VIDEO, "pipe:1"], capture_output=True, text=True, check=True)
    assert json.loads(out.stdout) == [["-i", "fd:", "pipe:1"], "bytes"]
    assert asked == [{"op": "open", "path": VIDEO}]

    async def via_asyncio() -> bytes:
        proc = await asyncio.create_subprocess_exec(
            tool,
            "-ss",
            "1",
            "-i",
            VIDEO,
            "-f",
            "null",
            "-",
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
        )
        stdout, _ = await proc.communicate()
        return stdout

    assert json.loads(asyncio.run(via_asyncio())) == [
        ["-ss", "1", "-i", "fd:", "-f", "null", "-"],
        "bytes",
    ]
    if before is not None:
        assert len(os.listdir("/dev/fd")) == before  # no handle left open in the runner

    for kwargs in ({"stdin": subprocess.PIPE}, {"input": b"x"}):
        with pytest.raises(PermissionError) as refused:
            subprocess.run([tool, "-i", VIDEO], capture_output=True, check=False, **kwargs)
        assert sandbox_refused(refused.value)
    plain = subprocess.run(
        [tool, "-i", "/dev/null"],
        capture_output=True,
        text=True,
        stdin=subprocess.DEVNULL,
        check=False,
    )
    assert json.loads(plain.stdout)[0] == ["-i", "/dev/null"]  # no share: untouched


# --- a runner's tool starts on Windows: anonymous pipes (winproc) ----------------------


_PY = sys.executable


def _alive(pid: int) -> bool:
    if sys.platform == "win32":
        return False  # only polled on posix; Windows is covered by CI's AppContainer test
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    try:  # a zombie the helper has not reaped yet is not running
        with open(f"/proc/{pid}/stat") as f:
            return f.read().split()[2] != "Z"
    except OSError:
        return True


def test_a_tool_start_through_anonymous_pipes_answers_as_asyncios_does() -> None:
    """Story 54.23: on Windows a runner starts its tools with winproc's
    ``create_subprocess_exec`` (``subprocess.Popen``, anonymous pipes: asyncio's own makes
    named pipes, which an AppContainer may not create). It answers as asyncio's does:
    ``communicate`` with the exit code set after, ``returncode`` None while it runs,
    ``stdout.readline`` and ``async for``, ``stderr.read(n)``, ``limit`` for long lines, and
    a large output on both pipes at once without a deadlock."""

    async def go() -> None:
        proc = await winproc.create_subprocess_exec(
            _PY,
            "-c",
            "import sys; sys.stdout.write('o'*(8<<20)); sys.stderr.write('e'*(8<<20)); sys.exit(3)",
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        out, err = await asyncio.wait_for(proc.communicate(), 60)
        assert out is not None and err is not None
        assert (len(out), len(err), proc.returncode) == (8 << 20, 8 << 20, 3)

        proc = await winproc.create_subprocess_exec(
            _PY,
            "-c",
            "import sys,time; print('a'); print('b'*100000); sys.stdout.flush();"
            " time.sleep(0.3); print('c')",
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            limit=1 << 20,
        )
        assert proc.returncode is None and proc.pid > 0
        assert await proc.stdout.readline() == b"a\n"  # type: ignore[union-attr]
        lines = [line async for line in proc.stdout]  # type: ignore[union-attr]
        assert lines == [b"b" * 100000 + b"\n", b"c\n"]
        assert await proc.stderr.read(4096) == b""  # type: ignore[union-attr]
        assert await proc.wait() == 0 and proc.returncode == 0

        proc = await winproc.create_subprocess_exec(
            _PY,
            "-c",
            "import sys; sys.stdout.write(sys.stdin.read().upper())",
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
        )
        out, err = await proc.communicate(b"hello")
        assert (out, err) == (b"HELLO", None)

    asyncio.run(go())


def test_a_tools_input_handle_reaches_it_through_anonymous_pipes(tmp_path: Path) -> None:
    """A descriptor given as the tool's standard input (the broker's handle for ``fd:``)
    reaches it: the tool reads and seeks it."""
    src = tmp_path / "in.bin"
    src.write_bytes(b"0123456789")

    async def go() -> bytes:
        fd = os.open(src, os.O_RDONLY | getattr(os, "O_BINARY", 0))
        try:
            proc = await winproc.create_subprocess_exec(
                _PY,
                "-c",
                "import os,sys; os.lseek(0, 4, 0); sys.stdout.buffer.write(os.read(0, 3))",
                stdin=fd,
                stdout=subprocess.PIPE,
            )
        finally:
            os.close(fd)
        out, _ = await proc.communicate()
        return out or b""

    assert asyncio.run(go()) == b"456"


def test_a_tool_that_times_out_or_is_cancelled_is_killed() -> None:
    """A caller's timeout (``wait_for`` then ``kill`` and ``wait``, as the media helpers do)
    ends the tool; a cancelled ``communicate`` or ``wait`` kills it too (on Windows with the
    Job Object it was put in, so what it started ends with it)."""

    async def timed_out() -> int:
        proc = await winproc.create_subprocess_exec(
            _PY, "-c", "import time; time.sleep(60)", stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        with pytest.raises(TimeoutError):
            await asyncio.wait_for(proc.communicate(), 0.5)
        # the cancelled communicate killed it: it exits at once, not in a minute
        code = await asyncio.wait_for(proc.wait(), 5)
        proc.kill()  # the caller's own kill after the timeout is harmless
        assert proc.returncode == code
        return proc.pid

    pid = asyncio.run(timed_out())
    assert not _alive(pid)

    async def cancelled() -> int:
        proc = await winproc.create_subprocess_exec(_PY, "-c", "import time; time.sleep(60)")
        task = asyncio.ensure_future(proc.wait())
        await asyncio.sleep(0.3)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        await asyncio.sleep(0.5)
        assert proc.returncode is not None
        return proc.pid

    pid = asyncio.run(cancelled())
    assert not _alive(pid)


def test_the_runner_starts_tools_through_anonymous_pipes_only_once_installed(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Installed (a Windows runner, after its sandbox): ``asyncio.create_subprocess_exec``
    is winproc's, so every stage's tool start takes it; not installed it is asyncio's own.
    The exec guard still sees each start (it is ``subprocess.Popen``'s audit event)."""
    monkeypatch.setattr(asyncio, "create_subprocess_exec", asyncio.create_subprocess_exec)
    monkeypatch.setattr(
        asyncio.subprocess, "create_subprocess_exec", asyncio.subprocess.create_subprocess_exec
    )
    ours: object = winproc.create_subprocess_exec
    assert vars(asyncio)["create_subprocess_exec"] is not ours
    winproc.install()
    assert vars(asyncio)["create_subprocess_exec"] is ours
    assert vars(asyncio.subprocess)["create_subprocess_exec"] is ours
    seen: list[object] = []

    def hook(event: str, args: tuple[object, ...]) -> None:
        if event == "subprocess.Popen":
            seen.append(args[1])

    sys.addaudithook(hook)

    async def go() -> bytes:
        proc = await asyncio.create_subprocess_exec(_PY, "-c", "print('x')", stdout=subprocess.PIPE)
        out, _ = await proc.communicate()
        return out

    assert asyncio.run(go()).strip() == b"x"
    assert any(isinstance(a, (list, tuple)) and "print('x')" in a for a in seen)


def test_a_kill_after_the_tool_exited_never_terminates_its_closed_job() -> None:
    """The waiter closes a tool's Job Object once it exits, while a kill may come from the
    loop before it hears of the exit: the kill then terminates nothing (a closed handle's
    value may already be another tool's job), and the two never overlap."""

    class K32:
        def __init__(self) -> None:
            self.calls: list[tuple[str, int]] = []

        def TerminateJobObject(self, h: int, _code: int) -> int:  # noqa: N802
            self.calls.append(("terminate", h))
            return 1

        def CloseHandle(self, h: int) -> int:  # noqa: N802
            self.calls.append(("close", h))
            return 1

    job = winproc._Job.__new__(winproc._Job)
    job._lock = threading.Lock()
    job._k32 = k32 = K32()
    job._handle = 77
    assert job.terminate()  # running: the kill reaches its job
    job.close()  # the waiter: the tool exited
    assert not job.terminate()  # a kill after: nothing terminated
    job.close()  # closed once
    assert k32.calls == [("terminate", 77), ("close", 77)]


def test_devnull_is_never_the_nul_device_for_a_tool(monkeypatch: pytest.MonkeyPatch) -> None:
    """An AppContainer may not open the ``nul`` device either, so ``DEVNULL`` (what the
    media helpers pass) is never it: the tool's standard input is an empty pipe, marked so
    the share routing treats it as ``DEVNULL``, and an output it discards goes to a pipe
    drained and dropped (a large one never blocks the tool)."""
    opened: list[str] = []
    real_open = os.open

    def no_nul(path: Any, *a: Any, **k: Any) -> int:
        opened.append(str(path))
        if str(path) == os.devnull:
            raise PermissionError(13, "Permission denied", path)
        return real_open(path, *a, **k)

    seen_stdin: list[object] = []
    real_init = subprocess.Popen.__init__

    def init(popen: Any, args: Any, **kw: Any) -> None:
        seen_stdin.append(kw.get("stdin"))
        real_init(popen, args, **kw)

    monkeypatch.setattr(os, "open", no_nul)
    monkeypatch.setattr(subprocess.Popen, "__init__", init)

    async def go() -> tuple[bytes | None, bytes | None, int | None, object, object]:
        proc = await winproc.create_subprocess_exec(
            _PY,
            "-c",
            "import sys; d = sys.stdin.read(); sys.stdout.write('x'*(4<<20));"
            " sys.stderr.write('in=%d' % len(d))",
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )
        out, err = await asyncio.wait_for(proc.communicate(), 60)
        return out, err, proc.returncode, proc.stdout, proc.stdin

    out, err, code, stdout, stdin = asyncio.run(go())
    assert (out, err, code, stdout, stdin) == (None, b"in=0", 0, None, None)
    assert os.devnull not in opened
    assert isinstance(seen_stdin[0], winproc.EmptyStdin) and seen_stdin[0].empty_stdin


@pytest.mark.skipif(sys.platform == "win32", reason="the fake tool is a script with a #! line")
def test_a_share_input_reaches_a_tool_started_through_anonymous_pipes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The two Windows shims together, as a runner installs them: a stage's asyncio start
    with ``stdin=DEVNULL`` (winproc's empty pipe, not the ``nul`` device) on a share input is
    routed (winfiles): the tool gets ``fd:`` and the brokered handle, which it seeks."""
    shim, asked = _shim(tmp_path, {})
    monkeypatch.setattr(subprocess.Popen, "__init__", shim.popen_init(subprocess.Popen.__init__))
    monkeypatch.setattr(sys, "audit", lambda *_: None)
    tool = str(_fake_tool(tmp_path, "ffmpeg"))

    async def go() -> bytes:
        proc = await winproc.create_subprocess_exec(
            tool, "-i", VIDEO, "pipe:1", stdin=subprocess.DEVNULL, stdout=subprocess.PIPE
        )
        out, _ = await proc.communicate()
        return out or b""

    assert json.loads(asyncio.run(go())) == [["-i", "fd:", "pipe:1"], "bytes"]
    assert asked == [{"op": "open", "path": VIDEO}]
