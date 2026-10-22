"""The node config: who this machine is and how it reaches its home (design section 7).

One JSON file per machine, written when the join is approved, holding a database
credential, so it is written atomically and readable by its owner only (mode 0600 on
POSIX; on Windows the per-user ``%LOCALAPPDATA%`` directory is already private).

| OS | path |
|---|---|
| macOS | ``~/Library/Application Support/Maktaba/node.json`` |
| Linux | ``${XDG_CONFIG_HOME:-~/.config}/maktaba/node.json`` |
| Windows | ``%LOCALAPPDATA%\\Maktaba\\node.json`` (Epic 38: a new join never roams) |

A Windows machine that joined before Epic 38 keeps ``%APPDATA%\\Maktaba\\node.json``:
one node config per machine, read and rewritten (a re-join) where it is, never
copied or moved (a copy would split it in two: a re-join or a hand edit on one side
unseen by the other). That file roams with a roaming profile, so it records the PC it
belongs to (``machine_id``, Windows' MachineGuid): the first new Maktaba that loads it
on a PC claims it (``load``), and a PC it does not belong to skips it and keeps its own
node config in ``%LOCALAPPDATA%``, so a join there never overwrites the other PC's
identity. ``save`` is the one writer of a node config (maktaba-server, streaming and the
node installer only read it).
``MAKTABA_NODE_CONFIG`` names another file (two nodes on one host, tests). Go resolves
the same path (``config.NodeConfigPath`` in maktaba-server, ``channel.NodeConfigPath``
in streaming).

A write that could not finish (ReplaceFileW failing half way, and the putting back
failing too) can leave the old file under a backup name and the new one as a temp file
beside the node config (``.node.json-<time>-<pid>-<hex>.bak`` / ``.tmp``). While
node.json is missing or broken, every reader takes the newest whole backup, else the
newest whole temp file, as the node config (``_source``; Go's ``nodeConfigFile``, the
installer's ``Get-NodeConfigSource``): a roaming config left that way is still this
PC's, and the node stays joined. The next load puts it back (``_recover``), never
over a whole file, and keeps a broken node.json beside it
(``node.json.broken-<time>-<hex>``). On Windows writes and recoveries of one node
config run one at a time (``_one_at_a_time``): two ReplaceFileW calls at once can undo
each other.
"""

from __future__ import annotations

import codecs
import contextlib
import errno
import json
import os
import platform
import re
import sys
import time
from collections.abc import Callable, Iterator, Mapping
from dataclasses import asdict, dataclass, field, replace
from pathlib import Path
from typing import Any

__all__ = [
    "ENV_VAR",
    "HomeRef",
    "NodeConfig",
    "config_path",
    "load",
    "machine_id",
    "save",
    "set_aside",
]

ENV_VAR = "MAKTABA_NODE_CONFIG"

#: this PC's id in place of Windows' MachineGuid (tests; a PC whose registry cannot be read)
MACHINE_ID_ENV = "MAKTABA_MACHINE_ID"


def machine_id(env: Mapping[str, str] | None = None) -> str:
    """This PC's id: ``MAKTABA_MACHINE_ID``, else Windows' MachineGuid; "" when it cannot
    be known (off Windows)."""
    env = os.environ if env is None else env
    override = str(env.get(MACHINE_ID_ENV) or "").strip()
    if override:
        return override
    if sys.platform != "win32":
        return ""
    import winreg

    try:
        with winreg.OpenKey(
            winreg.HKEY_LOCAL_MACHINE,
            r"SOFTWARE\Microsoft\Cryptography",
            0,
            winreg.KEY_READ | winreg.KEY_WOW64_64KEY,
        ) as key:
            value, _ = winreg.QueryValueEx(key, "MachineGuid")
    except OSError:
        return ""
    return str(value).strip()


@dataclass(frozen=True, slots=True)
class HomeRef:
    url: str
    instance_id: str = ""
    name: str = ""
    #: the home's long-term cluster public key (X25519, standard base64), sealed in the
    #: join credentials since the agent channel's handshake v2; empty in older configs
    cluster_pub: str = ""


@dataclass(frozen=True, slots=True)
class NodeConfig:
    node_id: str
    name: str
    private_key: str  #: X25519 private key, standard base64
    home: HomeRef = field(default_factory=lambda: HomeRef(url=""))
    database_url: str = ""
    joined_at: str = ""
    #: the home's database CA (PEM), sealed in the join's credentials (or handed over the
    #: machine channel later); "off" in database_tls when the home's database has no TLS
    database_ca: str = ""
    database_tls: str = ""
    #: the PC this config belongs to (Windows' MachineGuid); "" until a PC claims it
    machine_id: str = ""

    @property
    def node8(self) -> str:
        """First 8 hex of the node id (its database role, its worker ids)."""
        return self.node_id.replace("-", "")[:8].lower()

    @property
    def joined(self) -> bool:
        return bool(self.database_url)

    def to_json(self) -> dict[str, Any]:
        data = asdict(self)
        if not data["machine_id"]:
            del data["machine_id"]  # no PC's yet: the first new Maktaba to load it claims it
        return data

    @classmethod
    def from_json(cls, data: dict[str, Any]) -> NodeConfig:
        """Raises ValueError for a config without its identity (hand-edited, half-written)."""
        missing = [k for k in ("node_id", "private_key") if not str(data.get(k) or "").strip()]
        if missing:
            raise ValueError(
                f"incomplete node config: no {' or '.join(missing)}; move it aside and re-join"
                " this machine (maktaba-pipeline join)"
            )
        home = data.get("home") or {}
        return cls(
            node_id=str(data["node_id"]),
            name=str(data.get("name") or ""),
            private_key=str(data["private_key"]),
            home=HomeRef(
                url=str(home.get("url") or home.get("api_url") or ""),
                instance_id=str(home.get("instance_id") or ""),
                name=str(home.get("name") or ""),
                cluster_pub=str(home.get("cluster_pub") or ""),
            ),
            database_url=str(data.get("database_url") or ""),
            joined_at=str(data.get("joined_at") or ""),
            database_ca=str(data.get("database_ca") or ""),
            database_tls=str(data.get("database_tls") or ""),
            machine_id=str(data.get("machine_id") or "").strip(),
        )


