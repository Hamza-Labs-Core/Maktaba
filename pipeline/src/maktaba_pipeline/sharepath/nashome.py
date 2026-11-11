"""A NAS as the home: its own folders named as the SMB shares they are (Epic 59, story 59.13).

On a NAS home the libraries are local folders (``/volume1/Movies`` on DSM, mounted in the
container at the same path), not network mounts, so :mod:`.mounts` finds nothing and the
paths would stay in this machine's own spelling. A Mac node opens the same files as
``/Volumes/Movies`` over SMB. This module gives the home's own folders their share:

* **the NAS's record**: a ``storage_servers`` row for the NAS itself, named from its host
  name (or the name setup kept) with every address the nodes may mount it by (its mDNS
  name, its LAN addresses), so ``//diskstation.local/Movies`` and ``//192.168.1.20/Movies``
  both match it (:func:`~.resolver.match_server`);
* **its shares**: that row's ``exports`` (local folder -> share name), the table the
  resolver already reads for a node's NFS mounts (on a NAS the NFS export path *is* the
  share's folder). An entry comes from the platform's layout (:func:`rule_share`: DSM's
  ``/volume<N>/<share>``, Unraid's ``/mnt/user/<share>``, QNAP's ``/share/<share>``), from
  setup's share step (``MAKTABA_HOME_SHARES``: TrueNAS, whose share names are not in the
  path, and a mount at another path such as the old ``/media`` catch-all), or from the
  admin (Settings). An empty share name is a folder that is not shared on the network
  (local only): no node can work on it;
* **the home's resolver**: one ``local`` share mount per entry (:func:`local_share_mounts`),
  so the canonical conversion (:mod:`.auto`) turns ``/volume1/Movies/a.mkv`` into
  ``smb://diskstation/Movies/a.mkv`` and the share map the home reports names it for the
  API and the streaming service.

An entry already in the record is never changed here (the admin's edit stands); this only
adds the folders the record does not name yet.
"""

from __future__ import annotations

import json
import os
import re
import socket
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.paths import _norm
from ..log import get_logger
from .canonical import is_canonical, share_prefix
from .resolver import ShareMount, StorageServer
from .servers import load_servers, save_server

__all__ = [
    "ENV_HOME_SHARES",
    "ENV_PLATFORM",
    "HomeShares",
    "SetupShares",
    "ensure_home_server",
    "home_names",
    "is_host_path",
    "local_share_mounts",
    "nas_home",
    "rule_share",
    "setup_shares",
]

_log = get_logger()

#: the NAS platform (story 59.04's detection, or the install file's), as the supervisor
#: passes it
ENV_PLATFORM = "MAKTABA_PLATFORM"
#: setup's share step: ``{"server": name, "aliases": [...], "roots": {folder: share}}``
ENV_HOME_SHARES = "MAKTABA_HOME_SHARES"

#: the NAS systems (hostinfo.IsNAS) and a container on some other Linux, whose
#: households name their shares at setup ("Other" in the story's table)
NAS_PLATFORMS = frozenset({"dsm", "truenas", "unraid", "qts", "omv", "nas"})
HOME_PLATFORMS = NAS_PLATFORMS | {"docker"}

_DSM = re.compile(r"^(/volume(?:USB)?\d+/([^/]+))(?:/|$)")
_UNRAID = re.compile(r"^(/mnt/user0?/([^/]+))(?:/|$)")
_QNAP = re.compile(r"^(/share/(?:(?:CE_)?CACHEDEV\d+_DATA/)?([^/]+))(?:/|$)")
_QNAP_POOL = re.compile(r"^(?:CE_)?CACHEDEV\d+_DATA$")

#: where each platform keeps its shares (a library elsewhere is a mount at another path)
_HOST_ROOTS: dict[str, re.Pattern[str]] = {
    "dsm": re.compile(r"^/volume(?:USB)?\d+(?:/|$)"),
    "truenas": re.compile(r"^/mnt/"),
    "unraid": re.compile(r"^/mnt/"),
    "qts": re.compile(r"^/share/"),
    "omv": re.compile(r"^/srv/"),
}
#: the old compose file's catch-all, a mount at another path on any platform
_CATCH_ALL = re.compile(r"^/media(?:/|$)")


