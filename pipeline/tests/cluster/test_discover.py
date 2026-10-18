"""Finding the home: TXT parsing, --home, the fallback order."""

from __future__ import annotations

import pytest

from maktaba_pipeline.cluster.discover import (
    Home,
    discover_homes,
    home_from_service,
    home_from_url,
)

pytestmark = pytest.mark.unit

NAME = "Maktaba (home-mac)._maktaba._tcp.local."
TXT = {
    b"protocol": b"1",
    b"role": b"home",
    b"id": b"0b9a8c7d",
    b"version": b"1.4.0",
    b"schema_rev": b"103",
    b"join": b"1",
}


def test_a_home_from_its_txt_record() -> None:
    home = home_from_service(NAME, TXT, ["fe80::1", "192.168.1.20"], 8080)
    assert home == Home(
        url="http://192.168.1.20:8080",
        instance_id="0b9a8c7d",
        name="home-mac",
        version="1.4.0",
        schema_rev=103,
        join=True,
        urls=("http://192.168.1.20:8080", "http://[fe80::1]:8080"),
    )
    closed = home_from_service(NAME, {**TXT, b"join": b"0"}, ["192.168.1.20"], 8080)
    assert closed is not None and closed.join is False
    assert "not accepting" in closed.label()


def test_not_a_home_or_no_address() -> None:
    assert home_from_service(NAME, {**TXT, b"role": b"node"}, ["10.0.0.2"], 8080) is None
    assert home_from_service(NAME, TXT, [], 8080) is None
    v6 = home_from_service(NAME, {b"schema_rev": b"x"}, ["fe80::1"], 8080)
    assert v6 is not None and v6.url == "http://[fe80::1]:8080" and v6.schema_rev == 0
    assert v6.join is None


def test_home_url_skips_discovery() -> None:
    def boom(_: float) -> list[Home]:
        raise AssertionError("browsed")

    assert discover_homes("192.168.1.20:8080/", browse=boom) == [
        Home(url="http://192.168.1.20:8080")
    ]
    assert home_from_url("https://h:1") == Home(url="https://h:1")


def test_mdns_first_then_the_subnet_probe() -> None:
    a = Home(url="http://a:1", name="b-home", join=False)
    b = Home(url="http://b:1", name="a-home", join=True)
    probed: list[int] = []

    def probe() -> list[Home]:
        probed.append(1)
        return [Home(url="http://c:8080")]

    # open homes first
    assert discover_homes(browse=lambda t: [a, b], probe=probe) == [b, a]
    assert probed == []
    assert discover_homes(browse=lambda t: [], probe=probe) == [Home(url="http://c:8080")]

    def no_multicast(_: float) -> list[Home]:
        raise OSError("no multicast")

    assert discover_homes(browse=no_multicast, probe=probe) == [Home(url="http://c:8080")]
    assert len(probed) == 2


def test_the_home_s_lan_address_wins_over_its_vpn_one() -> None:
    import ipaddress

    from maktaba_pipeline.cluster.discover import rank_addresses

    lan = [ipaddress.ip_network("192.168.50.0/24")]
    got = rank_addresses(
        ["100.75.172.127", "fe80::1", "127.0.0.1", "10.0.0.5", "192.168.50.177", "8.8.8.8"], lan
    )
    # its own subnet, private, public, Tailscale (CGNAT), link-local, loopback
    want = ["192.168.50.177", "10.0.0.5", "8.8.8.8", "100.75.172.127", "fe80::1", "127.0.0.1"]
    assert got == want
    # without a shared subnet a private LAN address still beats the VPN one
    assert rank_addresses(["100.75.172.127", "192.168.1.20"], [])[0] == "192.168.1.20"


def test_the_first_address_that_answers_is_used() -> None:
    from maktaba_pipeline.cluster.discover import Home, _reachable

    home = Home(url="http://192.168.1.20:8080",
                urls=("http://192.168.1.20:8080", "http://100.75.0.9:8080"))  # fmt: skip
    asked: list[str] = []

    def answers(url: str) -> bool:
        asked.append(url)
        return "100.75" in url  # the LAN address is filtered on this network

    assert _reachable(home, answers).url == "http://100.75.0.9:8080"
    assert asked == list(home.urls)
    assert _reachable(home, lambda u: False).url == home.url  # none answer: keep the best
