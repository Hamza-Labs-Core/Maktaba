"""``cmd update.convert`` (Epic 35 story 35.8 AC8): a worker-only machine downloads the
home's release, verifies it itself against the pinned signing identity and checksums.txt,
and runs the release's ``maktaba-server setup --node --converting`` outside its service."""

from __future__ import annotations

import asyncio
import hashlib
import io
import json
import tarfile
import zipfile
from collections.abc import Mapping
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster.agent import convert as cv
from maktaba_pipeline.cluster.agent.client import Commands
from maktaba_pipeline.cluster.agent.convert import Converter, ConvertError

FIXTURES = Path(__file__).resolve().parents[2] / "fixtures" / "release-v0.3.5"
REPO_ROOT = Path(__file__).resolve().parents[4]


def sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


# --- the signature: a real release, verified offline with the pinned root -----------------


# real signatures and the pinned root: sigstore-python's first import alone takes seconds
@pytest.mark.integration
def test_a_real_release_verifies_with_the_pinned_identity() -> None:
    checksums = (FIXTURES / "checksums.txt").read_bytes()
    bundle = (FIXTURES / "checksums.txt.sigstore.json").read_bytes()
    cv.verify_signed(checksums, bundle, "v0.3.5", cv.DEFAULT_REPO)


# real signatures and the pinned root: sigstore-python's first import alone takes seconds
@pytest.mark.integration
def test_another_tag_or_repository_is_someone_else() -> None:
    checksums = (FIXTURES / "checksums.txt").read_bytes()
    bundle = (FIXTURES / "checksums.txt.sigstore.json").read_bytes()
    for tag, repo in (("v0.3.4", cv.DEFAULT_REPO), ("v0.3.5", "someone/Maktaba")):
        with pytest.raises(ConvertError) as err:
            cv.verify_signed(checksums, bundle, tag, repo)
        assert err.value.code == "verify_identity" and err.value.step == "verify"


# real signatures and the pinned root: sigstore-python's first import alone takes seconds
@pytest.mark.integration
def test_a_changed_checksums_file_is_refused() -> None:
    checksums = (FIXTURES / "checksums.txt").read_bytes() + b"0" * 64 + b"  ./evil\n"
    bundle = (FIXTURES / "checksums.txt.sigstore.json").read_bytes()
    with pytest.raises(ConvertError) as err:
        cv.verify_signed(checksums, bundle, "v0.3.5", cv.DEFAULT_REPO)
    assert err.value.code == "verify_signature"


@pytest.mark.unit
def test_the_pinned_root_is_the_servers() -> None:
    go_root = REPO_ROOT / "shared" / "go" / "releaseverify" / "trusted_root.json"
    if not go_root.exists():
        pytest.skip("not in the repository")
    assert cv.TRUSTED_ROOT.read_bytes() == go_root.read_bytes(), (
        "the pipeline's Sigstore root differs from releaseverify's: copy it (trusted-root.yml does)"
    )


@pytest.mark.unit
def test_a_fork_is_named_by_its_setting() -> None:
    assert cv.release_repo({}) == cv.DEFAULT_REPO
    assert cv.release_repo({cv.REPO_ENV: "someone/fork"}) == "someone/fork"
    with pytest.raises(ConvertError):
        cv.release_repo({cv.REPO_ENV: "../x"})


# --- the pieces ----------------------------------------------------------------------------


@pytest.mark.unit
def test_checksums_parse_as_the_go_verifier_reads_them() -> None:
    sums = cv.parse_checksums((FIXTURES / "checksums.txt").read_bytes())
    assert (
        sums["release.json"] == "ddfa4cf74a5c41f709bfba8d8cb9ff6094feeca4fa261cc19281c34217ef1753"
    )
    for bad in (
        b"nothex  a\n",
        b"%s  a/b\n" % (b"0" * 64),
        b"%s  a\n%s  a\n" % (b"0" * 64, b"1" * 64),
    ):
        with pytest.raises(ConvertError) as err:
            cv.parse_checksums(bad)
        assert err.value.code == "verify_manifest"