def config_path(
    *, system: str | None = None, env: dict[str, str] | None = None, home: Path | None = None
) -> Path:
    """Where this machine keeps its node config (arguments for tests)."""
    env = dict(os.environ) if env is None else env
    override = env.get(ENV_VAR)
    if override:
        return Path(override).expanduser()
    system = system or platform.system()
    home = home if home is not None else Path.home()
    if system == "Darwin":
        return home / "Library" / "Application Support" / "Maktaba" / "node.json"
    if system == "Windows":
        # one node config per machine, where it is: the roaming profile's of a machine
        # that joined before Epic 38 while it is this PC's or no PC's yet, else the new
        # place (where a new join writes; another PC's roaming config is not this PC's).
        # What a write that could not finish left of it counts as it (_source).
        old = _roaming_config(env, home)
        held = _held(old)
        if held is not None and not _claimed_elsewhere(held, env):
            return old
        local = env.get("LOCALAPPDATA")
        return (Path(local) if local else home / "AppData" / "Local") / "Maktaba" / "node.json"
    xdg = env.get("XDG_CONFIG_HOME")
    base = Path(xdg) if xdg else home / ".config"
    return base / "maktaba" / "node.json"


def _roaming_config(env: Mapping[str, str], home: Path) -> Path:
    """``%APPDATA%\\Maktaba\\node.json``: a Windows node config from before Epic 38."""
    appdata = env.get("APPDATA")
    return (Path(appdata) if appdata else home / "AppData" / "Roaming") / "Maktaba" / "node.json"


#: the byte order marks a node config is read past, in the encoding each names (UTF-32's
#: before UTF-16's: they begin alike)
_BOMS = (
    (codecs.BOM_UTF32_LE, "utf-32"),
    (codecs.BOM_UTF32_BE, "utf-32"),
    (codecs.BOM_UTF8, "utf-8-sig"),
    (codecs.BOM_UTF16_LE, "utf-16"),
    (codecs.BOM_UTF16_BE, "utf-16"),
)


def _text(path: Path) -> str:
    """A node config's text, read the way every reader reads it (maktaba-server's and
    streaming's ``withoutBOM``, the installer's ``Get-Content``): a node.json an editor
    saved with a byte order mark in that encoding (Notepad and PowerShell 5.1's
    ``Set-Content -Encoding UTF8`` write UTF-8 with one, its ``Out-File`` and ``>``
    UTF-16), one without as UTF-8. Raises OSError, and ValueError for text that does not
    decode (a broken file)."""
    raw = path.read_bytes()
    for bom, codec in _BOMS:
        if raw.startswith(bom):
            return raw.decode(codec)
    return raw.decode("utf-8")


def _owner(path: Path) -> str:
    """The machine_id a node config records: "" when it has none or cannot be read (a
    broken file is never skipped: ``load`` says what is wrong with it)."""
    try:
        data = json.loads(_text(path))
    except (OSError, ValueError):
        return ""
    return str(data.get("machine_id") or "").strip() if isinstance(data, dict) else ""


def _claimed_elsewhere(path: Path, env: Mapping[str, str]) -> bool:
    """The node config at ``path`` records another PC than this one. One no PC has
    claimed, or a PC whose id cannot be known, is never skipped: a working node is never
    taken down on a guess."""
    owner = _owner(path)
    if not owner:
        return False
    this = machine_id(env)
    return bool(this) and owner.lower() != this.lower()


def _is_roaming_config(path: Path) -> bool:
    if platform.system() != "Windows":
        return False
    try:
        return path.resolve() == _roaming_config(os.environ, Path.home()).resolve()
    except OSError:
        return False


def load(path: Path | None = None) -> NodeConfig | None:
    """The node config, or None when this machine never asked to join. A roaming
    profile's node config from before Epic 38 that no PC has claimed yet is claimed for
    this PC: its ``machine_id`` is written in, everything else kept (``_write``). What a
    write that could not finish left beside it is recovered first (``_recover``); what
    could not be put back yet is read where it is (``_source``)."""
    path = path or config_path()
    _recover(path)
    _prune_broken(path)
    _secure_once(path)
    src = path
    try:
        src, text = _read_steady(path)
        data = json.loads(text)
    except FileNotFoundError:
        return None
    except ValueError as exc:
        # a typo or a truncated file: never "not joined", and the error names the file
        raise ValueError(f"{src}: not a node config ({exc})") from None
    if not isinstance(data, dict):
        raise ValueError(f"{src}: not a node config")
    try:
        cfg = NodeConfig.from_json(data)
    except ValueError as exc:
        raise ValueError(f"{src}: {exc}") from None
    # a claim writes node.json itself: one read from a leftover waits until it is back
    if src == path and not cfg.machine_id and _is_roaming_config(path):
        this = machine_id()
        if this:
            claimed: dict[str, str] = {}

            def claim(now: dict[str, Any]) -> dict[str, Any] | None:
                # the file as it is with the lock held (a write may have changed it since
                # it was read here): only a config no PC has claimed is claimed
                claimed["by"] = str(now.get("machine_id") or "").strip() or this
                return None if now.get("machine_id") else {**now, "machine_id": this}

            try:
                _update(path, claim)
            except (OSError, ValueError) as exc:
                # the file stays as it was (_write never loses it) and the node runs on;
                # the next start tries again (and recovers what it left, _recover)
                if str(path) not in _claim_failed:
                    _claim_failed.add(str(path))
                    from ..log import get_logger

                    get_logger().warning(
                        "node_config_claim_failed",
                        path=str(path),
                        error=str(exc),
                        left=[p.name for p in _leftovers(path)],
                    )
            else:
                cfg = replace(cfg, machine_id=claimed.get("by", this))
    return cfg


#: the node configs whose claim this process could not write (said once each)
_claim_failed: set[str] = set()

#: a write's leftovers are that write's own for this long from the time in their names:
#: another load reads past them but never moves or removes them (a write takes
#: milliseconds; one that could not finish is put back by a load after that)
_LEFTOVER_GRACE_SEC = 60.0

#: node.json's leftovers as they were named before they carried the file's name:
#: ``.node-<pid>-<8 hex>.bak`` and mkstemp's ``.node-<8 characters>.tmp``
_LEGACY_LEFTOVER = re.compile(r"\.node-(\d+-[0-9a-f]{8}\.bak|[a-z0-9_]{8}\.tmp)")

