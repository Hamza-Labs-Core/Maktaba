"""``cmd update.convert``: a worker-only machine becomes a full node (Epic 35 story 35.8 AC8,
design §9.4).

A machine that joined with the Epic 31 installer runs the pipeline alone. On its next update
the home's run drains it and sends this worker ``update.convert`` (the run's job: ``run_id``,
``version``, ``tag``, ``assets``). The worker downloads the release for this platform (the
home's artifact mirror first, the job's GitHub Releases URLs otherwise) and verifies it
itself before anything runs: ``checksums.txt``'s Sigstore bundle against the pinned signing
identity (the release workflow of the repository, run by a tag push for exactly this tag,
on GitHub's runners), with the Sigstore root the server pins; then ``release.json``, the
platform archive, the pipeline's wheel and constraints against ``checksums.txt``, and the
server binary against ``release.json``. Only then does it run the release's own
``maktaba-server setup --node --converting --from <dir>``, outside this worker's service:
setup installs the layout, registers the launcher's service and removes the old worker units
(this one among them) once the new service answers.

The command answers at once (``started``); asked again for the same run it says how far the
conversion got (``running`` with its step, ``done``, or ``failed`` with a code and the
detail). The identity is this machine's own (``MAKTABA_UPDATE_REPO`` retargets a fork, as on
every Maktaba machine): nothing in the job can change what is trusted.
"""

from __future__ import annotations

import asyncio
import contextlib
import hashlib
import io
import json
import os
import platform
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import zipfile
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from ...log import get_logger
from .. import share
from .logship import AGENT_LOGGER

# the agent's logger: these lines go to the home with the worker's logs
_log = get_logger(logger=AGENT_LOGGER)

# --- the pinned signing identity (shared/go/releaseverify/identity.go) -------------------

DEFAULT_REPO = "Hamza-Labs-Core/Maktaba"
ISSUER = "https://token.actions.githubusercontent.com"
WORKFLOW_PATH = ".github/workflows/release.yml"
BUILD_TRIGGER = "push"
RUNNER_ENVIRONMENT = "github-hosted"
DEFAULT_REPO_ID = "1229315126"
DEFAULT_OWNER_ID = "247904019"
REPO_ENV = "MAKTABA_UPDATE_REPO"

CHECKSUMS = "checksums.txt"
BUNDLE = "checksums.txt.sigstore.json"
MANIFEST = "release.json"

#: the Sigstore root the server compiles in (a copy, kept equal by trusted-root.yml)
TRUSTED_ROOT = Path(__file__).with_name("sigstore_trusted_root.json")

_REPO_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$")
_TAG_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")
_HEX_RE = re.compile(r"^[0-9a-f]{64}$")

#: the largest file of a release this downloads (the platform archive, the wheel)
MAX_FILE = 512 << 20
#: how long one download may take
DOWNLOAD_TIMEOUT = 600.0


class ConvertError(Exception):
    """A conversion that stopped: the step it stopped at, a code of update-attempts.md."""

    def __init__(self, step: str, code: str, detail: str) -> None:
        super().__init__(detail)
        self.step, self.code, self.detail = step, code, detail


def release_repo(env: Mapping[str, str] | None = None) -> str:
    """The repository whose releases this machine trusts (``MAKTABA_UPDATE_REPO``, else
    Maktaba's)."""
    env = os.environ if env is None else env
    repo = (env.get(REPO_ENV) or "").strip() or DEFAULT_REPO
    if not _REPO_RE.match(repo) or ".." in repo:
        raise ConvertError(
            "verify", "verify_identity", f"invalid {REPO_ENV} {repo!r} (want owner/name)"
        )
    return repo


