"""LAN discovery: service advertisement payload + outbound prober.

The prober speaks HTTP (not mDNS) so it works in container / VPN setups
where multicast is filtered. It expects every Maktaba API to expose
``GET /api/system/version`` returning ``{"version":..., "schema_rev":...}``.
"""

from __future__ import annotations

import asyncio
import dataclasses
import ipaddress
import json
import socket
import urllib.error
import urllib.request
from typing import Iterable


SERVICE_TYPE = "_maktaba._tcp"
"""DNS-SD service type Maktaba registers under."""


@dataclasses.dataclass(frozen=True)
class ServiceAdvertisement:
    """The payload an mDNS publisher would broadcast."""

    instance: str
    port: int
    txt: dict[str, str] = dataclasses.field(default_factory=dict)
    domain: str = "local."

    def fqdn(self) -> str:
        return f"{self.instance}.{SERVICE_TYPE}.{self.domain}"

    def txt_record_bytes(self) -> list[bytes]:
        """Encode each TXT pair as ``key=value`` byte strings ≤ 255 bytes."""
        out: list[bytes] = []
        for k, v in self.txt.items():
            line = f"{k}={v}".encode("utf-8")
            if len(line) > 255:
                raise ValueError(f"TXT record {k} exceeds 255 bytes")
            out.append(line)
        return out


@dataclasses.dataclass(frozen=True)
class ProbeResult:
    """One responder on the LAN."""

    host: str
    port: int
    schema_rev: int
    version: str


class LANProbe:
    """Cheap LAN probe.

    Iterates ``hosts`` (typically the local /24 broadcast range), issues
    ``GET http://<host>:<port>/api/system/version`` against each, and
    yields the first ``max_results`` successes. Failures are swallowed.
    Designed for use from the desktop installer's "find my server" UI.
    """

    def __init__(
        self,
        port: int = 8080,
        timeout_sec: float = 0.25,
        max_results: int = 8,
    ) -> None:
        self.port = port
        self.timeout_sec = timeout_sec
        self.max_results = max_results

    async def probe(self, hosts: Iterable[str]) -> list[ProbeResult]:
        sem = asyncio.Semaphore(64)

        async def one(h: str) -> ProbeResult | None:
            async with sem:
                return await asyncio.get_running_loop().run_in_executor(
                    None, self._fetch, h
                )

        results: list[ProbeResult] = []
        tasks = [asyncio.create_task(one(h)) for h in hosts]
        for fut in asyncio.as_completed(tasks):
            r = await fut
            if r is not None:
                results.append(r)
                if len(results) >= self.max_results:
                    break
        for t in tasks:
            t.cancel()
        return results

    def _fetch(self, host: str) -> ProbeResult | None:
        url = f"http://{host}:{self.port}/api/system/version"
        try:
            with urllib.request.urlopen(url, timeout=self.timeout_sec) as resp:
                body = resp.read()
            payload = json.loads(body)
        except (urllib.error.URLError, OSError, json.JSONDecodeError):
            return None
        try:
            return ProbeResult(
                host=host,
                port=self.port,
                schema_rev=int(payload.get("schema_rev", 0)),
                version=str(payload.get("version", "")),
            )
        except (TypeError, ValueError):
            return None


def local_subnet_hosts(cidr: str = "192.168.1.0/24") -> list[str]:
    """Enumerate every host in a /24 except network + broadcast."""

    net = ipaddress.ip_network(cidr, strict=False)
    return [str(ip) for ip in net.hosts()]


def primary_iface_ip() -> str | None:
    """Best-effort current outbound IP. Returns None if offline."""

    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.connect(("8.8.8.8", 80))
            return s.getsockname()[0]
    except OSError:
        return None