@pytest.mark.unit
@pytest.mark.parametrize(
    ("system", "machine", "want"),
    [
        ("Linux", "x86_64", "linux/amd64"),
        ("Linux", "aarch64", "linux/arm64"),
        ("Darwin", "arm64", "darwin/arm64"),
        ("Windows", "AMD64", "windows/amd64"),
    ],
)
def test_platforms_are_named_as_the_release_names_them(
    system: str, machine: str, want: str
) -> None:
    assert cv.go_platform(system, machine) == want


@pytest.mark.unit
def test_the_binary_comes_out_of_either_archive() -> None:
    assert cv.extract_binary("x.tar.gz", targz({"maktaba-server": b"S"}), "maktaba-server") == (
        "maktaba-server",
        b"S",
    )
    assert cv.extract_binary(
        "x.zip", zipped({"dir/maktaba-server.exe": b"W"}), "maktaba-server"
    ) == (
        "maktaba-server.exe",
        b"W",
    )
    with pytest.raises(ConvertError) as err:
        cv.extract_binary("x.tar.gz", targz({"other": b"x"}), "maktaba-server")
    assert err.value.code == "release_incomplete"


@pytest.mark.unit
def test_the_mirror_first_then_github() -> None:
    seen: list[tuple[str, dict[str, str]]] = []
    bodies = {
        "https://gh.test/a": b"from github",
        "http://home:8080/api/cluster/updates/artifacts/1.5.0/b": b"from the mirror",
    }

    def get(url: str, headers: Mapping[str, str]) -> bytes | None:
        seen.append((url, dict(headers)))
        return bodies.get(url)

    job = {"version": "1.5.0", "assets": {"a": "https://gh.test/a"}, "download_token": "mkt_dl_t.s"}
    fetch = cv.release_source("http://home:8080/", job, get)
    assert fetch("b") == b"from the mirror"
    assert fetch("a") == b"from github"
    assert fetch("c") is None
    # the home's token goes to the home's mirror alone, never to GitHub
    for url, headers in seen:
        if url.startswith("http://home:8080/api/cluster/updates/artifacts/1.5.0/"):
            assert headers == {"Authorization": "Bearer mkt_dl_t.s"}, url
        else:
            assert "Authorization" not in headers, url
    assert seen[0][0] == "http://home:8080/api/cluster/updates/artifacts/1.5.0/b"
    assert ("https://gh.test/a", {}) in seen
    # without a token (an older home): no header at all
    seen.clear()
    cv.release_source("http://home:8080", {"version": "1.5.0"}, get)("b")
    assert seen == [("http://home:8080/api/cluster/updates/artifacts/1.5.0/b", {})]

    def broken(url: str, headers: Mapping[str, str]) -> bytes | None:
        raise OSError("connection refused")

    with pytest.raises(ConvertError) as err:
        cv.release_source("http://home:8080", {"version": "1.5.0"}, broken)("a")
    assert err.value.code == "download_failed"


