"""Storage servers registered from the home's own mounts (story 31.3, automatic).

The home mounts its NAS (``//192.168.50.94/Originals on /Volumes/Originals``); every
network server it mounts that no ``storage_servers`` record names becomes one:

* **grouped by address**: two mounts whose hosts resolve to a common IP
  (``192.168.50.94`` and ``Home-NAS.local``) are one server;
* **named** from the host's DNS name (a host name the mount uses, else the reverse
  DNS name of its IP), else its NetBIOS name (``smbutil status`` on macOS,
  ``nmblookup -A`` on Linux, ``nbtstat -A`` on Windows), else ``nas``; lower case, the
  first label (``Home-NAS.lan`` -> ``home-nas``), made unique among the records;
* **addresses**: every host the mounts use, every name found for it and every IP it
  resolves to, so a node that mounts it by another name or address matches it.

An existing record is never changed (an admin's edits stand), and a host this did
register once whose record the admin then removed is not registered again.
"""

from __future__ import annotations

import ipaddress
import platform
import re
import socket
import subprocess
from collections.abc import Callable, Iterable, Mapping, MutableMapping, Sequence
from dataclasses import dataclass, field

from ..log import get_logger
from .mounts import Mount
from .resolver import StorageServer, _resolve_ips, match_server
from .servers import save_server

__all__ = [
    "Lookups",
    "netbios_name",
    "parse_nbtstat",
    "parse_nmblookup",
    "parse_smbutil_status",
    "plan_servers",
    "register_from_mounts",
    "reverse_names",
]

_log = get_logger()

Resolve = Callable[[str], frozenset[str]]


def _is_ip(host: str) -> bool:
    try:
        ipaddress.ip_address(host.strip("[]"))
    except ValueError:
        return False
    return True


# -- NetBIOS names (the name a Windows network shows; what Synology answers) ---------


def parse_smbutil_status(text: str) -> str | None:
    """macOS ``smbutil status <ip>``: ``Workgroup: WORKGROUP`` / ``Server: HOME-NAS``."""
    m = re.search(r"^\s*Server:\s*(\S+)\s*$", text, re.M)
    return m.group(1) if m else None


def parse_nmblookup(text: str) -> str | None:
    """Linux ``nmblookup -A <ip>``: the first unique ``<00>`` name that is not a group."""
    for line in text.splitlines():
        m = re.match(r"^\s+(\S+)\s+<00>\s+-\s+(<GROUP>\s+)?", line)
        if m and not m.group(2):
            return m.group(1)
    return None


def parse_nbtstat(text: str) -> str | None:
    """Windows ``nbtstat -A <ip>``: the first ``<00>  UNIQUE`` name."""
    for line in text.splitlines():
        m = re.match(r"^\s+(\S+)\s+<00>\s+UNIQUE\b", line, re.I)
        if m:
            return m.group(1)
    return None


def _run(cmd: list[str]) -> str:
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=8, check=False).stdout
    except (OSError, subprocess.SubprocessError):
        return ""


def netbios_name(ip: str, system: str | None = None) -> str | None:
    """The NetBIOS name of the machine at ``ip``, ``None`` when nothing answers."""
    system = system or platform.system()
    if system == "Darwin":
        return parse_smbutil_status(_run(["smbutil", "status", ip]))
    if system == "Windows":
        return parse_nbtstat(_run(["nbtstat", "-A", ip]))
    return parse_nmblookup(_run(["nmblookup", "-A", ip]))


def reverse_names(ip: str) -> list[str]:
    """The reverse DNS name and aliases of ``ip`` (none when it has no PTR record)."""
    try:
        name, aliases, _ = socket.gethostbyaddr(ip)
    except (OSError, UnicodeError):
        return []
    return [n for n in (name, *aliases) if n and not _is_ip(n)]


@dataclass(frozen=True, slots=True)
class Lookups:
    """How names and addresses are found (tests pass their own)."""

    resolve: Resolve = _resolve_ips
    reverse: Callable[[str], list[str]] = reverse_names
    netbios: Callable[[str], str | None] = netbios_name