#: the leftovers this process has already said it could not recover (said once each)
_leftovers_said: set[tuple[str, ...]] = set()

#: the clock leftovers are aged by (tests move it)
_now = time.time


def _leftover_name(path: Path, ext: str) -> Path:
    """A name beside ``path`` for one write's backup of the old file (``.bak``) or temp
    file with the new one (``.tmp``): the config's name, the time the write started, the
    process and 8 random hex. The time says how old it is: ReplaceFileW's backup keeps
    the old file's mtime."""
    stamp = f"{int(_now())}-{os.getpid()}-{os.urandom(4).hex()}"
    return path.with_name(f".{path.name}-{stamp}{ext}")


def _leftovers(path: Path) -> list[Path]:
    """What a write of the node config at ``path`` that could not finish left beside it:
    ReplaceFileW's backup of the old file (``.<name>-*.bak``) and ``_write``'s temp file
    with the new one (``.<name>-*.tmp``; ``.node-*`` for node.json before they were
    named after the file)."""
    found = {p for ext in (".bak", ".tmp") for p in path.parent.glob(f".{path.name}-*{ext}")}
    if path.name == "node.json":  # the names before, exactly as they were made
        found |= {p for p in path.parent.glob(".node-*") if _LEGACY_LEFTOVER.fullmatch(p.name)}
    return sorted(found, key=lambda p: (p.suffix, p.name))


def _in_progress(path: Path, leftover: Path, now: float) -> bool:
    """``leftover`` may still be its write's: under a minute from the time in its name
    (``_leftover_name``); one named before that by its mtime."""
    pattern = rf"\.{re.escape(path.name)}-(\d+)-\d+-[0-9a-f]{{8}}\.(bak|tmp)"
    named = re.fullmatch(pattern, leftover.name)
    if named:
        started = float(named.group(1))
    else:
        try:
            started = leftover.stat().st_mtime
        except OSError:
            return False
    return abs(now - started) < _LEFTOVER_GRACE_SEC


def _whole_state(path: Path) -> bool | None:
    """True when ``path`` holds a node config with its identity (a whole file, not a cut
    one); False when it is gone, or not one (not JSON, cut, without its identity); None
    when it cannot be read now (another process holds it: a rename, ReplaceFileW). Read
    as every reader reads it (``_text``: past a byte order mark)."""
    try:
        data = json.loads(_text(path))
    except FileNotFoundError:
        return False
    except OSError:
        return None
    except ValueError:
        return False
    if not isinstance(data, dict):
        return False
    try:
        NodeConfig.from_json(data)
    except ValueError:
        return False
    return True


def _whole(path: Path) -> bool:
    """``path`` holds a node config with its identity (``_whole_state``)."""
    return _whole_state(path) is True


def _gone_or_broken(path: Path) -> bool:
    """There is no node config at ``path``, or what is there is not one. One that cannot
    be read is neither: it is never moved or read past, and its reader says why."""
    return _whole_state(path) is False


def _mtime(path: Path) -> float:
    try:
        return path.stat().st_mtime
    except OSError:
        return 0.0


def _candidate(left: list[Path], *, sure: bool = False) -> Path | None:
    """The leftover that stands for node.json, as ``_settle`` decides: the old file (the
    newest whole backup), else the new one (the newest whole temp file). ``sure``: None
    while one of them cannot be read (another process holds it): it may be the newest,
    and putting an older one back would undo a write (``_recover``; a reader takes the
    newest it can read)."""
    for ext in (".bak", ".tmp"):
        whole: list[Path] = []
        for p in left:
            if p.suffix != ext:
                continue
            state = _whole_state(p)
            if state is None and sure:
                return None
            if state:
                whole.append(p)
        if whole:
            return max(whole, key=_mtime)
    return None


def _source(path: Path) -> Path:
    """The file that holds the node config at ``path``: ``path``, unless it is missing or
    broken (``_gone_or_broken``) and a write that could not finish left a whole one: then
    the leftover ``_recover`` puts in its place (one it cannot move yet: a write in
    progress, or a rename that failed). maktaba-server, streaming and the node installer
    read the same file (``nodeConfigFile``, ``Get-NodeConfigSource``)."""
    if not _gone_or_broken(path):
        return path
    return _candidate(_leftovers(path)) or path


def _lock_path(path: Path) -> Path:
    """The write lock beside the node config at ``path`` (``_one_at_a_time``)."""
    return path.with_name(f".{path.name}.lock")


def _write_under_way(path: Path, exc: OSError) -> bool:
    """Reading the node config at ``path`` failed with ``exc`` while a write of it may be
    under way: the file is held by another process (a sharing violation: ReplaceFileW, a
    rename), or it is missing, or denied for a moment (a backup being removed), while its
    lock is held (``_locked_lately``) or a write's leftovers of the last minute are beside
    it (ReplaceFileW renames node.json aside before the new one takes its name; a stale
    leftover is no write under way). A reader waits that out rather than take
    "missing" for "not joined", as maktaba-server's and streaming's readers do."""
    if getattr(exc, "winerror", None) == 32 or _held_open(exc):
        return True
    if isinstance(exc, (FileNotFoundError, PermissionError)):
        now = _now()
        return _locked_lately(path, now) or any(
            _in_progress(path, p, now) for p in _leftovers(path)
        )
    return False


def _held_open(exc: OSError) -> bool:
    """Windows: ``open()`` failed with EACCES and no Windows error code, which is how the C
    runtime reports a sharing violation (another program, an antivirus or a backup tool,
    holds the file open without sharing it). A file this user may not read says the same:
    the bounded wait (``_read_steady``) then ends in the PermissionError it would raise."""
    return (
        sys.platform == "win32"
        and isinstance(exc, PermissionError)
        and exc.errno == errno.EACCES
        and getattr(exc, "winerror", None) is None
    )


def _locked_lately(path: Path, now: float) -> bool:
    """The node config at ``path`` has a lock file made in the last minute: a write
    removes it when it is done, and one a process that stopped mid-write left long ago is
    no write under way."""
    try:
        return abs(now - _lock_path(path).stat().st_mtime) < _LEFTOVER_GRACE_SEC
    except OSError:
        return False


