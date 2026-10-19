"""Where the cluster's files live: on the share with the videos (design-agent.md §4).

- **Share root** (database form, like the library roots): ``MAKTABA_SHARE_ROOT`` when
  set; otherwise the deepest directory common to every library root; when the libraries
  share nothing but the filesystem root, the root of the library with the most videos.
- **Cluster directory**: ``<share root>/maktaba/cluster`` translated to this machine's path
  by its path map; ``MAKTABA_CLUSTER_DIR`` overrides with a local path.
- **Local fallback** (share unreachable): ``~/.maktaba/cluster``. Secrets (the home agent
  key) stay local, never on the share.
- **The machine's folder**: its IP, the local address of a socket aimed at the home's API
  host (no packet is sent); a loopback answer (a home process) becomes the machine's first
  private IPv4. IPv6 is written with ``:`` as ``-``.
"""

from __future__ import annotations

import ipaddress
import os
import socket
import urllib.parse
from collections.abc import Iterable, Mapping, Sequence
from pathlib import Path
from typing import Any

from ..engine.paths import PathMap

__all__ = [
    "CLUSTER_SUBDIR",
    "ENV_CLUSTER_DIR",
    "ENV_IP",
    "ENV_KEY_PATH",
    "ENV_SHARE_ROOT",
    "cluster_dir_for",
    "common_root",
    "folder_name",
    "home_key_path",
    "load_library_roots",
    "local_dir",
    "machine_ip",
    "resolve_cluster_dir",
    "share_root_from",
]

ENV_SHARE_ROOT = "MAKTABA_SHARE_ROOT"
ENV_CLUSTER_DIR = "MAKTABA_CLUSTER_DIR"
ENV_KEY_PATH = "MAKTABA_HOME_AGENT_KEY"
#: an older name of the same setting
ENV_KEY_PATH_ALT = "MAKTABA_CLUSTER_KEY_PATH"
ENV_IP = "MAKTABA_CLUSTER_IP"
CLUSTER_SUBDIR = "maktaba/cluster"


def local_dir() -> Path:
    """``~/.maktaba/cluster``: this machine's own, never on the share."""
    return Path.home() / ".maktaba" / "cluster"


def home_key_path(env: Mapping[str, str] | None = None) -> Path:
    """The home agent key the home's API writes (``MAKTABA_HOME_AGENT_KEY`` names
    another file)."""
    env = os.environ if env is None else env
    override = env.get(ENV_KEY_PATH) or env.get(ENV_KEY_PATH_ALT)
    return Path(override).expanduser() if override else local_dir() / "home-agent.key"


# --- the share root -------------------------------------------------------------------


def _parts(path: str) -> list[str]:
    p = path.replace("\\", "/").rstrip("/")
    return p.split("/") if p else [""]


def _is_fs_root(parts: Sequence[str]) -> bool:
    """``/``, a drive (``Y:``), or a UNC host without its share (``//nas``)."""
    real = [p for p in parts if p]
    if not real:
        return True
    return len(real) == 1 and (
        (len(real[0]) == 2 and real[0][1] == ":") or list(parts[:2]) == ["", ""]
    )


def common_root(paths: Iterable[str]) -> str | None:
    """The deepest directory common to ``paths`` (``None`` when they share nothing but the
    filesystem root). Compared case-insensitively, returned in the first path's case."""
    split = [_parts(p) for p in paths if p and p.strip()]
    if not split:
        return None
    first = split[0]
    n = len(first)
    for other in split[1:]:
        k = 0
        while k < min(n, len(other)) and first[k].lower() == other[k].lower():
            k += 1
        n = k
    common = first[:n]
    if _is_fs_root(common):
        return None
    joined = "/".join(common)
    return joined or None


def share_root_from(
    libraries: Sequence[tuple[Sequence[str], int]], env: Mapping[str, str] | None = None
) -> str | None:
    """``libraries``: ``(roots, video count)`` of every library not deleted."""
    env = os.environ if env is None else env
    explicit = (env.get(ENV_SHARE_ROOT) or "").strip()
    if explicit:
        return explicit.replace("\\", "/").rstrip("/")
    roots = [r for lib_roots, _ in libraries for r in lib_roots]
    common = common_root(roots)
    if common is not None:
        return common
    busiest = sorted((lib for lib in libraries if lib[0]), key=lambda lib: lib[1], reverse=True)
    if not busiest:
        return None
    lib_roots = list(busiest[0][0])
    own = common_root(lib_roots)
    return own if own is not None else lib_roots[0].replace("\\", "/").rstrip("/")