def _clean(path: str) -> str:
    p = path.replace("\\", "/")
    return p.rstrip("/") or p


def rule_share(platform: str, path: str) -> tuple[str, str] | None:
    """``(share, the share's folder)`` of a local path by the platform's layout alone;
    ``None`` when the platform has no rule (TrueNAS, OMV, any other) or the path is on
    none (a volume's own root, DSM's ``@eaDir`` and ``#recycle``)."""
    p = _clean(path)
    m: re.Match[str] | None
    if platform == "dsm":
        m = _DSM.match(p)
    elif platform == "unraid":
        m = _UNRAID.match(p)
    elif platform == "qts":
        m = _QNAP.match(p)
        if m is not None and _QNAP_POOL.match(m.group(2)):
            m = None
    else:
        return None
    if m is None:
        return None
    share = m.group(2)
    if share.startswith(("@", "#", ".")):
        return None
    return share, m.group(1)


def is_host_path(platform: str, path: str) -> bool:
    """Whether ``path`` is where the NAS itself keeps a share; ``False`` for a mount at
    another path (the ``/media`` catch-all), which nodes match by the share name the
    household gave (Settings says so)."""
    p = _clean(path)
    if _CATCH_ALL.match(p):
        return False
    rule = _HOST_ROOTS.get(platform)
    return True if rule is None else bool(rule.match(p))


def nas_home(env: Mapping[str, str] | None = None) -> str | None:
    """This home's platform when its own folders can be shares (a NAS, or a container
    whose household names them); ``None`` otherwise."""
    env = os.environ if env is None else env
    p = (env.get(ENV_PLATFORM) or "").strip().lower()
    return p if p in HOME_PLATFORMS else None


@dataclass(frozen=True, slots=True)
class SetupShares:
    """What setup's share step kept (``MAKTABA_HOME_SHARES``)."""

    server: str = ""
    aliases: tuple[str, ...] = ()
    roots: tuple[tuple[str, str], ...] = ()


def setup_shares(env: Mapping[str, str] | None = None) -> SetupShares:
    env = os.environ if env is None else env
    raw = (env.get(ENV_HOME_SHARES) or "").strip()
    if not raw:
        return SetupShares()
    try:
        v = json.loads(raw)
    except ValueError:
        _log.warning("home_shares_unreadable")
        return SetupShares()
    if not isinstance(v, dict):
        return SetupShares()
    raw_roots = v.get("roots")
    roots: dict[str, Any] = raw_roots if isinstance(raw_roots, dict) else {}
    return SetupShares(
        server=str(v.get("server") or "").strip().lower(),
        aliases=tuple(str(a).strip() for a in (v.get("aliases") or []) if str(a).strip()),
        roots=tuple(
            (_clean(str(k)), str(s or "").strip())
            for k, s in roots.items()
            if str(k).startswith("/")
        ),
    )


def _local_ips() -> list[str]:
    try:
        import psutil
    except ImportError:  # pragma: no cover - psutil ships with the pipeline
        return []
    out: list[str] = []
    for addrs in psutil.net_if_addrs().values():
        for a in addrs:
            if a.family == socket.AF_INET and not a.address.startswith(("127.", "169.254.")):
                out.append(a.address)
    return sorted(set(out))


def home_names(
    hostname: str | None = None,
    ips: Iterable[str] | None = None,
    env: Mapping[str, str] | None = None,
) -> list[str]:
    """The names nodes may mount this machine by: its host name, ``<host>.local``, its
    LAN addresses (and ``MAKTABA_HOST_IP``, a bridge's)."""
    env = os.environ if env is None else env
    host = (hostname if hostname is not None else socket.gethostname()).strip().lower()
    short = host.split(".", 1)[0]
    names: list[str] = []
    for n in (short, f"{short}.local" if short else "", host):
        if n and n not in names:
            names.append(n)
    for ip in [
        *(env.get("MAKTABA_HOST_IP", "").split(",")),
        *(_local_ips() if ips is None else ips),
    ]:
        ip = ip.strip()
        if ip and ip not in names:
            names.append(ip)
    return names