def _read_steady(path: Path) -> tuple[Path, str]:
    """The node config's text from the file that holds it (``_source``), waiting out a
    write under way (``_write_under_way``) for up to ``_READ_WAIT_SEC``; the file and its
    text. Raises the last OSError (FileNotFoundError when there is none), ValueError for
    text that does not decode. A write still under way at the end is an OSError that is
    not FileNotFoundError (EBUSY): never "not joined"."""
    deadline = time.monotonic() + _READ_WAIT_SEC
    while True:
        src = _source(path)
        try:
            return src, _text(src)
        except OSError as exc:
            # a leftover it picked that is gone already: the write ended, look again
            vanished = src != path and isinstance(exc, (FileNotFoundError, PermissionError))
            if not vanished and not _write_under_way(path, exc):
                raise
            if time.monotonic() >= deadline:
                if isinstance(exc, FileNotFoundError) or getattr(exc, "winerror", None) == 32:
                    raise OSError(
                        errno.EBUSY,
                        f"being written (a write of it still under way after {_READ_WAIT_SEC} s):"
                        f" {exc}",
                        str(src),
                    ) from exc
                raise
        time.sleep(_BUSY_SLEEP)


def _held(path: Path) -> Path | None:
    """The file that holds the node config at ``path`` (``_source``) when there is one,
    waiting out a write under way (a leftover it picked can be gone a moment later, and
    a resolver must not take the local place for it then) for up to ``_READ_WAIT_SEC``;
    None when there is none. A write still under way at the end keeps ``path`` (its
    reader then says it is being written)."""
    deadline = time.monotonic() + _READ_WAIT_SEC
    while True:
        held = _source(path)
        if held.is_file():
            return held
        # a leftover it picked that is gone already (the write ended): look again
        under_way = _write_under_way(path, FileNotFoundError(str(held)))
        if held == path and not under_way:
            return None
        if time.monotonic() >= deadline:
            return path if under_way else None
        time.sleep(_BUSY_SLEEP)


#: the node configs this process could not make their user's alone (said once each)
_secure_failed: set[str] = set()


def _secure_once(path: Path) -> None:
    """At load: node.json that is not its user's alone is made so (``_secure``; one that
    maktaba-server's writer of #178 left with its folder's ACL), and the warning says so;
    one that cannot be is said once. It runs with the write lock, so a write replacing
    the file meanwhile is no failure (a write running now leaves it the user's alone);
    and before it says one, it looks again."""
    if not path.is_file():
        return
    from ..log import get_logger

    try:
        made = _secure_held(path)
    except OSError as exc:
        if _user_only_now(path):
            return  # replaced or gone meanwhile: nothing to say
        if str(path) not in _secure_failed:
            _secure_failed.add(str(path))
            get_logger().warning("node_config_not_private", path=str(path), error=str(exc))
        return
    if made:
        get_logger().warning("node_config_made_private", path=str(path))


def _secure_held(path: Path) -> bool:
    """``_secure`` with the write lock held (``_one_at_a_time``, the recovery's wait):
    False when another write holds it (that write leaves the file the user's alone), and
    without the lock when there is none to be had (a folder this user may not write)."""
    with contextlib.ExitStack() as stack:
        try:
            held = stack.enter_context(_one_at_a_time(path, wait=_RECOVER_WAIT_SEC))
        except OSError:
            held = True
        return held and path.is_file() and _secure(path)


def _user_only_now(path: Path) -> bool:
    """The node config at ``path`` is gone, or its user's alone now (looked at again)."""
    try:
        if not path.exists():
            return True
        if sys.platform == "win32":
            return _is_user_only(path)
        return not path.stat().st_mode & 0o077
    except OSError:
        return False


def _rename_no_replace(src: Path, dst: Path) -> None:
    """``src`` takes the name ``dst``, never replacing a file there (FileExistsError):
    MoveFileExW without MOVEFILE_REPLACE_EXISTING on Windows (``os.rename``), a hard link
    and then the unlink on POSIX."""
    if sys.platform == "win32":
        os.rename(src, dst)
        return
    os.link(src, dst)
    os.unlink(src)


def _broken_name(path: Path) -> Path:
    """Where a broken node config is kept when a leftover takes its name: beside it,
    ``<name>.broken-<time>-<4 hex>`` (no leftover's name: it is never put back; a person
    may want what is in it, for ``_BROKEN_KEEP_DAYS``)."""
    return path.with_name(f"{path.name}.broken-{int(_now())}-{os.urandom(2).hex()}")


#: how long a broken node.json kept aside stays (housekeeping, not a setting: long enough
#: to look into what broke it; it may hold part of a credential, so not forever). A
#: re-join removes them at once.
_BROKEN_KEEP_DAYS = 30


def _prune_broken(path: Path, *, all_of_them: bool = False) -> None:
    """Remove the broken node configs kept beside ``path`` (``_broken_name``) that are
    older than ``_BROKEN_KEEP_DAYS`` by the time in their names, or all of them."""
    named = re.compile(rf"{re.escape(path.name)}\.broken-(\d+)-[0-9a-f]{{4}}")
    now = _now()
    for p in path.parent.glob(f"{path.name}.broken-*"):
        m = named.fullmatch(p.name)
        if m and (all_of_them or now - float(m.group(1)) > _BROKEN_KEEP_DAYS * 86400):
            with contextlib.suppress(OSError):
                p.unlink()


def _put_in_place(path: Path, leftover: Path) -> tuple[bool, Path | None]:
    """``leftover`` takes the name ``path``, never over a whole file there (one another
    load put back a moment ago, or a write's). A broken ``path``, looked at again just
    before, is kept aside (``_broken_name``; moved back when what was moved is not broken
    after all: a write landed in between), then ``leftover`` is renamed without
    replacing. Returns whether it took the name, and the broken file kept aside."""
    kept: Path | None = None
    if path.exists():
        if not _gone_or_broken(path):
            return False, None  # whole again (another load, a write), or unreadable
        kept = _broken_name(path)
        try:
            _when_free(_rename_no_replace, path, kept)
        except FileNotFoundError:
            kept = None  # gone meanwhile: nothing to keep
        else:
            if not _gone_or_broken(kept):
                with contextlib.suppress(OSError):
                    _when_free(_rename_no_replace, kept, path)
                return False, None
            # it may hold part of a credential, and it stays for a while: the user's
            # alone, whatever ACL it had (best effort: it is never read as a node config)
            with contextlib.suppress(OSError):
                _secure(kept)
    try:
        _when_free(_rename_no_replace, leftover, path)
    except FileExistsError:
        return False, kept
    return True, kept


