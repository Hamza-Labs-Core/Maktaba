"""Canonical path <-> this machine's path (story 31.3).

:class:`Resolver` is a :class:`~maktaba_pipeline.engine.paths.PathMap`, so every caller
that maps database paths today (``ctx.local_path``, ``engine.paths.to_local``, the
storage check, the cluster share) resolves canonical paths with no change. Its
entries come from, highest precedence first:

1. ``--path-map`` / ``MAKTABA_PATH_MAP`` given to this process (any keys);
2. the admin's per-machine override on the Machines page ("share -> local mount");
3. the shares this machine has mounted (:mod:`.mounts`), matched to a
   ``storage_servers`` record by its address set: ``smb://nas/Originals/ -> O:/``;
4. on Windows, a UNC fallback per server: ``smb://nas/ -> //192.168.50.94/``
   (Windows opens ``\\\\server\\share\\...`` without a drive letter);
5. legacy prefixes (paths stored before the migration, the inferred map):
   ``/Volumes/Originals/ -> O:/``.

On top of the prefix rewrite it applies the server's character mapping to the part
after an SMB mount and, when the mapped name does not exist, tries the NFD spelling
and the other side of the mapping (names written by a Mac over SMB can be NFD on
the NAS).
"""

from __future__ import annotations

import functools
import os
import re
import socket
import unicodedata
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.paths import PathMap, _norm, find_existing
from .canonical import FULLWIDTH, CharMap, normalise_rel, parse_canonical, share_prefix
from .mounts import Mount

__all__ = [
    "Resolver",
    "ShareMount",
    "StorageServer",
    "build_resolver",
    "match_server",
    "share_mounts",
]

Resolve = Callable[[str], frozenset[str]]


@dataclass(frozen=True, slots=True)
class StorageServer:
    """A ``storage_servers`` row: the identity a canonical path names."""

    name: str
    addresses: tuple[str, ...] = ()
    char_map: str = "none"
    #: NFS export path -> share name (``/volume1/Originals`` -> ``Originals``)
    exports: tuple[tuple[str, str], ...] = ()
    id: str | None = None

    @property
    def names(self) -> frozenset[str]:
        return frozenset(a.strip().lower().rstrip(".") for a in (self.name, *self.addresses) if a)

    def to_json(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "name": self.name,
            "addresses": list(self.addresses),
            "char_map": self.char_map,
            "exports": dict(self.exports),
        }


@functools.lru_cache(maxsize=256)
def _resolve_ips(host: str) -> frozenset[str]:
    try:
        infos = socket.getaddrinfo(host, None, proto=socket.IPPROTO_TCP)
    except (OSError, UnicodeError):
        return frozenset()
    return frozenset(str(i[4][0]) for i in infos)


def match_server(
    servers: Sequence[StorageServer], host: str, *, resolve: Resolve | None = None
) -> StorageServer | None:
    """The server a mount's host belongs to: by name or address, else (``resolve``)
    by the IPs both resolve to."""
    h = host.strip().lower().rstrip(".")
    for s in servers:
        if h in s.names:
            return s
    if resolve is None:
        return None
    ips = resolve(h)
    if not ips:
        return None
    for s in servers:
        mine: set[str] = set()
        for a in s.names:
            mine |= resolve(a)
        if ips & mine:
            return s
    return None


_VOLUME_RE = re.compile(r"^/volume\d+/(.+)$")


def share_of(server: StorageServer, mount: Mount) -> tuple[str, str]:
    """``(scheme, share[/sub])`` of a mount: an SMB share as named; an NFS export by
    the server's export table, a Synology ``/volumeN/<share>`` path, else as ``nfs``."""
    if mount.protocol == "smb":
        return "smb", mount.share.replace("\\", "/").strip("/")
    export = "/" + mount.share.strip("/")
    best: tuple[str, str] | None = None
    for path, share in server.exports:
        p = "/" + path.strip("/")
        if (export.lower() == p.lower() or export.lower().startswith(p.lower() + "/")) and (
            best is None or len(p) > len(best[0])
        ):
            best = (p, share)
    if best is not None:
        rest = export[len(best[0]) :].strip("/")
        return "smb", "/".join(x for x in (best[1].strip("/"), rest) if x)
    m = _VOLUME_RE.match(export)
    if m:
        return "smb", m.group(1).strip("/")
    return "nfs", export.strip("/")


@dataclass(frozen=True, slots=True)
class ShareMount:
    prefix: str  #: canonical directory, ``smb://nas/Originals/Backup/``
    local: str  #: ``Y:/``, ``/Volumes/Originals/``, ``//192.168.50.94/``
    protocol: str  #: how this machine reaches it: ``smb`` | ``nfs`` | ``admin`` | ``unc``
    server: str
    source: str
    char_map: str = "none"

    def as_json(self) -> dict[str, str]:
        return {
            "share": self.prefix.rstrip("/"),
            "local": self.local,
            "protocol": self.protocol,
            "server": self.server,
            "source": self.source,
        }


