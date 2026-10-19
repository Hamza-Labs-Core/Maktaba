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
import re
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
    "common_dir",
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


def split_db_path(path: str) -> tuple[str, list[str]]:
    """``(filesystem root, parts)`` of a database-form path: the root is ``/``, a drive
    (``Y:/``), a UNC share (``//nas/share/``) or empty (relative). The hub's rule."""
    p = path.strip().replace("\\", "/")
    rest: str
    m = re.match(r"^((?:smb|nfs)://[^/]+/[^/]+/?)(.*)$", p, re.IGNORECASE)
    if m:
        # story 31.3: smb://server/share/... keeps its server and share as the root
        root, rest = m.group(1).rstrip("/") + "/", m.group(2)
    elif p.startswith("//"):
        segs = p[2:].split("/", 2)
        if len(segs) == 1:
            root, rest = f"//{segs[0]}/", ""
        elif len(segs) == 2:
            root, rest = f"//{segs[0]}/{segs[1]}/", ""
        else:
            root, rest = f"//{segs[0]}/{segs[1]}/", segs[2]
    elif len(p) >= 2 and p[1] == ":" and p[0].isascii() and p[0].isalpha():
        root, rest = p[:2] + "/", p[2:].lstrip("/")
    elif p.startswith("/"):
        root, rest = "/", p.lstrip("/")
    else:
        root, rest = "", p
    return root, [s for s in rest.split("/") if s not in ("", ".")]


def join_db_path(root: str, parts: Sequence[str]) -> str:
    return root + "/".join(parts)


def common_dir(paths: Sequence[str]) -> tuple[str, bool]:
    """The deepest directory shared by every path (compared case-insensitively, in the
    first path's case) and whether it is more than a filesystem root."""
    if not paths:
        return "", False
    root, common = split_db_path(paths[0])
    for path in paths[1:]:
        r, parts = split_db_path(path)
        if r.lower() != root.lower():
            return "", False
        n = 0
        while n < len(common) and n < len(parts) and common[n].lower() == parts[n].lower():
            n += 1
        common = common[:n]
    if root == "" and not common:
        return "", False
    return join_db_path(root, common), bool(common)


def common_root(paths: Iterable[str]) -> str | None:
    """The deepest directory common to ``paths``, or None when they share nothing but a
    filesystem root (``/``, a drive, a UNC share)."""
    found, ok = common_dir([p for p in paths if p and p.strip()])
    return found if ok else None


def share_root_from(
    roots: Sequence[tuple[str, str, int]], env: Mapping[str, str] | None = None
) -> str | None:
    """The share root from ``(library id, root path, the library's videos)`` of every root
    of a live library — the hub's rule (``ShareRootOf``), so both agree before the hub's
    value arrives: the deepest directory common to every root; failing that, the library
    with the most videos (ties: the smallest id), its roots' common directory, else its
    first root. ``MAKTABA_SHARE_ROOT`` overrides."""
    env = os.environ if env is None else env
    explicit = (env.get(ENV_SHARE_ROOT) or "").strip()
    if explicit:
        r, parts = split_db_path(explicit)
        return join_db_path(r, parts)
    rows = [(lib, path, videos) for lib, path, videos in roots if path and path.strip()]
    if not rows:
        return None
    found, ok = common_dir([path for _, path, _ in rows])
    if ok:
        return found
    videos: dict[str, int] = {}
    by_lib: dict[str, list[str]] = {}
    for lib, path, n in rows:
        videos[lib] = n
        by_lib.setdefault(lib, []).append(path)
    busiest = sorted(by_lib, key=lambda lib: (-videos[lib], lib))[0]
    paths = by_lib[busiest]
    found, ok = common_dir(paths)
    if ok:
        return found
    r, parts = split_db_path(sorted(paths)[0])
    return join_db_path(r, parts)


#: every root of a live library, with the library's present videos (the hub's query)
LIBRARY_ROOTS_SQL = (
    "SELECT l.id AS library_id, lr.path AS path,"
    " (SELECT count(*) FROM videos v WHERE v.library_id = l.id AND v.deleted_at IS NULL)"
    " AS videos"
    " FROM library_roots lr JOIN libraries l ON l.id = lr.library_id"
    " WHERE l.deleted_at IS NULL ORDER BY l.id, lr.path"
)


async def load_library_roots(db: Any) -> list[tuple[str, str, int]]:
    """``(library id, root path, videos)`` from ``library_roots`` — where the API keeps
    the roots (``libraries.roots`` is the old copy) — for the live libraries."""
    rows = await db.fetch(LIBRARY_ROOTS_SQL)
    return [(str(r["library_id"]), str(r["path"]), int(r["videos"] or 0)) for r in rows]


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
    roots: list[tuple[str, str, int]] = []
    if not (env.get(ENV_SHARE_ROOT) or "").strip():
        if db is None:
            return None, "no database to find the share"
        try:
            roots = await load_library_roots(db)
        except Exception as exc:  # noqa: BLE001 - the share is optional; logs stay local
            return None, f"libraries unreadable: {exc}"
    root = share_root_from(roots, env)
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