@pytest.mark.unit
def test_setup_runs_outside_the_workers_service(tmp_path: Path) -> None:
    argv = ["/x/maktaba-server", "setup", "--node", "--converting", "--from", "/x"]
    log, done = tmp_path / "setup.log", tmp_path / "setup.exit"
    user = cv.detached_command(argv, log, done, system="Linux", env={}, systemd=True, root=False)
    assert user[:6] == ["systemd-run", "--user", "--scope", "--quiet", "--collect", "--"]
    assert user[6:8] == ["/bin/sh", "-c"] and "echo $? >" in user[8]
    system = cv.detached_command(argv, log, done, system="Linux", env={}, systemd=True, root=True)
    assert system[:5] == ["systemd-run", "--scope", "--quiet", "--collect", "--"]
    mac = cv.detached_command(argv, log, done, system="Darwin", env={}, systemd=False)
    assert mac[:2] == ["/bin/sh", "-c"]
    win = cv.detached_command(
        [r"C:\m\maktaba-server.exe", "setup"],
        Path(r"C:\m\setup.log"),
        Path(r"C:\m\setup.exit"),
        system="Windows",
        env={"MAKTABA_NODE_CONFIG": r"C:\n.json", "OTHER": "x", "MAKTABA_BANG": "a!b"},
    )
    assert win[0] == "powershell.exe" and "Win32_Process" in win[4]
    line = win[5]
    assert line.startswith('cmd.exe /d /s /v:on /c "')
    assert r'set "MAKTABA_NODE_CONFIG=C:\n.json" & ' in line and "OTHER" not in line
    assert "MAKTABA_BANG" not in line  # cmd would expand its !
    run = r'C:\m\maktaba-server.exe setup > "C:\m\setup.log" 2>&1'
    assert run + r' & echo !errorlevel! > "C:\m\setup.exit"' in line


# --- the conversion ------------------------------------------------------------------------


def targz(files: Mapping[str, bytes]) -> bytes:
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, data in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            tf.addfile(info, io.BytesIO(data))
    return buf.getvalue()


def zipped(files: Mapping[str, bytes]) -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        for name, data in files.items():
            zf.writestr(name, data)
    return buf.getvalue()


PLATFORM = "linux/amd64"
SERVER = b"#!/bin/sh\necho maktaba-server 1.5.0\n"


def fake_release(version: str = "1.5.0", **change: Any) -> dict[str, bytes]:
    """A release's files (unsigned: the tests replace the signature check)."""
    archive_name = f"maktaba-server-{version}-linux-amd64.tar.gz"
    archive = targz({"maktaba-server": SERVER, "maktaba-launcher": b"L"})
    wheel, cons = (
        f"maktaba_pipeline-{version}-py3-none-any.whl",
        f"maktaba-pipeline-{version}-constraints.txt",
    )
    manifest: dict[str, Any] = {
        "v": 1,
        "version": version,
        "tag": "v" + version,
        "assets": {
            PLATFORM: {
                "archive": archive_name,
                "sha256": sha(archive),
                "files": {"maktaba-server": sha(SERVER), "maktaba-launcher": sha(b"L")},
            }
        },
        "pipeline": {"wheel": wheel, "constraints": cons},
    }
    manifest.update(change)
    files = {
        archive_name: archive,
        wheel: b"wheel",
        cons: b"x==1 --hash=sha256:" + b"0" * 64 + b"\n",
        "release.json": json.dumps(manifest).encode(),
    }
    files["checksums.txt"] = "".join(
        f"{sha(b)}  ./{n}\n" for n, b in sorted(files.items())
    ).encode()
    files["checksums.txt.sigstore.json"] = b"{}"
    return files


class FakeSetup:
    """Plays the release's setup: records its command line, writes its log and exit code."""

    def __init__(self, code: int = 0, log: str = "") -> None:
        self.code, self.log = code, log
        self.calls: list[list[str]] = []

    def __call__(self, argv: list[str], log: Path, exit_file: Path) -> None:
        self.calls.append(argv)
        log.write_text(self.log)
        exit_file.write_text(f"{self.code} \r\n")


def converter(
    tmp_path: Path,
    files: Mapping[str, bytes],
    *,
    setup: FakeSetup | None = None,
    verify: Any = None,
) -> tuple[Converter, FakeSetup, list[tuple[bytes, bytes, str, str]]]:
    verified: list[tuple[bytes, bytes, str, str]] = []

    def ok(checksums: bytes, bundle: bytes, tag: str, repo: str) -> None:
        verified.append((checksums, bundle, tag, repo))

    setup = setup or FakeSetup()
    conv = Converter(
        "http://home:8080",
        root=tmp_path / "convert",
        source=lambda home, job: lambda name: files.get(name),
        verify=verify or ok,
        spawn=setup,
        platform_name=PLATFORM,
        env={},
        poll_sec=0.01,
    )
    return conv, setup, verified