#: a file another process has open (a reader: Python, Go, PowerShell) cannot be renamed or
#: read for a moment (ERROR_SHARING_VIOLATION): a recovery tries again this often
_BUSY_TRIES, _BUSY_SLEEP = 20, 0.005

#: how long a reader waits out a write under way (``_write_under_way``): a write holds
#: the lock for all of it, which takes milliseconds, and more on a busy machine
_READ_WAIT_SEC = 2.0


def _when_free(move: Any, src: Path, dst: Path) -> None:
    """``move(src, dst)``, tried again while one of them is open in another process."""
    for left in range(_BUSY_TRIES - 1, -1, -1):
        try:
            move(src, dst)
            return
        except PermissionError as exc:
            if getattr(exc, "winerror", None) != 32 or not left:
                raise
        time.sleep(_BUSY_SLEEP)


def _readable_candidate(left: list[Path]) -> Path | None:
    """``_candidate`` with every leftover read (``sure``), tried again for a moment while
    one is open in another process; None when none stands for node.json, or one stays
    unreadable."""
    for _ in range(_BUSY_TRIES):
        if all(_whole_state(p) is not None for p in left):
            return _candidate(left, sure=True)
        time.sleep(_BUSY_SLEEP)
    return None


def _recover(path: Path) -> None:
    """At load: a write whose ReplaceFileW failed and whose settling (``_settle``) failed
    too can leave node.json's old file under its backup name, the new one as a temp file,
    or both. When node.json is missing or broken, the right one takes its place as
    ``_settle`` decides (``_candidate``), without replacing anything (a broken node.json
    is kept aside, ``_put_in_place``); once node.json is whole, the rest go. A warning
    names them. A leftover a write may still be making (``_in_progress``) is left as it
    is and not named. It runs with the write lock (``_one_at_a_time``) or not at all: a
    write that runs settles its own files, and a later load looks again."""
    left = _leftovers(path)
    if not left:
        return
    try:
        with _one_at_a_time(path, wait=_RECOVER_WAIT_SEC) as held:
            if held:
                _recover_held(path)
    except OSError as exc:
        # no lock to be had (a folder this user may not write, a read-only or another
        # user's lock file, a full disk): nothing is moved, the load reads past the
        # leftovers (_source) and the warning says why
        _tell_leftovers(path, kept=[p.name for p in left], error=str(exc))


def _recover_held(path: Path) -> None:
    """``_recover`` with the lock held."""
    now = _now()
    # node.json first, the leftovers after: a backup that took its name a moment ago (a
    # write's ReplaceFileW failing half way) is then among them, and an older one is
    # never put back in its place
    gone = _gone_or_broken(path)
    left = [p for p in _leftovers(path) if not _in_progress(path, p, now)]
    if not left:
        return
    used: Path | None = None
    broken: Path | None = None
    error = ""
    if gone:
        candidate = _readable_candidate(left)
        if candidate is not None:
            try:
                if candidate.suffix == ".tmp":
                    _restrict_to_user(candidate)
                placed, broken = _put_in_place(path, candidate)
                if placed:
                    used = candidate
            except OSError as exc:
                error = str(exc)
    removed: list[str] = []
    if _whole(path):
        for p in left:
            if p != used:
                with contextlib.suppress(OSError):
                    _unlink_when_free(p)
                    removed.append(p.name)
    _tell_leftovers(
        path,
        restored=used.name if used is not None else "",
        broken_kept=broken.name if broken is not None else "",
        removed=removed,
        kept=[p.name for p in left if p != used and p.name not in removed],
        error=error,
    )


def _tell_leftovers(
    path: Path,
    *,
    kept: list[str],
    error: str,
    restored: str = "",
    broken_kept: str = "",
    removed: list[str] | None = None,
) -> None:
    """The warning ``_recover`` gives: what it put back, kept aside, removed and left;
    once per process for the same leftovers while they stay."""
    key = (str(path), *kept)
    if kept and key in _leftovers_said:
        return
    _leftovers_said.add(key)
    from ..log import get_logger

    get_logger().warning(
        "node_config_leftovers",
        path=str(path),
        restored=restored,
        broken_kept=broken_kept,
        removed=removed or [],
        left=kept,
        error=error,
    )


def _restrict_to_user(path: Path) -> None:
    """Windows: a leftover temp file takes node.json's name as the user's alone (one an
    older write made may not be): chmod means nothing there, so its DACL is set to the
    user this process runs as and nothing else, inheritance off (as maktaba-server's
    restrictToUser sets the keys'). The whole DACL is replaced: removing inherited
    entries is not enough, since Python can create a file with explicit ones (SYSTEM,
    Administrators). Best effort: a failure is logged (the secret is in that file
    already). A write's own temp file is the user's from its creation (``_new_temp``)."""
    if sys.platform != "win32":
        return
    try:
        _set_user_only_dacl(str(path))
    except OSError as exc:
        from ..log import get_logger

        get_logger().warning("node_config_acl_failed", path=str(path), error=str(exc))


