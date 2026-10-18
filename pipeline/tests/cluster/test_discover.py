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
