"""A first-party runner's sandbox on Windows, probed from inside (story 54.23; CI only).

TestARunnerOnWindowsInItsAppContainer starts this through the launcher (runner-sandbox-run)
as a runner group: ``--policy`` prints the group's policy (the PROBE_* folders) the way the
runner does; otherwise it checks its AppContainer with the runner's own sandbox code, tries
what a runner must not do and what it may, calls the plugin API through its API pipe, and
serves the runner's hello on its back pipe (the real Runner and winpipe code), the probes'
results in it. With PROBE_CONTROL=1 (the positive control, outside any sandbox) it prints
the probes and exits.
"""

from __future__ import annotations

import asyncio
import ctypes
import json
import os
import socket
import subprocess
import sys
from pathlib import Path
from typing import Any

from maktaba_plugin_runner import sandbox, winpipe
from maktaba_plugin_runner.server import Runner

env = os.environ.get
policy = sandbox.Policy(
    read=[env("PROBE_READ_DIR", "")],
    write=[env("PROBE_WRITE_DIR", "")],
    run_dir=env("PROBE_RUN_DIR", ""),
    net=env("PROBE_NET") == "1",
)
probes: dict[str, str] = {}


def probe(name: str, fn: Any) -> None:
    try:
        fn()
        probes[name] = "allowed"
    except Exception as exc:  # noqa: BLE001 - every refusal is a result
        probes[name] = "denied:" + type(exc).__name__


def tcp(host: str, port: int) -> None:
    """Two-way or nothing: a bind or a send can succeed while the traffic is dropped."""
    with socket.create_connection((host, port), timeout=3) as s:
        s.settimeout(3)
        s.sendall(b"ping")
        if s.recv(4) != b"pong":
            raise ConnectionError("no answer")


def internet() -> None:
    with socket.create_connection(("example.com", 80), timeout=5) as s:
        s.settimeout(5)
        s.sendall(b"HEAD / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
        if not s.recv(16).startswith(b"HTTP/1."):
            raise ConnectionError("no answer")


def kernel_exec() -> None:
    """A child process past the runner's own guard (CreateProcessW itself): the Job Object
    allows none to a group without tools."""
    from ctypes import wintypes

    class SI(ctypes.Structure):
        _fields_ = [("cb", wintypes.DWORD), ("_", ctypes.c_byte * 100)]

    class PI(ctypes.Structure):
        _fields_ = [("hp", wintypes.HANDLE), ("ht", wintypes.HANDLE), ("pid", wintypes.DWORD), ("tid", wintypes.DWORD)]

    si, pi = SI(), PI()
    si.cb = ctypes.sizeof(SI)
    cmd = ctypes.create_unicode_buffer(f'"{sys.executable}" -c "pass"')
    k = ctypes.WinDLL("kernel32", use_last_error=True)
    if not k.CreateProcessW(None, cmd, None, None, False, 0, None, None, ctypes.byref(si), ctypes.byref(pi)):
        raise PermissionError(ctypes.get_last_error())
    k.TerminateProcess(pi.hp, 0)


def open_pipe(name: str) -> None:
    with open(name, "r+b", buffering=0):
        pass


def run_probes() -> None:
    probe("read_granted", lambda: Path(env("PROBE_READ_DIR", ""), "granted.txt").read_text())
    probe("read_secret", lambda: Path(env("PROBE_SECRET", "")).read_text())
    probe("write_granted", lambda: Path(env("PROBE_WRITE_DIR", ""), "w.txt").write_text("x"))
    probe("write_elsewhere", lambda: Path(env("PROBE_DENY_DIR", ""), "w.txt").write_text("x"))
    for i, addr in enumerate((env("PROBE_CANARIES") or "").split(";")):
        if addr:
            host, _, port = addr.rpartition(":")
            probe(f"tcp_{i}", lambda h=host, p=int(port): tcp(h, p))
    if policy.net:
        probe("internet", internet)
    probe("exec", lambda: subprocess.run([sys.executable, "-c", "pass"], check=True))
    if sys.platform == "win32":
        probe("exec_kernel", kernel_exec)
    probe("other_group_pipe", lambda: open_pipe(env("PROBE_OTHER_PIPE", "")))
    probe("own_caller_pipe", lambda: open_pipe(env("PROBE_CALLER_PIPE", "")))


class ProbeRunner(Runner):
    def hello(self) -> dict[str, Any]:
        return {**super().hello(), "probes": probes}


async def serve() -> None:
    t = winpipe.PipeTransport(env(winpipe.API_ENV, ""), "probe-token")
    try:
        answer = await t.call("ping", {})
        probes["api"] = "allowed" if answer.get("ok") else f"answer:{answer}"
    except Exception as exc:  # noqa: BLE001
        probes["api"] = "denied:" + type(exc).__name__
    finally:
        await t.close()
    r = ProbeRunner(group="e2e", api_socket="", stages={}, plugin_of={}, bundle="e2e")
    await r.serve(Path("unused.sock"))


def main() -> int:
    if "--policy" in sys.argv:
        sys.stdout.write(json.dumps(sandbox.policy_doc(policy)) + "\n")
        return 0
    if env("PROBE_CONTROL") == "1":
        run_probes()
        sys.stdout.write(json.dumps(probes) + "\n")
        return 0
    try:
        probes["sandbox"] = sandbox.apply(policy).level
    except sandbox.SandboxUnavailable as exc:
        sys.stderr.write(f"sandbox_unavailable: {exc.step}: {exc}\n")
        return 70
    run_probes()
    sys.stderr.write("probes: " + json.dumps(probes) + "\n")
    asyncio.run(serve())
    return 0


if __name__ == "__main__":
    sys.exit(main())