def identity_policy(repo: str, tag: str) -> Any:
    """The sigstore-python policy of the pinned identity for ``tag`` (the Go verifier's,
    identity.go): the SAN, the issuer and every Fulcio extension it checks; the numeric
    ids for Maktaba's own repository (a fork named by MAKTABA_UPDATE_REPO is pinned by
    name)."""
    from sigstore.verify import policy as p

    if not _TAG_RE.match(tag):
        raise ConvertError("verify", "verify_manifest", f"not a release tag: {tag!r}")
    repo_uri = f"https://github.com/{repo}"
    san = f"{repo_uri}/{WORKFLOW_PATH}@refs/tags/{tag}"
    owner = repo.split("/", 1)[0]
    checks: list[Any] = [
        p.Identity(identity=san, issuer=ISSUER),
        p.OIDCSourceRepositoryURI(repo_uri),
        p.OIDCSourceRepositoryRef(f"refs/tags/{tag}"),
        p.OIDCSourceRepositoryOwnerURI(f"https://github.com/{owner}"),
        p.OIDCBuildConfigURI(san),
        p.OIDCBuildTrigger(BUILD_TRIGGER),
        p.OIDCRunnerEnvironment(RUNNER_ENVIRONMENT),
    ]
    if repo == DEFAULT_REPO:
        checks += [
            p.OIDCSourceRepositoryIdentifier(DEFAULT_REPO_ID),
            p.OIDCSourceRepositoryOwnerIdentifier(DEFAULT_OWNER_ID),
        ]
    return p.AllOf(checks)


def verify_signed(checksums: bytes, bundle: bytes, tag: str, repo: str) -> None:
    """``checksums.txt`` signed by the pinned identity for ``tag`` (its Sigstore bundle,
    offline: the inclusion proof and the log's signed time in the bundle, the pinned
    root)."""
    from sigstore.errors import Error as SigstoreError
    from sigstore.models import Bundle, TrustedRoot
    from sigstore.verify import Verifier

    pol = identity_policy(repo, tag)
    try:
        b = Bundle.from_json(bundle)
    except Exception as exc:  # noqa: BLE001 - a bundle that cannot be read verifies nothing
        raise ConvertError("verify", "verify_signature", f"{BUNDLE}: {exc}") from exc
    try:
        verifier = Verifier(trusted_root=TrustedRoot.from_file(str(TRUSTED_ROOT)))
    except Exception as exc:  # noqa: BLE001
        raise ConvertError("verify", "trust_root", f"the Sigstore root: {exc}") from exc
    try:
        verifier.verify_artifact(checksums, b, pol)
    except SigstoreError as exc:
        text = str(exc)
        # the policy's words (a SAN, an extension that does not match): someone else signed
        who = "SAN" in text or "extension" in text.lower() or "issuer" in text.lower()
        code = "verify_identity" if who else "verify_signature"
        raise ConvertError("verify", code, f"{CHECKSUMS}: {text}") from exc


def parse_checksums(data: bytes) -> dict[str, str]:
    """``checksums.txt`` (``<sha256>  <name>`` lines, as the Go ParseChecksums reads it)."""
    out: dict[str, str] = {}
    for n, raw in enumerate(data.decode("utf-8", "replace").splitlines(), 1):
        line = raw.rstrip("\r")
        if not line.strip():
            continue
        digest, _, name = line.partition(" ")
        name = name.lstrip(" ").removeprefix("*").removeprefix("./")
        if not _HEX_RE.match(digest) or not name or "/" in name or "\\" in name or name in out:
            raise ConvertError(
                "verify", "verify_manifest", f"{CHECKSUMS} line {n} is not '<sha256>  <name>'"
            )
        out[name] = digest
    return out


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def check(sums: Mapping[str, str], name: str, data: bytes) -> None:
    want = sums.get(name)
    if want is None:
        raise ConvertError("verify", "verify_checksum", f"{name} is not listed in {CHECKSUMS}")
    if sha256(data) != want:
        raise ConvertError(
            "verify", "verify_checksum", f"{name}: sha256 {sha256(data)}, {CHECKSUMS} lists {want}"
        )


def go_platform(system: str | None = None, machine: str | None = None) -> str:
    """This machine as the release names platforms (``linux/amd64``, ``darwin/arm64``,
    ``windows/amd64``)."""
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    goos = {"darwin": "darwin", "linux": "linux", "windows": "windows"}.get(system, system)
    goarch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(
        machine, machine
    )
    return f"{goos}/{goarch}"