JOB = {"run_id": "run-1", "version": "1.5.0", "tag": "v1.5.0", "assets": {}}


async def finished(conv: Converter, job: Mapping[str, Any] = JOB) -> dict[str, Any]:
    for _ in range(500):
        ok, ans = await conv.command(job)
        assert ok, ans
        if ans["state"] in ("done", "failed"):
            return ans
        await asyncio.sleep(0.01)
    raise AssertionError(f"still {ans}")


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_verified_release_is_set_up(tmp_path: Path) -> None:
    files = fake_release()
    conv, setup, verified = converter(tmp_path, files)
    ok, first = await conv.command(JOB)
    assert ok and first["state"] in ("started", "running")
    ans = await finished(conv)
    assert ans["state"] == "done", ans
    assert verified == [(files["checksums.txt"], b"{}", "v1.5.0", cv.DEFAULT_REPO)]
    [argv] = setup.calls
    exe = Path(argv[0])
    assert (
        argv[1:5] == ["setup", "--node", "--converting", "--from"] and Path(argv[5]) == exe.parent
    )
    assert exe.name == "maktaba-server" and exe.read_bytes() == SERVER
    for name in ("release.json", "checksums.txt", "checksums.txt.sigstore.json"):
        assert (exe.parent / name).read_bytes() == files[name]
    assert (exe.parent / "maktaba_pipeline-1.5.0-py3-none-any.whl").exists()
    # asked again for the same run: how it ended, nothing run twice
    assert (await conv.command(JOB))[1]["state"] == "done" and len(setup.calls) == 1


@pytest.mark.integration
@pytest.mark.asyncio
async def test_one_conversion_at_a_time(tmp_path: Path) -> None:
    conv, _, _ = converter(tmp_path, fake_release(), setup=FakeSetup())
    gate = asyncio.Event()
    real = conv._prepare

    def slow(c: Any, job: Any) -> Path:
        asyncio.run_coroutine_threadsafe(gate.wait(), loop).result(5)
        return real(c, job)

    loop = asyncio.get_running_loop()
    conv._prepare = slow  # type: ignore[method-assign,assignment]
    assert (await conv.command(JOB))[0]
    ok, ans = await conv.command({**JOB, "run_id": "run-2"})
    assert not ok and ans["code"] == "convert_refused" and "run-1" in ans["detail"]
    gate.set()
    assert (await finished(conv))["state"] == "done"
    ok, ans = await conv.command({"run_id": "", "version": ""})
    assert not ok and ans["code"] == "convert_refused"


@pytest.mark.integration
@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("change", "step", "code"),
    [
        ("tampered_archive", "verify", "verify_checksum"),
        ("tampered_binary", "verify", "verify_checksum"),
        ("no_platform", "download", "release_incomplete"),
        ("other_version", "verify", "version_mismatch"),
        ("missing_wheel", "download", "release_incomplete"),
    ],
)
async def test_what_does_not_verify_is_never_run(
    tmp_path: Path, change: str, step: str, code: str
) -> None:
    files = dict(fake_release())
    archive = next(n for n in files if n.endswith(".tar.gz"))
    match change:
        case "tampered_archive":
            files[archive] = targz({"maktaba-server": b"evil"})
        case "tampered_binary":
            # an archive checksums.txt lists, whose binary release.json does not
            files = fake_release()
            m = json.loads(files["release.json"])
            m["assets"][PLATFORM]["files"]["maktaba-server"] = "0" * 64
            files = refreshed({**files, "release.json": json.dumps(m).encode()})
        case "no_platform":
            files = fake_release(assets={"darwin/arm64": {"archive": "x.tar.gz"}})
        case "other_version":
            files = fake_release(tag="v1.4.0")
        case "missing_wheel":
            del files["maktaba_pipeline-1.5.0-py3-none-any.whl"]
    conv, setup, _ = converter(tmp_path, files)
    ans = await finished(conv)
    assert (ans["state"], ans["step"], ans["code"]) == ("failed", step, code), ans
    assert setup.calls == []


