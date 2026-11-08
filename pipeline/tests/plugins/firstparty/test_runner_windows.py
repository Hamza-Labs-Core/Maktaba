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
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.native import PluginRefused

from maktaba_plugin_runner import sandbox, winpipe


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
