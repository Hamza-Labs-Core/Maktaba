"""The runner's OS sandbox (Epic 54, story 54.23 §3, T01): a process that applied a group's
sandbox cannot open the network, read the user's keys or run a shell, and still reads the
files its group was granted. Each probe runs in a child process (a sandbox is for good)."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest

pytestmark = pytest.mark.skipif(
    not (sys.platform == "darwin" or sys.platform.startswith("linux")),
    reason="runners run on macOS and Linux (the stages run in the worker on Windows)",
)

PROBE = textwrap.dedent(
    """
    import json, os, socket, subprocess, sys
    from maktaba_plugin_runner import sandbox
    granted, secret, run_dir, net = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4] == "1"
    policy = sandbox.Policy(read=[os.path.dirname(granted)], run_dir=run_dir, net=net)
    out = {"applied": None}
    if sys.argv[5] == "1":
        try:
            out["applied"] = sandbox.apply(policy).level
        except sandbox.SandboxUnavailable as exc:
            out["applied"] = "unavailable:" + exc.step
    def probe(name, fn):
        try:
            fn()
            out[name] = "allowed"
        except Exception as exc:
            out[name] = "denied:" + type(exc).__name__
    probe("read_granted", lambda: open(granted).read())
    probe("read_secret", lambda: open(secret).read())
    probe("write_run", lambda: open(os.path.join(run_dir, "x"), "w").write("x"))
    def tcp():
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(1)
        try:
            s.connect(("127.0.0.1", 9))
        except ConnectionRefusedError:
            pass  # refused by the address: the socket was allowed
        finally:
            s.close()
    probe("tcp", tcp)
    probe("shell", lambda: subprocess.run(["/bin/sh", "-c", "true"], check=True))
    print(json.dumps(out))
    """
)


def run_probe(tmp_path: Path, *, sandboxed: bool, net: bool = False) -> dict[str, str]:
    granted = tmp_path / "granted" / "media.txt"
    granted.parent.mkdir()
    granted.write_text("ok")
    secret = tmp_path / "home" / ".ssh" / "id_ed25519"
    secret.parent.mkdir(parents=True)
    secret.write_text("key")
    run_dir = tmp_path / "run"
    run_dir.mkdir()
    out = subprocess.run(
        [
            sys.executable,
            "-c",
            PROBE,
            str(granted.resolve()),
            str(secret.resolve()),
            str(run_dir.resolve()),
            "1" if net else "0",
            "1" if sandboxed else "0",
        ],
        capture_output=True,
        text=True,
        timeout=60,
        env={**os.environ, "HOME": str(tmp_path / "home")},
    )
    assert out.returncode == 0, out.stderr
    got: dict[str, str] = json.loads(out.stdout.strip().splitlines()[-1])
    return got


def test_without_the_sandbox_every_probe_succeeds(tmp_path: Path) -> None:
    got = run_probe(tmp_path, sandboxed=False)
    assert got["read_granted"] == got["read_secret"] == got["tcp"] == got["shell"] == "allowed"


def test_the_sandbox_denies_network_keys_and_shells_and_allows_its_files(tmp_path: Path) -> None:
    got = run_probe(tmp_path, sandboxed=True)
    if str(got["applied"]).startswith("unavailable"):
        pytest.skip(f"this machine cannot apply the sandbox: {got['applied']}")
    assert got["tcp"].startswith("denied"), got  # seccomp or Seatbelt: always in force
    if got["applied"] == "partial" and sys.platform.startswith("linux"):
        pytest.skip("no Landlock on this kernel: files are not limited (Degraded sandbox_partial)")
    assert got["read_granted"] == "allowed", got
    assert got["write_run"] == "allowed", got
    assert got["read_secret"].startswith("denied"), got
    assert got["tcp"].startswith("denied"), got
    assert got["shell"].startswith("denied"), got


def test_a_net_group_may_open_the_network(tmp_path: Path) -> None:
    got = run_probe(tmp_path, sandboxed=True, net=True)
    if str(got["applied"]).startswith("unavailable"):
        pytest.skip(f"this machine cannot apply the sandbox: {got['applied']}")
    assert got["tcp"] == "allowed", got
    assert got["read_secret"].startswith("denied"), got


def test_the_macos_profile_names_no_tcp_and_no_host() -> None:
    from maktaba_plugin_runner import sandbox

    text = sandbox.seatbelt_profile(
        sandbox.Policy(api_socket="/x/api.sock", own_socket="/x/r.sock", run_dir="/x")
    )
    assert "(deny default)" in text and "tcp" not in text.lower() and "remote ip" not in text
    assert "(allow network-outbound)" not in text
    assert "(allow network-outbound)" in sandbox.seatbelt_profile(sandbox.Policy(net=True))


def test_a_runner_refuses_to_start_with_secrets_in_its_environment() -> None:
    out = subprocess.run(
        [sys.executable, "-m", "maktaba_plugin_runner", "--group", "light"],
        capture_output=True,
        text=True,
        timeout=60,
        env={**os.environ, "DATABASE_URL": "postgres://x"},
    )
    assert out.returncode == 72, out.stderr
    assert "plugin_runner_env_refused" in out.stderr


FILES_PROBE = textwrap.dedent(
    """
    import json, os, subprocess, sys
    from maktaba_plugin_runner import sandbox
    library, cache, run_dir, tool = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    policy = sandbox.Policy(read=[library], write=[cache], run_dir=run_dir, exec=[tool])
    out = {}
    try:
        out["applied"] = sandbox.apply(policy).level
    except sandbox.SandboxUnavailable as exc:
        out["applied"] = "unavailable:" + exc.step
    def probe(name, fn):
        try:
            fn()
            out[name] = "allowed"
        except Exception as exc:
            out[name] = "denied:" + type(exc).__name__
    probe("read_library", lambda: open(os.path.join(library, "ep1.mp4"), "rb").read())
    probe("write_library", lambda: open(os.path.join(library, "ep1.ar.srt"), "w").write("x"))
    probe("write_cache", lambda: open(os.path.join(cache, "small.jpg"), "wb").write(b"x"))
    probe("exec_tool", lambda: subprocess.run([tool], check=True))
    probe("shell", lambda: subprocess.run(["/bin/sh", "-c", "true"], check=True))
    print(json.dumps(out))
    """
)


def test_a_files_group_writes_only_its_write_areas_and_runs_only_its_tools(
    tmp_path: Path,
) -> None:
    """Wave 2 (story 54.29): the thumbnails group reads the library, writes the thumbnail
    cache and runs ffmpeg; a write to a read-only area is a PermissionError (the worker then
    runs that job itself: sandbox_refused)."""
    library, cache, run_dir = tmp_path / "lib", tmp_path / "cache", tmp_path / "run"
    for d in (library, cache, run_dir):
        d.mkdir()
    (library / "ep1.mp4").write_bytes(b"video")
    tool = "/usr/bin/true" if os.path.exists("/usr/bin/true") else "/bin/true"
    out = subprocess.run(
        [sys.executable, "-c", FILES_PROBE, str(library.resolve()), str(cache.resolve()),
         str(run_dir.resolve()), tool],
        capture_output=True, text=True, timeout=60,
    )  # fmt: skip
    assert out.returncode == 0, out.stderr
    got = json.loads(out.stdout.strip().splitlines()[-1])
    if str(got["applied"]).startswith("unavailable"):
        pytest.skip(f"this machine cannot apply the sandbox: {got['applied']}")
    if got["applied"] == "partial" and sys.platform.startswith("linux"):
        pytest.skip("no Landlock on this kernel: files are not limited (Degraded sandbox_partial)")
    assert got["read_library"] == "allowed", got
    assert got["write_cache"] == "allowed", got
    assert got["write_library"] == "denied:PermissionError", got
    assert got["exec_tool"] == "allowed", got
    assert got["shell"].startswith("denied"), got


def test_a_tool_reads_its_own_installation(tmp_path: Path) -> None:
    from maktaba_plugin_runner.sandbox import tool_prefixes

    bundled = tmp_path / "Maktaba.app" / "Resources" / "ffmpeg" / "bin" / "ffmpeg"
    bundled.parent.mkdir(parents=True)
    bundled.write_bytes(b"")
    assert tool_prefixes(str(bundled)) == [str(bundled.parent.parent.resolve())]
    assert tool_prefixes("/opt/homebrew/Cellar/ffmpeg/8.1/bin/ffmpeg") == ["/opt/homebrew"]
    assert tool_prefixes("/usr/bin/ffmpeg") == ["/usr/bin/ffmpeg"]  # never all of /usr


NAMES_PROBE = textwrap.dedent(
    """
    import json, os, sys
    from maktaba_plugin_runner import sandbox
    library, run_dir, guarded = sys.argv[1], sys.argv[2], sys.argv[3] == "1"
    policy = sandbox.Policy(read=[library], write_named=[library], run_dir=run_dir)
    out = {}
    try:
        out["applied"] = sandbox.apply(policy).level
    except sandbox.SandboxUnavailable as exc:
        out["applied"] = "unavailable:" + exc.step
    if guarded:
        sandbox.guard_names(policy.write_named)
    def probe(name, fn):
        try:
            fn()
            out[name] = "allowed"
        except Exception as exc:
            out[name] = "denied:" + type(exc).__name__ + ":" + getattr(exc, "code", "")
    show = os.path.join(library, "show")
    def subtitle():
        tmp = os.path.join(show, "ep1.ar.srt.maktaba-tmp")
        with open(tmp, "w") as f:
            f.write("1")
        os.replace(tmp, os.path.join(show, "ep1.ar.srt"))
    probe("subtitle", subtitle)
    probe("vtt", lambda: open(os.path.join(show, "ep1.en-GB.vtt"), "w").write("x"))
    probe("evil", lambda: open(os.path.join(show, "evil.sh"), "w").write("x"))
    probe("over_video", lambda: open(os.path.join(show, "ep1.mp4"), "w").write("x"))
    probe("read_video", lambda: open(os.path.join(show, "ep1.mp4"), "rb").read())
    print(json.dumps(out))
    """
)


@pytest.mark.parametrize("guarded", [True, False])
def test_subtitles_write_only_subtitle_names_next_to_a_video(tmp_path: Path, guarded: bool) -> None:
    """Story 54.29 T04: library.subtitles. With the runner's guard any other name is
    plugin_scope (both systems); without it, Seatbelt alone refuses it on macOS."""
    library, run_dir = tmp_path / "lib", tmp_path / "run"
    (library / "show").mkdir(parents=True)
    run_dir.mkdir()
    (library / "show" / "ep1.mp4").write_bytes(b"video")
    out = subprocess.run(
        [sys.executable, "-c", NAMES_PROBE, str(library.resolve()), str(run_dir.resolve()),
         "1" if guarded else "0"],
        capture_output=True, text=True, timeout=60,
    )  # fmt: skip
    assert out.returncode == 0, out.stderr
    got = json.loads(out.stdout.strip().splitlines()[-1])
    if str(got["applied"]).startswith("unavailable"):
        pytest.skip(f"this machine cannot apply the sandbox: {got['applied']}")
    assert got["subtitle"] == got["vtt"] == got["read_video"] == "allowed", got
    assert (library / "show" / "ep1.ar.srt").read_text() == "1"
    if guarded:
        assert got["evil"] == got["over_video"] == "denied:PluginRefused:plugin_scope", got
    elif sys.platform == "darwin":
        assert got["evil"].startswith("denied:PermissionError"), got
        assert got["over_video"].startswith("denied:PermissionError"), got
    else:
        # Landlock matches folders, never names: without the guard any name is writable,
        # which is why the runner always installs it (sandbox.guard_names)
        assert got["evil"] == got["over_video"] == "allowed", got
        return
    assert (library / "show" / "ep1.mp4").read_bytes() == b"video"


def test_the_light_group_writes_subtitles_by_name_and_thumbnails_in_full() -> None:
    from maktaba_pipeline.plugins import firstparty
    from maktaba_plugin_runner.sandbox import policy_for, seatbelt_profile

    light = [dict(p.trusted) for p in firstparty.plugins() if p.trusted.get("runner") == "light"]
    p = policy_for(
        light,
        areas={"library": "/media/a;/media/b", "data.thumbnails": "/data/thumbnails"},
        api_socket="/run/api.sock",
        own_socket="/run/runner-light.sock",
        run_dir="/run",
    )
    assert p.read[:2] == ["/media/a", "/media/b"]
    assert p.write == ["/data/thumbnails"]
    assert p.write_named == ["/media/a", "/media/b"]
    profile = seatbelt_profile(p)
    assert '(allow file-read* file-write* (subpath "/media/a"))' not in profile
    assert '(regex #"^/media/a/(.*/)?' in profile


def test_the_files_group_rewrites_the_library_and_the_light_group_does_not() -> None:
    """convert and dedup rewrite and move library files, so they run in their own group
    (``files``): in ``light`` their write would cover subtitles' name-only rule."""
    from maktaba_pipeline.plugins import firstparty
    from maktaba_plugin_runner.sandbox import policy_for

    def group(name: str) -> list[dict[str, object]]:
        return [dict(p.trusted) for p in firstparty.plugins() if p.trusted.get("runner") == name]

    assert {p.id for p in firstparty.plugins() if p.trusted.get("runner") == "files"} == {
        "tv.maktaba.convert",
        "tv.maktaba.dedup",
    }
    areas = {"library": "/media/a", "trash": "/media/.trash", "data.thumbnails": "/data/t"}
    kw = {"api_socket": "/run/api.sock", "run_dir": "/run"}
    files = policy_for(group("files"), areas=areas, own_socket="/run/runner-files.sock", **kw)
    assert set(files.write) >= {"/media/a", "/media/.trash"}
    light = policy_for(group("light"), areas=areas, own_socket="/run/runner-light.sock", **kw)
    assert "/media/a" not in light.write and "/media/.trash" not in light.write