def _win32() -> tuple[Any, Any]:
    """advapi32 and kernel32, with the signatures of the calls below."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    from ctypes import wintypes

    advapi32 = ctypes.WinDLL("advapi32", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel32.GetCurrentProcess.restype = wintypes.HANDLE
    kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel32.LocalFree.argtypes = [ctypes.c_void_p]
    kernel32.LocalFree.restype = ctypes.c_void_p
    kernel32.CreateFileW.argtypes = [
        wintypes.LPCWSTR,
        wintypes.DWORD,
        wintypes.DWORD,
        ctypes.c_void_p,
        wintypes.DWORD,
        wintypes.DWORD,
        wintypes.HANDLE,
    ]
    kernel32.CreateFileW.restype = wintypes.HANDLE
    advapi32.OpenProcessToken.argtypes = [
        wintypes.HANDLE,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.HANDLE),
    ]
    advapi32.GetTokenInformation.argtypes = [
        wintypes.HANDLE,
        ctypes.c_int,
        ctypes.c_void_p,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.DWORD),
    ]
    advapi32.ConvertSidToStringSidW.argtypes = [ctypes.c_void_p, ctypes.POINTER(wintypes.LPWSTR)]
    advapi32.ConvertStringSecurityDescriptorToSecurityDescriptorW.argtypes = [
        wintypes.LPCWSTR,
        wintypes.DWORD,
        ctypes.POINTER(ctypes.c_void_p),
        ctypes.c_void_p,
    ]
    advapi32.GetSecurityDescriptorDacl.argtypes = [
        ctypes.c_void_p,
        ctypes.POINTER(wintypes.BOOL),
        ctypes.POINTER(ctypes.c_void_p),
        ctypes.POINTER(wintypes.BOOL),
    ]
    advapi32.SetNamedSecurityInfoW.argtypes = [
        wintypes.LPWSTR,
        ctypes.c_int,
        wintypes.DWORD,
        ctypes.c_void_p,
        ctypes.c_void_p,
        ctypes.c_void_p,
        ctypes.c_void_p,
    ]
    advapi32.SetNamedSecurityInfoW.restype = wintypes.DWORD
    return advapi32, kernel32


def _user_only_sddl() -> str:
    """The security descriptor of a file only this process's user may open (SDDL): a
    protected DACL (nothing inherited) granting the user, by its SID, full control."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    from ctypes import wintypes

    advapi32, kernel32 = _win32()
    token_query, token_user = 0x0008, 1
    token = wintypes.HANDLE()
    if not advapi32.OpenProcessToken(
        kernel32.GetCurrentProcess(), token_query, ctypes.byref(token)
    ):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        size = wintypes.DWORD()
        advapi32.GetTokenInformation(token, token_user, None, 0, ctypes.byref(size))
        info = ctypes.create_string_buffer(size.value)
        if not advapi32.GetTokenInformation(token, token_user, info, size, ctypes.byref(size)):
            raise ctypes.WinError(ctypes.get_last_error())
        user_sid = ctypes.cast(info, ctypes.POINTER(ctypes.c_void_p))[0]  # TOKEN_USER.User.Sid
        sid_text = wintypes.LPWSTR()
        if not advapi32.ConvertSidToStringSidW(user_sid, ctypes.byref(sid_text)):
            raise ctypes.WinError(ctypes.get_last_error())
        sid = sid_text.value
        kernel32.LocalFree(sid_text)
    finally:
        kernel32.CloseHandle(token)
    return f"D:P(A;;FA;;;{sid})"


@contextlib.contextmanager
def _user_only_descriptor() -> Iterator[Any]:
    """``_user_only_sddl`` as a security descriptor, freed after the block."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes

    advapi32, kernel32 = _win32()
    descriptor = ctypes.c_void_p()
    if not advapi32.ConvertStringSecurityDescriptorToSecurityDescriptorW(
        _user_only_sddl(), 1, ctypes.byref(descriptor), None
    ):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        yield descriptor
    finally:
        kernel32.LocalFree(descriptor)


def _set_user_only_dacl(path: str) -> None:
    """SetNamedSecurityInfoW with the user-only DACL (``_user_only_sddl``)."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    from ctypes import wintypes

    advapi32, _ = _win32()
    with _user_only_descriptor() as descriptor:
        present, defaulted, dacl = wintypes.BOOL(), wintypes.BOOL(), ctypes.c_void_p()
        if not advapi32.GetSecurityDescriptorDacl(
            descriptor, ctypes.byref(present), ctypes.byref(dacl), ctypes.byref(defaulted)
        ):
            raise ctypes.WinError(ctypes.get_last_error())
        se_file_object, dacl_info, protected_dacl_info = 1, 0x00000004, 0x80000000
        err = advapi32.SetNamedSecurityInfoW(
            path, se_file_object, dacl_info | protected_dacl_info, None, None, dacl, None
        )
        if err:
            raise ctypes.WinError(err)


def _is_user_only(path: Path) -> bool:
    """Windows: the file's DACL is protected and grants this process's user full control
    and no one anything (what ``_create_user_only`` and ``_set_user_only_dacl`` give it).
    Raises OSError when it cannot be read."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    from ctypes import wintypes

    advapi32, kernel32 = _win32()
    advapi32.GetNamedSecurityInfoW.argtypes = [
        wintypes.LPCWSTR,
        ctypes.c_int,
        wintypes.DWORD,
        ctypes.c_void_p,
        ctypes.c_void_p,
        ctypes.POINTER(ctypes.c_void_p),
        ctypes.c_void_p,
        ctypes.POINTER(ctypes.c_void_p),
    ]
    advapi32.GetNamedSecurityInfoW.restype = wintypes.DWORD
    advapi32.ConvertSecurityDescriptorToStringSecurityDescriptorW.argtypes = [
        ctypes.c_void_p,
        wintypes.DWORD,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.LPWSTR),
        ctypes.c_void_p,
    ]
    se_file_object, dacl_info = 1, 0x00000004
    dacl, descriptor = ctypes.c_void_p(), ctypes.c_void_p()
    err = advapi32.GetNamedSecurityInfoW(
        str(path),
        se_file_object,
        dacl_info,
        None,
        None,
        ctypes.byref(dacl),
        None,
        ctypes.byref(descriptor),
    )
    if err:
        raise ctypes.WinError(err)
    try:
        sddl = _dacl_sddl(descriptor)
    finally:
        kernel32.LocalFree(descriptor)
    # the user as SDDL writes it: an alias for a well-known account ("LA" for the built-in
    # Administrator, a CI runner's user), not always its S-1-5-... string
    with _user_only_descriptor() as wanted:
        want = _USER_ONLY_DACL.fullmatch(_dacl_sddl(wanted))
    m = _USER_ONLY_DACL.fullmatch(sddl)
    return (
        m is not None
        and want is not None
        and "P" in m.group(1)
        and m.group(3).upper() == want.group(3).upper()
    )


#: a protected DACL (as SDDL) that grants one SID full control and no one anything else
_USER_ONLY_DACL = re.compile(r"D:([A-Z]*)\(A;;(FA|GA|0x1f01ff);;;([^)]+)\)")


def _dacl_sddl(descriptor: Any) -> str:
    """The DACL of a security descriptor as SDDL."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    from ctypes import wintypes

    advapi32, kernel32 = _win32()
    advapi32.ConvertSecurityDescriptorToStringSecurityDescriptorW.argtypes = [
        ctypes.c_void_p,
        wintypes.DWORD,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.LPWSTR),
        ctypes.c_void_p,
    ]
    text = wintypes.LPWSTR()
    if not advapi32.ConvertSecurityDescriptorToStringSecurityDescriptorW(
        descriptor, 1, 0x00000004, ctypes.byref(text), None
    ):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        return text.value or ""
    finally:
        kernel32.LocalFree(text)