# -- grouping and naming ---------------------------------------------------------------


@dataclass
class _Group:
    hosts: list[str] = field(default_factory=list)
    ips: set[str] = field(default_factory=set)


def _short(name: str) -> str:
    n = name.strip().lower().rstrip(".")
    n = re.sub(r"\.local$", "", n)
    return n.split(".", 1)[0]


def _slug(name: str) -> str:
    s = re.sub(r"[^a-z0-9-]+", "-", name.lower()).strip("-")
    return s or "nas"


def _unique(base: str, taken: set[str]) -> str:
    name, n = base, 2
    while name in taken:
        name, n = f"{base}-{n}", n + 1
    return name


def _add(out: list[str], value: str) -> None:
    v = value.strip().rstrip(".")
    if v and v.lower() not in {x.lower() for x in out}:
        out.append(v)


def plan_servers(
    mounts: Iterable[Mount],
    servers: Sequence[StorageServer],
    lookups: Lookups,
    *,
    registered: Mapping[str, str] | None = None,
) -> tuple[list[StorageServer], list[str]]:
    """The records to add for the mounts no server names, and the hosts left out
    because the admin removed the record this registered for them before."""
    registered = registered or {}
    names = {s.name for s in servers}
    groups: list[_Group] = []
    skipped: list[str] = []
    for m in mounts:
        host = m.host.strip().lower()
        if not host or match_server(servers, host, resolve=lookups.resolve) is not None:
            continue
        if host in registered:
            skipped.append(host)  # the admin removed the record: theirs to add again
            continue
        ips = set(lookups.resolve(host)) | ({host.strip("[]")} if _is_ip(host) else set())
        hit = next((g for g in groups if host in g.hosts or (ips and g.ips & ips)), None)
        if hit is None:
            groups.append(_Group([host], ips))
        else:
            if host not in hit.hosts:
                hit.hosts.append(host)
            hit.ips |= ips
    out: list[StorageServer] = []
    for g in groups:
        dns: list[str] = [h for h in g.hosts if not _is_ip(h)]
        for ip in sorted(g.ips):
            for n in lookups.reverse(ip):
                _add(dns, n)
        nb: str | None = None
        if not dns:
            for ip in sorted(g.ips):
                nb = lookups.netbios(ip)
                if nb:
                    break
        base = _slug(_short(dns[0])) if dns else _slug(nb) if nb else "nas"
        name = _unique(base, names)
        names.add(name)
        addresses: list[str] = []
        for h in g.hosts:
            _add(addresses, h)
        for n in dns:
            _add(addresses, n)
            if _short(n) != name:
                _add(addresses, _short(n))
        if nb:
            _add(addresses, nb)
        for ip in sorted(g.ips):
            _add(addresses, ip)
        out.append(StorageServer(name=name, addresses=tuple(addresses)))
    return out, skipped


async def register_from_mounts(
    db: object,
    mounts: Iterable[Mount],
    servers: Sequence[StorageServer],
    lookups: Lookups | None = None,
    *,
    registered: MutableMapping[str, str] | None = None,
) -> tuple[list[StorageServer], list[str]]:
    """Insert a record for every server the mounts reach that none names; the records
    added and the hosts skipped. ``registered`` (host -> name) is kept up to date."""
    mounts = list(mounts)
    planned, skipped = plan_servers(
        mounts, servers, lookups or Lookups(), registered=registered or {}
    )
    added: list[StorageServer] = []
    for s in planned:
        saved = await save_server(db, s)
        added.append(saved)
        _log.info("storage_server_registered", name=saved.name, addresses=list(saved.addresses))
    if registered is not None:
        all_servers = [*servers, *added]
        for m in mounts:
            hit = match_server(all_servers, m.host, resolve=(lookups or Lookups()).resolve)
            if hit is not None and hit in added:
                registered[m.host.strip().lower()] = hit.name
    return added, skipped