def share_mounts(
    servers: Sequence[StorageServer],
    mounts: Iterable[Mount],
    *,
    system: str,
    resolve: Resolve | None = None,
) -> list[ShareMount]:
    """The canonical shares this machine reaches, from its mounts; on Windows also
    one UNC root per server (only used when no drive maps the share)."""
    out: list[ShareMount] = []
    hosts_used: dict[str, str] = {}
    for m in mounts:
        server = match_server(servers, m.host, resolve=resolve)
        if server is None:
            continue
        scheme, share = share_of(server, m)
        prefix = share_prefix(scheme, server.name, share)
        out.append(
            ShareMount(
                prefix=prefix,
                local=_norm(m.local),
                protocol=m.protocol,
                server=server.name,
                source=m.source,
                char_map=server.char_map if m.protocol == "smb" else "none",
            )
        )
        if m.protocol == "smb":
            hosts_used.setdefault(server.name, m.host)
    if system == "Windows":
        for s in servers:
            host = hosts_used.get(s.name) or next(iter(s.addresses), None) or s.name
            out.append(
                ShareMount(
                    prefix=f"smb://{s.name.lower()}/",
                    local=f"//{host}/",
                    protocol="unc",
                    server=s.name,
                    source="unc",
                    char_map=s.char_map,
                )
            )
    return out


def _canon_key(key: str) -> str:
    """An override key in canonical shape (server lower case) or as given."""
    sp = parse_canonical(key.rstrip("/") or key)
    if sp is None or sp.scheme == "file":
        return _norm(key)
    return _norm(share_prefix(sp.scheme, sp.server, "/".join(x for x in (sp.share, sp.rel) if x)))


def _root(dst: str) -> str:
    """A mapped directory without its trailing slash, except a drive (``O:/``) or ``/``."""
    if dst == "/" or re.match(r"^[A-Za-z]:/$", dst):
        return dst
    return dst.rstrip("/")


def _exists(p: str) -> bool:
    try:
        return os.path.exists(p)
    except (OSError, ValueError):
        return False


@dataclass(frozen=True, slots=True)
class Resolver(PathMap):
    """A :class:`PathMap` that also knows the shares behind canonical paths."""

    shares: tuple[ShareMount, ...] = ()
    #: (canonical prefix, char map spec) for prefixes reached over SMB with a mapping
    char_maps: tuple[tuple[str, str], ...] = ()
    probe: bool = True
    exists: Callable[[str], bool] = field(default=_exists, compare=False)

    # -- canonical -> local -------------------------------------------------------

    def _forward(self, db_path: str) -> tuple[str, str, str] | None:
        """``(src prefix, dst prefix, rest)`` for the longest matching entry; a share
        root stored without its slash (a library root) matches its own entry."""
        p = db_path.replace("\\", "/")
        low = p.lower()
        slashed = low if low.endswith("/") else low + "/"
        best: tuple[str, str] | None = None
        for src, dst in self.entries:
            s_low = src.lower()
            if (low.startswith(s_low) or slashed == s_low) and (
                best is None or len(src) > len(best[0])
            ):
                best = (src, dst)
        if best is None:
            return None
        return best[0], best[1], p[len(best[0]) :]

    def _char_map_for(self, prefix: str) -> CharMap | None:
        best: tuple[str, str] | None = None
        for pfx, spec in self.char_maps:
            if prefix.lower().startswith(pfx.lower()) and (best is None or len(pfx) > len(best[0])):
                best = (pfx, spec)
        if best is None:
            return None
        cm = CharMap(best[1])
        return cm if cm.active else None

    def _find(self, path: str) -> str:
        """The spelling of ``path`` that exists here (NFC / NFD / per directory, see
        :func:`~maktaba_pipeline.engine.paths.find_existing`); ``path`` when none."""
        if self.exists is _exists:
            return find_existing(path)
        if path.isascii() or self.exists(path):
            return path
        for form in ("NFC", "NFD"):
            cand = unicodedata.normalize(form, path)
            if self.exists(cand):
                return cand
        return path

    def to_local(self, db_path: str) -> str:
        sp = parse_canonical(db_path)
        if sp is not None and sp.scheme == "file":
            return self._find(sp.rel) if self.probe else sp.rel
        hit = self._forward(db_path)
        if hit is None:
            return self._find(db_path) if self.probe and sp is None else db_path
        src, dst, rest = hit
        if sp is None:
            out = dst + rest if rest else _root(dst)
            return self._find(out) if self.probe else out
        cm = self._char_map_for(src)
        primary_rest = cm.to_client(rest) if cm else rest
        primary = dst + primary_rest if primary_rest else _root(dst)
        if not self.probe:
            return primary
        plain = rest.isascii() and cm is None and not any(c in FULLWIDTH for c in rest)
        if plain:
            return primary
        found = self._find(primary)
        if found != primary or self.exists(primary):
            return found
        for alt in self._variants(rest, cm):
            cand = self._find(dst + alt)
            if cand != primary and self.exists(cand):
                return cand
        return primary

    @staticmethod
    def _variants(rest: str, cm: CharMap | None) -> list[str]:
        """The other sides of a character mapping (declared, else the known tables)."""
        if cm is not None:
            return [cm.to_disk(rest)]
        return [CharMap(spec).to_client(rest) for spec in ("fullwidth", "sfm")]

    # -- local -> canonical -------------------------------------------------------

    def canonical(self, local_path: str) -> str | None:
        """The canonical path of a local path under one of the shares, else ``None``."""
        p = local_path.replace("\\", "/")
        low = p.lower()
        best: ShareMount | None = None
        for sm in self.shares:
            loc = sm.local.lower()
            if (low.startswith(loc) or low + "/" == loc) and (
                best is None or len(sm.local) > len(best.local)
            ):
                best = sm
        if best is None:
            return None
        rest = p[len(best.local) :] if len(p) >= len(best.local) else ""
        cm = CharMap(best.char_map)
        rest = cm.to_disk(rest) if cm.active else rest
        body = normalise_rel(rest)
        base = best.prefix.rstrip("/")
        return f"{base}/{body}" if body else base

    def to_db(self, local_path: str) -> str:
        """Canonical when the path is on a known share; else the legacy map's form."""
        c = self.canonical(local_path)
        if c is not None:
            return c
        return PathMap.to_db(self, local_path)

    def share_list(self) -> list[dict[str, str]]:
        return [s.as_json() for s in self.shares]


