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
"""

from __future__ import annotations

import contextlib
import json
import os
import platform
import sys
import tempfile
from collections.abc import Mapping
from dataclasses import asdict, dataclass, field, replace
from pathlib import Path
from typing import Any

__all__ = ["ENV_VAR", "HomeRef", "NodeConfig", "config_path", "load", "machine_id", "save"]

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
        # place (where a new join writes; another PC's roaming config is not this PC's)
        old = _roaming_config(env, home)
        if old.is_file() and not _claimed_elsewhere(old, env):
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


def _owner(path: Path) -> str:
    """The machine_id a node config records: "" when it has none or cannot be read (a
    broken file is never skipped: ``load`` says what is wrong with it)."""
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
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
    this PC: its ``machine_id`` is written in, everything else kept (``_write``)."""
    path = path or config_path()
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return None
    except ValueError as exc:
        # a typo or a truncated file: never "not joined", and the error names the file
        raise ValueError(f"{path}: not a node config ({exc})") from None
    if not isinstance(data, dict):
        raise ValueError(f"{path}: not a node config")
    try:
        cfg = NodeConfig.from_json(data)
    except ValueError as exc:
        raise ValueError(f"{path}: {exc}") from None
    if not cfg.machine_id and _is_roaming_config(path):
        this = machine_id()
        if this:
            try:
                _write(path, {**data, "machine_id": this})
            except OSError as exc:
                from ..log import get_logger

                get_logger().warning("node_config_claim_failed", path=str(path), error=str(exc))
            else:
                cfg = replace(cfg, machine_id=this)
    return cfg


def _restrict_to_user(path: Path) -> None:
    """Windows: the file holds the home's database password; chmod means nothing there,
    so its ACL is cut to the current user (inheritance off). Best effort: a failure is
    logged, the file stays where the OS put it (the profile directory is per user)."""
    import subprocess

    user = os.environ.get("USERNAME")
    if not user:
        return
    try:
        subprocess.run(
            ["icacls", str(path), "/inheritance:r", "/grant:r", f"{user}:F"],
            check=True, capture_output=True, timeout=15,
        )  # fmt: skip
    except (OSError, subprocess.SubprocessError) as exc:
        from ..log import get_logger

        get_logger().warning("node_config_acl_failed", path=str(path), error=str(exc))


def _replace_keeping_acl(path: Path, tmp: Path) -> None:
    """Windows: put ``tmp`` in place of ``path`` with ReplaceFileW, which keeps the
    replaced file's ACL (and its other attributes)."""
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
    if not replace_file(str(path), str(tmp), None, 0, None, None):
        raise ctypes.WinError()


def _write(path: Path, data: Mapping[str, Any]) -> Path:
    """The one writer of a node config (``save``, and ``load``'s claim): atomically (a
    temp file in the same directory, flushed, then put in place), mode 0600 on POSIX. On
    Windows a file already there is replaced with ReplaceFileW, which keeps its ACL; a
    new one is limited to the user before it gets its name."""
    path.parent.mkdir(parents=True, exist_ok=True)
    if os.name != "nt":
        with contextlib.suppress(OSError):
            os.chmod(path.parent, 0o700)
    body = json.dumps(dict(data), indent=2, sort_keys=True) + "\n"
    fd, tmp = tempfile.mkstemp(prefix=".node-", suffix=".tmp", dir=path.parent)
    try:
        if os.name != "nt":
            os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(body)
            fh.flush()
            os.fsync(fh.fileno())
        if sys.platform == "win32" and path.exists():
            _replace_keeping_acl(path, Path(tmp))
        else:
            if sys.platform == "win32":
                _restrict_to_user(Path(tmp))
            os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise
    return path


def save(cfg: NodeConfig, path: Path | None = None) -> Path:
    """Write the node config where this PC keeps it (``config_path``), through ``_write``."""
    return _write(path or config_path(), cfg.to_json())
