"""Find the home on the LAN (Epic 31 design section 2).

The home advertises ``_maktaba._tcp`` over mDNS with TXT ``protocol``, ``role=home``,
``id`` (first 8 hex of its instance id), ``version``, ``schema_rev`` and ``join``
(``1`` while it accepts new machines). Its service instance name is
``Maktaba (<name>)``. A node browses for a few seconds (pure-Python ``zeroconf``: macOS,
Linux, Windows); when nothing answers (multicast blocked) it falls back to the HTTP
sweep of :mod:`maktaba_pipeline.discovery.probe`; ``--home URL`` skips both.
"""

from __future__ import annotations

import asyncio
import ipaddress
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Any

from ..discovery.probe import LANProbe, local_subnet_hosts, primary_iface_ip
from ..log import get_logger

__all__ = [
    "SERVICE_TYPE",
    "Home",
    "browse_mdns",
    "discover_homes",
    "home_from_service",
    "home_from_url",
    "probe_lan",
]

SERVICE_TYPE = "_maktaba._tcp.local."
_log = get_logger()


@dataclass(frozen=True, slots=True)
class Home:
    url: str
    instance_id: str = ""
    name: str = ""
    version: str = ""
    schema_rev: int = 0
    #: True / False from the TXT record; None when unknown (probe fallback, --home)
    join: bool | None = None

    def label(self) -> str:
        bits = [self.name or self.url]
        if self.name:
            bits.append(self.url)
        if self.version:
            bits.append(f"v{self.version}")
        if self.join is False:
            bits.append("not accepting new machines")
        return " - ".join(bits)


def _txt(props: Mapping[Any, Any]) -> dict[str, str]:
    out: dict[str, str] = {}
    for k, v in props.items():
        key = k.decode("utf-8", "replace") if isinstance(k, bytes) else str(k)
        if v is None:
            val = ""
        elif isinstance(v, bytes):
            val = v.decode("utf-8", "replace")
        else:
            val = str(v)
        out[key] = val
    return out


def _instance_name(service_name: str) -> str:
    """``Maktaba (home-mac)._maktaba._tcp.local.`` -> ``home-mac``."""
    inst = service_name
    suffix = "." + SERVICE_TYPE
    if inst.endswith(suffix):
        inst = inst[: -len(suffix)]
    if inst.startswith("Maktaba (") and inst.endswith(")"):
        return inst[len("Maktaba (") : -1]
    return inst


def _url(host: str, port: int) -> str:
    try:
        if ipaddress.ip_address(host).version == 6:
            host = f"[{host}]"
    except ValueError:
        pass
    return f"http://{host}:{port}"


def home_from_service(
    service_name: str, properties: Mapping[Any, Any], addresses: list[str], port: int
) -> Home | None:
    """A Home from one resolved mDNS service; None when it is not a home."""
    txt = _txt(properties)
    if txt.get("role", "home") != "home" or not addresses:
        return None
    # prefer an IPv4 address: what the database credentials will point at
    addr = sorted(addresses, key=lambda a: (":" in a, a))[0]
    try:
        schema_rev = int(txt.get("schema_rev") or 0)
    except ValueError:
        schema_rev = 0
    join_raw = txt.get("join")
    return Home(
        url=_url(addr, port),
        instance_id=txt.get("id", ""),
        name=_instance_name(service_name),
        version=txt.get("version", ""),
        schema_rev=schema_rev,
        join=None if join_raw is None else join_raw == "1",
    )


def home_from_url(url: str) -> Home:
    url = url.strip().rstrip("/")
    if "://" not in url:
        url = "http://" + url
    return Home(url=url)


def browse_mdns(timeout: float = 3.0) -> list[Home]:
    """Browse ``_maktaba._tcp`` for ``timeout`` seconds."""
    from zeroconf import ServiceBrowser, ServiceListener, Zeroconf

    found: dict[str, Home] = {}
    names: set[str] = set()

    class _Listener(ServiceListener):
        def add_service(self, zc: Zeroconf, type_: str, name: str) -> None:
            names.add(name)

        def update_service(self, zc: Zeroconf, type_: str, name: str) -> None:
            names.add(name)

        def remove_service(self, zc: Zeroconf, type_: str, name: str) -> None:
            names.discard(name)

    zc = Zeroconf()
    try:
        browser = ServiceBrowser(zc, SERVICE_TYPE, _Listener())
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            time.sleep(0.1)
        browser.cancel()
        for name in sorted(names):
            info = zc.get_service_info(SERVICE_TYPE, name, timeout=1500)
            if info is None or info.port is None:
                continue
            home = home_from_service(name, info.properties, info.parsed_addresses(), info.port)
            if home is not None:
                found[home.url] = home
    finally:
        zc.close()
    return list(found.values())


def probe_lan(port: int = 8080) -> list[Home]:
    """The HTTP sweep of this machine's /24 (``GET /api/system/version``)."""
    ip = primary_iface_ip()
    if ip is None:
        return []
    hosts = local_subnet_hosts(f"{ip}/24")
    results = asyncio.run(LANProbe(port=port).probe(hosts))
    return [
        Home(url=_url(r.host, r.port), version=r.version, schema_rev=r.schema_rev) for r in results
    ]


def discover_homes(
    home_url: str | None = None,
    *,
    timeout: float = 3.0,
    browse: Callable[[float], list[Home]] = browse_mdns,
    probe: Callable[[], list[Home]] = probe_lan,
) -> list[Home]:
    """``--home`` when given; else mDNS; else the subnet sweep."""
    if home_url:
        return [home_from_url(home_url)]
    try:
        homes = browse(timeout)
    except Exception as exc:  # noqa: BLE001 - no multicast here: fall back to the sweep
        _log.warning("cluster_mdns_browse_failed", error=str(exc))
        homes = []
    if homes:
        return sorted(homes, key=lambda h: (h.join is False, h.name, h.url))
    _log.info("cluster_mdns_nothing_found_probing_subnet")
    try:
        return probe()
    except Exception as exc:  # noqa: BLE001
        _log.warning("cluster_lan_probe_failed", error=str(exc))
        return []
