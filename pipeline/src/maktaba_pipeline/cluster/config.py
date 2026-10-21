"""The node config: who this machine is and how it reaches its home (design section 7).

One JSON file per machine, written when the join is approved, holding a database
credential, so it is written atomically and readable by its owner only (mode 0600 on
POSIX; on Windows the per-user ``%LOCALAPPDATA%`` directory is already private).

| OS | path |
|---|---|
| macOS | ``~/Library/Application Support/Maktaba/node.json`` |
| Linux | ``${XDG_CONFIG_HOME:-~/.config}/maktaba/node.json`` |
| Windows | ``%LOCALAPPDATA%\\Maktaba\\node.json`` (Epic 38: never in the roaming profile) |

A Windows machine that joined before Epic 38 keeps ``%APPDATA%\\Maktaba\\node.json``
while there is none in the new place (maktaba-server moves it with its own files).
``MAKTABA_NODE_CONFIG`` names another file (two nodes on one host, tests). Go resolves
the same path (``config.NodeConfigPath`` in maktaba-server, ``channel.NodeConfigPath``
in streaming).
"""

from __future__ import annotations

import contextlib
import json
import os
import platform
import tempfile
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

__all__ = ["ENV_VAR", "HomeRef", "NodeConfig", "config_path", "load", "save"]

ENV_VAR = "MAKTABA_NODE_CONFIG"


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

    @property
    def node8(self) -> str:
        """First 8 hex of the node id (its database role, its worker ids)."""
        return self.node_id.replace("-", "")[:8].lower()

    @property
    def joined(self) -> bool:
        return bool(self.database_url)

    def to_json(self) -> dict[str, Any]:
        return asdict(self)

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
        local = env.get("LOCALAPPDATA")
        cur = (Path(local) if local else home / "AppData" / "Local") / "Maktaba" / "node.json"
        if cur.is_file():
            return cur
        appdata = env.get("APPDATA")
        old = (Path(appdata) if appdata else home / "AppData" / "Roaming") / "Maktaba" / "node.json"
        return old if old.is_file() else cur
    xdg = env.get("XDG_CONFIG_HOME")
    base = Path(xdg) if xdg else home / ".config"
    return base / "maktaba" / "node.json"


def load(path: Path | None = None) -> NodeConfig | None:
    """The node config, or None when this machine never asked to join."""
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
        return NodeConfig.from_json(data)
    except ValueError as exc:
        raise ValueError(f"{path}: {exc}") from None


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


def save(cfg: NodeConfig, path: Path | None = None) -> Path:
    """Write atomically (temp file in the same directory, then rename), mode 0600."""
    path = path or config_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    if os.name != "nt":
        with contextlib.suppress(OSError):
            os.chmod(path.parent, 0o700)
    body = json.dumps(cfg.to_json(), indent=2, sort_keys=True) + "\n"
    fd, tmp = tempfile.mkstemp(prefix=".node-", suffix=".tmp", dir=path.parent)
    try:
        if os.name != "nt":
            os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(body)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
        if os.name == "nt":
            _restrict_to_user(path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise
    return path