def extract_binary(archive_name: str, archive: bytes, base: str) -> tuple[str, bytes]:
    """The entry whose base name is ``base`` (or ``base.exe``) of a ``.zip`` or ``.tar.gz``
    archive (the Go ExtractBinary)."""

    def match(name: str) -> bool:
        b = name.replace("\\", "/").rsplit("/", 1)[-1]
        return b in (base, base + ".exe")

    if archive_name.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(archive)) as zf:
            for info in zf.infolist():
                if not info.is_dir() and match(info.filename):
                    return info.filename.replace("\\", "/").rsplit("/", 1)[-1], zf.read(info)
    elif archive_name.endswith((".tar.gz", ".tgz")):
        with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tf:
            for member in tf.getmembers():
                if member.isfile() and match(member.name):
                    f = tf.extractfile(member)
                    if f is not None:
                        return member.name.rsplit("/", 1)[-1], f.read()
    else:
        raise ConvertError(
            "verify", "verify_manifest", f"{archive_name}: not a .zip or .tar.gz archive"
        )
    raise ConvertError("verify", "release_incomplete", f"{archive_name} has no {base}")


# --- downloads -----------------------------------------------------------------------------

Fetcher = Callable[[str], bytes | None]


def http_get(url: str, *, timeout: float = DOWNLOAD_TIMEOUT, limit: int = MAX_FILE) -> bytes | None:
    """A GET of ``url``: its body, None on 404."""
    req = urllib.request.Request(url, headers={"Accept": "application/octet-stream"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 - the home's or GitHub's URL
            body: bytes = resp.read(limit + 1)
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return None
        raise
    if len(body) > limit:
        raise ConvertError("download", "download_failed", f"{url}: larger than {limit >> 20} MB")
    return body


def release_source(
    home_url: str, job: Mapping[str, Any], get: Callable[[str], bytes | None] = http_get
) -> Fetcher:
    """A getter of the release's files by name: the home's artifact mirror first (story
    35.12), the job's GitHub Releases URLs otherwise. A file found nowhere is None."""
    version = str(job.get("version") or "")
    mirror = home_url.rstrip("/") + "/api/cluster/updates/artifacts/" + urllib_quote(version) + "/"
    assets = job.get("assets") if isinstance(job.get("assets"), Mapping) else {}
    assert isinstance(assets, Mapping)

    def fetch(name: str) -> bytes | None:
        errors: list[str] = []
        urls = [mirror + urllib_quote(name)]
        gh = assets.get(name)
        if isinstance(gh, str) and gh.startswith("https://"):
            urls.append(gh)
        for url in urls:
            try:
                body = get(url)
            except ConvertError:
                raise
            except Exception as exc:  # noqa: BLE001 - the next source, then download_failed
                errors.append(f"{url}: {exc}")
                continue
            if body is not None:
                return body
        if errors:
            raise ConvertError("download", "download_failed", "; ".join(errors)[:1000])
        return None

    return fetch


def urllib_quote(s: str) -> str:
    import urllib.parse

    return urllib.parse.quote(s, safe="")


# --- running setup outside this worker's service ----------------------------------------

#: the environment setup keeps (the rest of the worker's is the service's)
_KEEP_ENV = ("HOME", "PATH", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "SystemRoot")


def kept_env(env: Mapping[str, str]) -> dict[str, str]:
    """What setup needs of this worker's environment: every MAKTABA_* and XDG_* setting
    (the node config's place, a fork's repository), and the user's folders."""
    return {k: v for k, v in env.items() if k.startswith(("MAKTABA_", "XDG_")) or k in _KEEP_ENV}


def detached_command(
    argv: list[str],
    log: Path,
    exit_file: Path,
    *,
    system: str | None = None,
    env: Mapping[str, str] | None = None,
    systemd: bool | None = None,
    root: bool | None = None,
) -> list[str]:
    """The command that runs ``argv`` so that it outlives this worker's service (setup
    stops it at its end), writing its output to ``log`` and its exit code to
    ``exit_file``.

    - Linux under systemd: a scope of its own (``systemd-run --scope``): stopping the
      worker's unit kills its cgroup, not the scope's.
    - Windows: a process WMI creates (``Win32_Process.Create``), outside the job object the
      worker's scheduled task runs in; its environment set on its command line.
    - Otherwise (macOS's launchd kills the job's process group): a new session (the caller
      starts it with ``start_new_session``).
    """
    system = (system or platform.system()).lower()
    env = os.environ if env is None else env
    if system == "windows":
        # cmd's delayed expansion reads ! and %: a value with either (or a quote) is left out
        sets = "".join(
            f'set "{k}={v}" & '
            for k, v in sorted(kept_env(env).items())
            if not any(c in v for c in '"!%')
        )
        line = subprocess.list2cmdline(argv)
        inner = f'{sets}{line} > "{log}" 2>&1 & echo !errorlevel! > "{exit_file}"'
        cmdline = f'cmd.exe /d /s /v:on /c "{inner}"'
        script = (
            "$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create "
            "-Arguments @{CommandLine=$env:MAKTABA_CONVERT_CMD}; exit $r.ReturnValue"
        )
        return ["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, cmdline]
    out, done = shlex.quote(str(log)), shlex.quote(str(exit_file))
    sh = f"{shlex.join(argv)} > {out} 2>&1; echo $? > {done}"
    wrapped = ["/bin/sh", "-c", sh]
    if systemd is None:
        systemd = (
            system == "linux"
            and bool(env.get("INVOCATION_ID"))
            and shutil.which("systemd-run") is not None
        )
    if systemd:
        if root is None:
            root = hasattr(os, "geteuid") and os.geteuid() == 0
        scope = ["systemd-run", "--scope", "--quiet", "--collect"]
        if not root:
            scope.insert(1, "--user")
        return [*scope, "--", *wrapped]
    return wrapped


def spawn_detached(argv: list[str], log: Path, exit_file: Path) -> None:
    """Start ``argv`` outside this worker's service (detached_command); its exit code
    lands in ``exit_file``."""
    cmd = detached_command(argv, log, exit_file)
    if platform.system() == "Windows":
        # the command line goes through the environment: no quoting through PowerShell
        cmdline = cmd.pop()
        env = dict(os.environ, MAKTABA_CONVERT_CMD=cmdline)
        done = subprocess.run(
            cmd, env=env, capture_output=True, text=True, timeout=120, check=False
        )  # noqa: S603
        if done.returncode != 0:
            raise ConvertError(
                "install",
                "install_failed",
                f"Win32_Process.Create answered {done.returncode}: {done.stderr.strip()[:300]}",
            )
        return
    subprocess.Popen(  # noqa: S603 - the verified release's own setup
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
        close_fds=True,
    )


def tail(path: Path, limit: int = 1500) -> str:
    with contextlib.suppress(OSError):
        data = path.read_bytes()
        return data[-limit:].decode("utf-8", "replace").strip()
    return ""


# --- the conversion ------------------------------------------------------------------------


@dataclass
class Conversion:
    run_id: str
    version: str
    dir: Path
    state: str = "started"  # started | running | done | failed
    step: str = "download"
    code: str = ""
    detail: str = ""
    started: float = field(default_factory=time.time)

    def answer(self) -> dict[str, Any]:
        out: dict[str, Any] = {"state": self.state, "step": self.step, "version": self.version}
        if self.code:
            out["code"] = self.code
        if self.detail:
            out["detail"] = self.detail
        return out

    @classmethod
    def load(cls, folder: Path) -> Conversion | None:
        """The conversion a previous run of this worker left in ``folder``."""
        try:
            doc = json.loads((folder / "convert.json").read_text())
            return cls(
                run_id=str(doc["run_id"]),
                version=str(doc.get("version") or ""),
                dir=folder,
                state=str(doc.get("state") or ""),
                step=str(doc.get("step") or ""),
                code=str(doc.get("code") or ""),
                detail=str(doc.get("detail") or ""),
                started=float(doc.get("started") or 0),
            )
        except (OSError, ValueError, KeyError, TypeError):
            return None

    def save(self) -> None:
        with contextlib.suppress(OSError):
            self.dir.mkdir(parents=True, exist_ok=True)
            doc = {"run_id": self.run_id, "started": self.started, **self.answer()}
            (self.dir / "convert.json").write_text(json.dumps(doc))


def convert_root() -> Path:
    """Where conversions keep their files (``~/.maktaba/convert/<run>``)."""
    return share.local_dir().parent / "convert"


Spawner = Callable[[list[str], Path, Path], None]


class Converter:
    """``update.convert`` on this worker (one conversion at a time)."""

    def __init__(
        self,
        home_url: str,
        *,
        root: Path | None = None,
        source: Callable[[str, Mapping[str, Any]], Fetcher] | None = None,
        verify: Callable[[bytes, bytes, str, str], None] = verify_signed,
        spawn: Spawner = spawn_detached,
        platform_name: str | None = None,
        env: Mapping[str, str] | None = None,
        poll_sec: float = 2.0,
    ) -> None:
        self.home_url = home_url
        self.root = root or convert_root()
        self.source = source or (lambda home, job: release_source(home, job))
        self.verify = verify
        self.spawn = spawn
        self.platform = platform_name or go_platform()
        self.env = os.environ if env is None else env
        self.poll_sec = poll_sec
        self.current: Conversion | None = None
        self.task: asyncio.Task[None] | None = None

    async def command(self, args: Mapping[str, Any]) -> tuple[bool, dict[str, Any]]:
        """The command's answer: (ok, detail)."""
        run_id = str(args.get("run_id") or "")
        version = str(args.get("version") or "").removeprefix("v")
        if not run_id or not version:
            return False, {
                "state": "failed",
                "code": "convert_refused",
                "detail": "update.convert needs run_id and version",
            }
        cur = self.current
        if cur is not None and cur.run_id == run_id:
            return True, cur.answer()
        if cur is not None and cur.state in ("started", "running"):
            return False, {
                "state": "failed",
                "code": "convert_refused",
                "detail": f"the conversion of run {cur.run_id} is under way",
            }
        safe = re.sub(r"[^A-Za-z0-9-]", "", run_id)[:64] or "run"
        folder = self.root / safe
        prev = Conversion.load(folder)
        if prev is not None and prev.run_id == run_id and self._still_on(prev):
            # this worker restarted while the release's setup ran: follow it, never a second
            self.current = prev
            self.task = asyncio.create_task(self._follow(prev))
            return True, prev.answer()
        conv = Conversion(run_id=run_id, version=version, dir=folder)
        self.current = conv
        conv.save()
        self.task = asyncio.create_task(self._run(conv, dict(args)))
        _log.info("convert_started", run_id=run_id, version=version, dir=str(conv.dir))
        return True, conv.answer()

    async def _run(self, conv: Conversion, job: dict[str, Any]) -> None:
        try:
            conv.state = "running"
            exe = await asyncio.to_thread(self._prepare, conv, job)
            conv.step = "install"
            conv.save()
            log, exit_file = conv.dir / "setup.log", conv.dir / "setup.exit"
            with contextlib.suppress(FileNotFoundError):
                exit_file.unlink()
            argv = [str(exe), "setup", "--node", "--converting", "--from", str(exe.parent)]
            _log.info("convert_setup", run_id=conv.run_id, argv=argv)
            await asyncio.to_thread(self.spawn, argv, log, exit_file)
            code = await self._wait_exit(exit_file)
            if code != 0:
                raise ConvertError(
                    "install",
                    "install_failed",
                    f"maktaba-server setup --node exited {code}: {tail(log)}",
                )
            conv.state = "done"
            _log.info("convert_done", run_id=conv.run_id, version=conv.version)
        except ConvertError as exc:
            conv.state, conv.step, conv.code, conv.detail = (
                "failed",
                exc.step,
                exc.code,
                exc.detail[:2000],
            )
            _log.error(
                "convert_failed",
                run_id=conv.run_id,
                step=exc.step,
                code=exc.code,
                detail=exc.detail[:500],
            )
        except asyncio.CancelledError:
            raise
        except Exception as exc:  # noqa: BLE001 - reported to the home
            conv.state, conv.code, conv.detail = (
                "failed",
                "install_failed",
                f"{type(exc).__name__}: {exc}"[:2000],
            )
            _log.exception("convert_failed", run_id=conv.run_id)
        finally:
            conv.save()

    #: how long a setup started by an earlier run of this worker is followed
    FOLLOW_SEC = 2 * 3600

    def _still_on(self, prev: Conversion) -> bool:
        """Whether an earlier run of this worker left this conversion's setup running, or
        its outcome to read (the install step, not older than FOLLOW_SEC)."""
        return (
            prev.step == "install"
            and prev.state in ("started", "running", "done", "failed")
            and (prev.state != "running" or time.time() - prev.started < self.FOLLOW_SEC)
        )

    async def _follow(self, conv: Conversion) -> None:
        """Waits for the setup an earlier run of this worker started (its exit file)."""
        if conv.state != "running":
            return
        log, exit_file = conv.dir / "setup.log", conv.dir / "setup.exit"
        code = await self._wait_exit(exit_file)
        if code == 0:
            conv.state = "done"
        else:
            conv.state, conv.code = "failed", "install_failed"
            conv.detail = f"maktaba-server setup --node exited {code}: {tail(log)}"[:2000]
        conv.save()

    async def _wait_exit(self, exit_file: Path) -> int:
        while True:
            with contextlib.suppress(OSError, ValueError):
                text = exit_file.read_text().strip()
                if text:
                    return int(text)
            await asyncio.sleep(self.poll_sec)

    def _prepare(self, conv: Conversion, job: Mapping[str, Any]) -> Path:
        """Download and verify the release into the conversion's folder; the path of its
        maktaba-server, checked against release.json."""
        repo = release_repo(self.env)
        tag = str(job.get("tag") or "") or "v" + conv.version
        fetch = self.source(self.home_url, job)

        def need(name: str) -> bytes:
            data = fetch(name)
            if data is None:
                raise ConvertError(
                    "download",
                    "release_incomplete",
                    f"{name}: found neither on the home's mirror nor on GitHub",
                )
            return data

        conv.step = "download"
        conv.save()
        checksums, bundle, manifest_raw = need(CHECKSUMS), need(BUNDLE), need(MANIFEST)
        conv.step = "verify"
        conv.save()
        self.verify(checksums, bundle, tag, repo)
        sums = parse_checksums(checksums)
        check(sums, MANIFEST, manifest_raw)
        try:
            manifest = json.loads(manifest_raw)
        except ValueError as exc:
            raise ConvertError("verify", "verify_manifest", f"{MANIFEST}: {exc}") from exc
        if manifest.get("tag") != tag or str(manifest.get("version") or "") != conv.version:
            raise ConvertError(
                "verify", "version_mismatch", f"{MANIFEST} is {manifest.get('tag')}, not {tag}"
            )
        asset = (manifest.get("assets") or {}).get(self.platform)
        if not isinstance(asset, Mapping) or not asset.get("archive"):
            raise ConvertError(
                "download", "release_incomplete", f"the release has no build for {self.platform}"
            )
        names = [str(asset["archive"])]
        pipe = manifest.get("pipeline") if isinstance(manifest.get("pipeline"), Mapping) else None
        if pipe is not None:
            names += [
                str(pipe[k]) for k in ("wheel", "constraints", "constraints_cuda") if pipe.get(k)
            ]

        conv.step = "download"
        conv.save()
        files: dict[str, bytes] = {}
        for name in names:
            files[name] = need(name)
        conv.step = "verify"
        conv.save()
        for name, data in files.items():
            check(sums, name, data)
        archive = files[str(asset["archive"])]
        if asset.get("sha256") and sha256(archive) != asset["sha256"]:
            raise ConvertError(
                "verify", "verify_checksum", f"{asset['archive']}: not the archive {MANIFEST} names"
            )
        entry, server = extract_binary(str(asset["archive"]), archive, "maktaba-server")
        want = (asset.get("files") or {}).get(entry)
        if not want or sha256(server) != want:
            raise ConvertError(
                "verify", "verify_checksum", f"{entry}: not the binary {MANIFEST} lists"
            )

        # the verified files, as setup --node --from reads them; a fresh folder each time
        with contextlib.suppress(FileNotFoundError):
            shutil.rmtree(conv.dir / "release")
        dest = conv.dir / "release"
        dest.mkdir(parents=True, exist_ok=True)
        for name, data in {
            CHECKSUMS: checksums,
            BUNDLE: bundle,
            MANIFEST: manifest_raw,
            **files,
        }.items():
            (dest / name).write_bytes(data)
        exe = dest / entry
        exe.write_bytes(server)
        if sys.platform != "win32":
            exe.chmod(0o755)
        return exe


def make_handler(
    home_url: str, **kw: Any
) -> Callable[[Mapping[str, Any]], Awaitable[tuple[bool, dict[str, Any]]]]:
    """The agent's ``update.convert`` hook (Commands.convert)."""
    conv = Converter(home_url, **kw)
    return conv.command