def build_resolver(
    *,
    servers: Sequence[StorageServer] = (),
    mounts: Iterable[Mount] = (),
    system: str,
    explicit: PathMap | Mapping[str, str] | None = None,
    admin: Mapping[str, str] | None = None,
    legacy: PathMap | Mapping[str, str] | None = None,
    resolve: Resolve | None = None,
    probe: bool = True,
    exists: Callable[[str], bool] | None = None,
) -> Resolver:
    """Merge the sources (see the module docstring) into one resolver."""
    shares = share_mounts(servers, mounts, system=system, resolve=resolve)
    merged: dict[str, str] = {}

    def put(src: str, dst: str) -> None:
        k = _canon_key(src)
        for existing in [x for x in merged if x.lower() == k.lower()]:
            del merged[existing]
        merged[k] = _norm(dst)

    def as_pairs(m: PathMap | Mapping[str, str] | None) -> list[tuple[str, str]]:
        if m is None:
            return []
        if isinstance(m, PathMap):
            return list(m.entries)
        return [(str(a), str(b)) for a, b in m.items()]

    # lowest precedence first; each later source overrides an equal key
    for a, b in as_pairs(legacy):
        put(a, b)
    for sm in shares:
        if sm.protocol == "unc":
            put(sm.prefix, sm.local)
    seen: set[str] = set()
    for sm in shares:
        # the first mount of a share wins (a drive letter before a ``net use`` line)
        if sm.protocol != "unc" and sm.prefix.lower() not in seen:
            seen.add(sm.prefix.lower())
            put(sm.prefix, sm.local)
    for a, b in as_pairs(admin):
        put(a, b)
    for a, b in as_pairs(explicit):
        put(a, b)
    admin_shares = [
        ShareMount(prefix=k, local=v, protocol="admin", server="", source="admin")
        for k, v in merged.items()
        if parse_canonical(k.rstrip("/")) is not None
        and not any(s.prefix.lower() == k.lower() and s.local.lower() == v.lower() for s in shares)
    ]
    char_maps = tuple(
        (s.prefix, s.char_map)
        for s in shares
        if s.protocol in ("smb", "unc") and s.char_map != "none"
    )
    by_server = {s.name.lower(): s.char_map for s in servers if s.char_map != "none"}
    for s in admin_shares:
        sp = parse_canonical(s.prefix.rstrip("/"))
        if sp is not None and sp.server in by_server:
            char_maps += ((s.prefix, by_server[sp.server]),)
    return Resolver(
        entries=tuple(sorted(merged.items())),
        shares=tuple(shares) + tuple(admin_shares),
        char_maps=char_maps,
        probe=probe,
        exists=exists or _exists,
    )