def refreshed(files: dict[str, bytes]) -> dict[str, bytes]:
    """checksums.txt made again over files (a release signed as it is)."""
    body = {
        n: b for n, b in files.items() if n not in ("checksums.txt", "checksums.txt.sigstore.json")
    }
    files["checksums.txt"] = "".join(f"{sha(b)}  ./{n}\n" for n, b in sorted(body.items())).encode()
    return files


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_signature_from_someone_else_is_never_run(tmp_path: Path) -> None:
    def refuse(checksums: bytes, bundle: bytes, tag: str, repo: str) -> None:
        raise ConvertError("verify", "verify_identity", "checksums.txt: SANs do not match")

    conv, setup, _ = converter(tmp_path, fake_release(), verify=refuse)
    ans = await finished(conv)
    assert ans["code"] == "verify_identity" and setup.calls == []


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_setup_that_fails_says_why(tmp_path: Path) -> None:
    conv, _, _ = converter(
        tmp_path, fake_release(), setup=FakeSetup(code=1, log="register the service: access denied")
    )
    ans = await finished(conv)
    assert (ans["state"], ans["step"], ans["code"]) == ("failed", "install", "install_failed")
    assert "exited 1" in ans["detail"] and "access denied" in ans["detail"]


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_download_that_fails(tmp_path: Path) -> None:
    def source(home: str, job: Mapping[str, Any]) -> Any:
        def fetch(name: str) -> bytes | None:
            raise ConvertError("download", "download_failed", f"{name}: connection refused")

        return fetch

    conv, setup, _ = converter(tmp_path, fake_release())
    conv.source = source
    ans = await finished(conv)
    assert (ans["state"], ans["code"]) == ("failed", "download_failed") and setup.calls == []


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_restarted_worker_follows_the_setup_it_started(tmp_path: Path) -> None:
    # an earlier run of this worker started setup and stopped (its state on disk)
    folder = tmp_path / "convert" / "run-1"
    prev = cv.Conversion(
        run_id="run-1", version="1.5.0", dir=folder, state="running", step="install"
    )
    prev.save()
    conv, setup, _ = converter(tmp_path, fake_release())
    ok, ans = await conv.command(JOB)
    assert ok and ans["state"] == "running" and ans["step"] == "install"
    (folder / "setup.exit").write_text("0\n")
    assert (await finished(conv))["state"] == "done"
    assert setup.calls == []  # never a second setup


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_agent_answers_update_convert() -> None:
    from tests.cluster.agent.fakes import running_hub
    from tests.cluster.agent.test_channel import make_agent, node_hub, running

    async def convert(args: Mapping[str, Any]) -> tuple[bool, dict[str, Any]]:
        return True, {"state": "started", "version": args["version"]}

    hub, key = node_hub()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, commands=Commands(convert=convert))
        async with running(agent):
            s = await hub.wait_session(1)
            await s.send(
                {"t": "cmd", "id": "c1", "name": "update.convert", "args": {"version": "1.5.0"}}
            )
            r = await s.next("cmd.result", lambda f: f["id"] == "c1")
            assert r["ok"] and r["detail"] == {"state": "started", "version": "1.5.0"}
        bare = make_agent(hub, node_key=key)
        async with running(bare):
            s = await hub.wait_session(2)
            await s.send({"t": "cmd", "id": "c2", "name": "update.convert", "args": {}})
            r = await s.next("cmd.result", lambda f: f["id"] == "c2")
            assert not r["ok"] and "converts nothing" in r["detail"]