async def load_library_roots(db: Any) -> list[tuple[list[str], int]]:
    """``(roots, videos)`` of every library not deleted."""
    from ..engine.sql import loads

    rows = await db.fetch("SELECT id, roots FROM libraries WHERE deleted_at IS NULL")
    counts = {
        str(r["library_id"]): int(r["n"])
        for r in await db.fetch(
            "SELECT library_id, count(*) AS n FROM videos"
            " WHERE deleted_at IS NULL GROUP BY library_id"
        )
    }
    out: list[tuple[list[str], int]] = []
    for row in rows:
        roots = row["roots"]
        items = loads(roots) if isinstance(roots, str) else list(roots or [])
        out.append(([str(r) for r in items or []], counts.get(str(row["id"]), 0)))
    return out


def cluster_dir_for(share_root: str, path_map: PathMap) -> Path:
    return Path(path_map.to_local(share_root.rstrip("/") + "/" + CLUSTER_SUBDIR))


async def resolve_cluster_dir(
    db: Any | None, path_map: PathMap, env: Mapping[str, str] | None = None
) -> tuple[Path | None, str]:
    """``(the cluster directory on the share, how it was found)``; ``(None, why not)``
    when there is no share to use."""
    env = os.environ if env is None else env
    override = (env.get(ENV_CLUSTER_DIR) or "").strip()
    if override:
        return Path(override).expanduser(), ENV_CLUSTER_DIR
    libraries: list[tuple[list[str], int]] = []
    if not (env.get(ENV_SHARE_ROOT) or "").strip():
        if db is None:
            return None, "no database to find the share"
        try:
            libraries = await load_library_roots(db)
        except Exception as exc:  # noqa: BLE001 - the share is optional; logs stay local
            return None, f"libraries unreadable: {exc}"
    root = share_root_from(libraries, env)
    if root is None:
        return None, "no library roots"
    return cluster_dir_for(root, path_map), f"share root {root}"


# --- the machine's folder ---------------------------------------------------------------


def _private_ipv4() -> str | None:
    try:
        import psutil

        addrs = psutil.net_if_addrs()
    except Exception:  # noqa: BLE001
        return None
    candidates: list[str] = []
    for nics in addrs.values():
        for a in nics:
            if a.family != socket.AF_INET:
                continue
            try:
                ip = ipaddress.ip_address(a.address)
            except ValueError:
                continue
            if ip.is_loopback or ip.is_link_local:
                continue
            if ip.is_private:
                candidates.append(a.address)
    return sorted(candidates)[0] if candidates else None


def machine_ip(home_url: str | None, env: Mapping[str, str] | None = None) -> str:
    """This machine's address as the home sees it (see the module doc);
    ``MAKTABA_CLUSTER_IP`` overrides it (a container's own address is not the machine's)."""
    env = os.environ if env is None else env
    override = (env.get(ENV_IP) or "").strip()
    if override:
        return folder_name(override)
    found: str | None = None
    if home_url:
        parsed = urllib.parse.urlsplit(home_url if "//" in home_url else "//" + home_url)
        host = parsed.hostname
        port = parsed.port or (443 if parsed.scheme in ("https", "wss") else 80)
        if host:
            try:
                info = socket.getaddrinfo(host, port, type=socket.SOCK_DGRAM)[0]
                with socket.socket(info[0], socket.SOCK_DGRAM) as s:
                    s.connect(info[4])  # UDP: picks the route, sends nothing
                    found = str(s.getsockname()[0])
            except OSError:
                found = None
    if found is not None:
        try:
            if ipaddress.ip_address(found.split("%")[0]).is_loopback:
                found = None
        except ValueError:
            found = None
    if found is None:
        found = _private_ipv4() or "127.0.0.1"
    return folder_name(found)


def folder_name(ip: str) -> str:
    return ip.split("%")[0].replace(":", "-")