@dataclass(frozen=True, slots=True)
class HomeShares:
    """The NAS's record and the platform: what :func:`local_share_mounts` reads."""

    server: StorageServer
    platform: str
    #: roots no share names and no rule finds (setup asks for them)
    unnamed: tuple[str, ...] = field(default=())


def _find_self(
    servers: Sequence[StorageServer], name: str, names: Sequence[str]
) -> StorageServer | None:
    if name:
        for s in servers:
            if s.name == name:
                return s
    mine = {n.lower().rstrip(".") for n in names}
    for s in servers:
        if s.names & mine:
            return s
    return None


def _covered(exports: Mapping[str, str], root: str) -> bool:
    r = root.lower()
    for path in exports:
        p = _clean(path).lower()
        if r == p or r.startswith(p + "/"):
            return True
    return False


async def ensure_home_server(
    db: Any,
    roots: Iterable[str],
    *,
    platform: str,
    names: Sequence[str] | None = None,
    setup: SetupShares | None = None,
) -> HomeShares | None:
    """The NAS's own record, made or completed: its names, setup's shares and each local
    library root the platform's rule names. ``None`` when this home is not one whose
    folders can be shares, or the record cannot be written (an older schema)."""
    if platform not in HOME_PLATFORMS:
        return None
    setup = setup or setup_shares()
    names = list(names if names is not None else home_names())
    if not names and not setup.server:
        return None
    try:
        servers = await load_servers(db)
    except Exception:  # noqa: BLE001 - load_servers already returns [] without the table
        servers = []
    existing = _find_self(servers, setup.server, names)
    add_exports: dict[str, str] = {}
    have = dict(existing.exports) if existing else {}
    for folder, share in setup.roots:
        if not _covered({**have, **add_exports}, folder):
            add_exports[folder] = share
    unnamed: list[str] = []
    for root in roots:
        if not root or is_canonical(root) or not root.startswith("/"):
            continue
        r = _clean(root)
        if _covered({**have, **add_exports}, r):
            continue
        rule = rule_share(platform, r)
        if rule is None:
            unnamed.append(r)
            continue
        add_exports[rule[1]] = rule[0]
    addresses = [*setup.aliases, *names]
    name = existing.name if existing else (setup.server or names[0].split(".", 1)[0])
    new_addrs = [a for a in addresses if a.lower() not in (existing.names if existing else {name})]
    server = existing
    if existing is None or add_exports or new_addrs:
        try:
            server = await save_server(
                db,
                StorageServer(
                    name=name,
                    addresses=tuple(dict.fromkeys(addresses)),
                    exports=tuple(add_exports.items()),
                ),
            )
        except Exception as exc:  # noqa: BLE001 - an older schema: nothing is shared
            _log.warning("home_server_not_saved", error=str(exc))
            return None
        _log.info(
            "home_server_saved",
            server=name,
            added_shares=add_exports,
            added_addresses=new_addrs,
        )
    assert server is not None
    return HomeShares(server=server, platform=platform, unnamed=tuple(unnamed))


def local_share_mounts(home: HomeShares | None) -> list[ShareMount]:
    """The home's own folders as shares: one ``local`` mount per named folder of the
    NAS's record (an empty name is local only: none)."""
    if home is None:
        return []
    out: list[ShareMount] = []
    seen: set[str] = set()
    for folder, share in sorted(home.server.exports, key=lambda e: -len(e[0])):
        share = share.strip().strip("/")
        if not share or not folder.startswith("/"):
            continue
        prefix = share_prefix("smb", home.server.name, share)
        if prefix.lower() in seen:
            continue
        seen.add(prefix.lower())
        out.append(
            ShareMount(
                prefix=prefix,
                local=_norm(_clean(folder) + "/"),
                protocol="local",
                server=home.server.name,
                source="nas",
            )
        )
    return out