def _secure(path: Path) -> bool:
    """Make the node config at ``path`` its user's alone when it is not: on Windows the
    user-only DACL (a node.json maktaba-server's writer of #178 left with its folder's
    ACL, Authenticated Users on a second drive, keeps none of it); elsewhere mode 0600.
    True when it changed something. Raises OSError when it cannot be done."""
    if sys.platform == "win32":
        if _is_user_only(path):
            return False
        _set_user_only_dacl(str(path))
        return True
    mode = path.stat().st_mode
    if not mode & 0o077:
        return False
    os.chmod(path, 0o600)
    return True


def _create_user_only(path: Path) -> int:
    """Windows: create ``path`` (CREATE_NEW: FileExistsError when the name is taken) with
    the user-only DACL from the start and open it for writing with no sharing, so it never
    has another ACL and no other handle to it can be opened while it is written. Returns a
    file descriptor."""
    if sys.platform != "win32":
        raise OSError("a Windows call")
    import ctypes
    import msvcrt
    from ctypes import wintypes

    class SecurityAttributes(ctypes.Structure):
        _fields_ = [
            ("nLength", wintypes.DWORD),
            ("lpSecurityDescriptor", ctypes.c_void_p),
            ("bInheritHandle", wintypes.BOOL),
        ]

    _, kernel32 = _win32()
    generic_write, no_sharing, create_new, normal = 0x40000000, 0, 1, 0x80
    with _user_only_descriptor() as descriptor:
        attrs = SecurityAttributes(ctypes.sizeof(SecurityAttributes), descriptor, False)
        handle = kernel32.CreateFileW(
            str(path), generic_write, no_sharing, ctypes.byref(attrs), create_new, normal, None
        )
    if handle is None or handle == wintypes.HANDLE(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        return msvcrt.open_osfhandle(handle, os.O_WRONLY)
    except OSError:
        kernel32.CloseHandle(handle)
        raise


def _new_temp(path: Path) -> tuple[int, Path]:
    """A new temp file beside ``path`` for one write of it (``_leftover_name``), open for
    writing and the user's alone from the moment it exists: on Windows created with the
    user-only DACL and no sharing (``_create_user_only``), on POSIX with mode 0600.
    Raises OSError when it cannot be made so: the secret is then never written."""
    for _ in range(8):
        tmp = _leftover_name(path, ".tmp")
        try:
            if sys.platform == "win32":
                return _create_user_only(tmp), tmp
            flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC
            return os.open(tmp, flags, 0o600), tmp
        except FileExistsError:
            continue
    raise FileExistsError(errno.EEXIST, "no free temp file name", str(path.parent))


def _replace_file(path: Path, tmp: Path, backup: Path) -> None:
    """ReplaceFileW: ``tmp`` takes the place (and the ACL) of ``path``, whose old file is
    kept under ``backup``. Raises the Windows error (tests replace this call)."""
    if sys.platform != "win32":
        raise OSError("ReplaceFileW is a Windows call")
    import ctypes
    from ctypes import wintypes

    replace_file = ctypes.windll.kernel32.ReplaceFileW
    replace_file.argtypes = [
        wintypes.LPCWSTR,
        wintypes.LPCWSTR,
        wintypes.LPCWSTR,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.LPVOID,
    ]
    replace_file.restype = wintypes.BOOL
    if not replace_file(str(path), str(tmp), str(backup), 0, None, None):
        raise ctypes.WinError()


def _replace_keeping_acl(path: Path, tmp: Path) -> None:
    """Windows: put ``tmp`` in place of ``path`` keeping the replaced file's ACL (and its
    other attributes), through ReplaceFileW with a backup name. ReplaceFileW can fail half
    way (ERROR_UNABLE_TO_MOVE_REPLACEMENT_2: the old file under the backup name;
    ERROR_UNABLE_TO_MOVE_REPLACEMENT without a backup name: the old file gone), so a
    failure is settled before it is raised: the old file is put back, or, when it is gone,
    the new one takes its name (the user's alone from its creation). The only copy of a
    node's identity is never removed. Raises when ``path`` holds the old file again;
    returns when it holds the new one. A sharing violation (another process has node.json
    open; nothing moved) is tried again for a moment, as a recovery's renames are."""
    backup = _leftover_name(path, ".bak")
    for left in range(_BUSY_TRIES - 1, -1, -1):
        try:
            _replace_file(path, tmp, backup)
            break
        except OSError as exc:
            busy = getattr(exc, "winerror", None) == 32
            if busy and left and path.exists() and tmp.exists() and not backup.exists():
                time.sleep(_BUSY_SLEEP)
                continue
            if not _settle(path, tmp, backup):
                raise
            return
    # the old file under its backup name: a reader may have it open for a moment, and one
    # left whole would be read as the node config were node.json ever missing
    with contextlib.suppress(OSError):
        _unlink_when_free(backup)


def _settle(path: Path, tmp: Path, backup: Path) -> bool:
    """After a failed ReplaceFileW: ``path`` holds the old file (put back from ``backup``
    when the call left it there) or, when the old file is gone, the new one (``tmp``, the
    user's alone from its creation). What is left over goes only once ``path`` holds a
    whole file. True when the new file took the name. The old file is put back without
    replacing: another write that found node.json gone meanwhile and put its new one
    there keeps it (this write then fails)."""
    if not path.exists() and backup.exists():
        with contextlib.suppress(OSError):
            _rename_no_replace(backup, path)
    if not path.exists() and tmp.exists():
        os.replace(tmp, path)
        with contextlib.suppress(OSError):
            _unlink_when_free(backup)
        return True
    if path.exists():
        for left in (tmp, backup):
            with contextlib.suppress(OSError):
                _unlink_when_free(left)
    return False


def _unlink_when_free(path: Path) -> None:
    """Remove ``path``, trying again for a moment while another process has it open (a
    reader: ERROR_SHARING_VIOLATION); gone already is no error. Raises OSError when it
    stays."""
    for left in range(_BUSY_TRIES - 1, -1, -1):
        try:
            path.unlink()
            return
        except FileNotFoundError:
            return
        except PermissionError as exc:
            if getattr(exc, "winerror", None) != 32 or not left:
                raise
        time.sleep(_BUSY_SLEEP)


#: how long a write waits for another process's write of the same node config, and a
#: load's recovery for a write or another recovery (then it leaves it to a later load)
_LOCK_WAIT_SEC, _RECOVER_WAIT_SEC = 10.0, 0.5


@contextlib.contextmanager
def _one_at_a_time(path: Path, *, wait: float) -> Iterator[bool]:
    """Windows: hold the lock of the node config at ``path`` (a byte-range lock on
    ``.<name>.lock`` beside it; the OS lets it go when the process ends), waiting up to
    ``wait`` seconds for another process's write or recovery; yields whether it is held.
    Two writes at once can undo each other inside ReplaceFileW (one that fails puts back
    the file it had opened, over the other's new one), and a recovery in the middle of a
    write can put an older file back, so every write and recovery holds it. The lock file
    goes when no other process has it open. POSIX: always held (``os.replace`` is atomic:
    the last write wins, and nothing is put back)."""
    if sys.platform != "win32":
        yield True
        return
    import msvcrt

    lock = _lock_path(path)
    for left in range(_BUSY_TRIES - 1, -1, -1):
        try:  # access is denied for a moment while another process removes it
            fd = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_BINARY | os.O_NOINHERIT, 0o600)
            break
        except PermissionError:
            if not left:
                raise
            time.sleep(_BUSY_SLEEP)
    held = False
    try:
        deadline = time.monotonic() + wait
        while not held:
            try:
                msvcrt.locking(fd, msvcrt.LK_NBLCK, 1)
                held = True
            except OSError:
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.005)
        yield held
    finally:
        if held:
            with contextlib.suppress(OSError):
                os.lseek(fd, 0, os.SEEK_SET)
                msvcrt.locking(fd, msvcrt.LK_UNLCK, 1)
        os.close(fd)
        with contextlib.suppress(OSError):
            lock.unlink()  # fails while another process has it open: then it stays theirs


def _write(path: Path, data: Mapping[str, Any]) -> Path:
    """The one writer of a node config (``save``, and ``load``'s claim): atomically (a
    temp file in the same directory, flushed, then put in place), one write at a time
    (``_one_at_a_time``; one that waits too long fails as a write that cannot finish).
    The temp file is the user's alone from the moment it exists (``_new_temp``; the
    folder's ACL may let others read, ``Authenticated Users`` on a second drive); when it
    cannot be, nothing is written and the error is raised (a claim then fails as any
    failed claim: the file stays as it was). On Windows a file already there is made the
    user's alone first when it is not (``_secure``) and replaced with ReplaceFileW, which
    keeps that ACL."""
    path.parent.mkdir(parents=True, exist_ok=True)
    if os.name != "nt":
        with contextlib.suppress(OSError):
            os.chmod(path.parent, 0o700)
    body = json.dumps(dict(data), indent=2, sort_keys=True) + "\n"
    with _one_at_a_time(path, wait=_LOCK_WAIT_SEC) as held:
        if not held:
            raise TimeoutError(errno.ETIMEDOUT, "another write of the node config runs", str(path))
        return _write_held(path, body)


def _write_held(path: Path, body: str) -> Path:
    """``_write`` with the lock held."""
    existed = path.exists()
    if sys.platform == "win32" and existed:
        _secure(path)  # raises: the secret is never written into a file others may read
    fd, tmp = _new_temp(path)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(body)
            fh.flush()
            os.fsync(fh.fileno())
        if sys.platform == "win32" and existed:
            _replace_keeping_acl(path, tmp)
        else:
            os.replace(tmp, path)
    except BaseException:
        # the temp file goes unless it is the only copy left (a node config that was
        # there and is gone): then it stays for the next start
        if path.exists() or not existed:
            with contextlib.suppress(OSError):
                os.unlink(tmp)
        raise
    return path


def _update(path: Path, edit: Callable[[dict[str, Any]], dict[str, Any] | None]) -> bool:
    """Change the node config at ``path`` as it is with the write lock held (never a
    copy read before it, which another write may have changed meanwhile): ``edit`` gets
    its fields and gives the new ones, or None for no write. True when it wrote. Raises
    OSError (and ValueError for a node config that is not one now)."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with _one_at_a_time(path, wait=_LOCK_WAIT_SEC) as held:
        if not held:
            raise TimeoutError(errno.ETIMEDOUT, "another write of the node config runs", str(path))
        data = json.loads(_text(path))
        if not isinstance(data, dict):
            raise ValueError(f"{path}: not a node config")
        new = edit(data)
        if new is None:
            return False
        _write_held(path, json.dumps(new, indent=2, sort_keys=True) + "\n")
        return True


def save(cfg: NodeConfig, path: Path | None = None) -> Path:
    """Write the node config where this PC keeps it (``config_path``), through ``_write``."""
    return _write(path or config_path(), cfg.to_json())


def set_aside(path: Path) -> Path:
    """``join --rejoin``: the node config at ``path`` (the file that holds it, ``_source``)
    is kept as ``<name>.bak`` and forgotten, with everything a write that could not finish
    left of it (the next load would put that back) and the broken ones kept aside (they
    may hold part of the old credential). Returns the kept file."""
    backup = path.with_name(path.name + ".bak")
    with _one_at_a_time(path, wait=_LOCK_WAIT_SEC) as held:
        if not held:
            raise TimeoutError(errno.ETIMEDOUT, "another write of the node config runs", str(path))
        # in an order that leaves nothing read as this node config once it is kept,
        # wherever it stops: the other leftovers first (one that is whole and stays is an
        # error, before anything moves), a broken node.json next (the config is in a
        # leftover then), the file that holds it last
        src = _source(path)
        for p in _leftovers(path):
            if p == src:
                continue
            try:
                _unlink_when_free(p)
            except OSError as exc:
                if _whole_state(p) is not False:
                    raise OSError(
                        f"{p}, which every reader would take as the node config after a"
                        f" re-join, cannot be removed: {exc}"
                    ) from exc
        if src != path and path.exists():
            _unlink_when_free(path)
        _when_free(os.replace, src, backup)
        _prune_broken(path, all_of_them=True)
    return backup
