import asyncio

import pytest

from maktaba_pipeline.discovery import LANProbe, ServiceAdvertisement
from maktaba_pipeline.discovery.probe import local_subnet_hosts


def test_advertisement_fqdn() -> None:
    ad = ServiceAdvertisement(instance="Maktaba @ test", port=8080)
    assert ad.fqdn() == "Maktaba @ test._maktaba._tcp.local."


def test_advertisement_txt_encoding() -> None:
    ad = ServiceAdvertisement(
        instance="t",
        port=1,
        txt={"version": "1.0", "schema_rev": "53"},
    )
    out = ad.txt_record_bytes()
    assert b"version=1.0" in out
    assert b"schema_rev=53" in out
    for line in out:
        assert len(line) <= 255


def test_txt_record_rejects_overlong_value() -> None:
    long = "x" * 260
    ad = ServiceAdvertisement(instance="t", port=1, txt={"k": long})
    with pytest.raises(ValueError):
        ad.txt_record_bytes()


def test_local_subnet_hosts_size() -> None:
    hosts = local_subnet_hosts("10.0.0.0/24")
    assert len(hosts) == 254
    assert "10.0.0.1" in hosts
    assert "10.0.0.0" not in hosts
    assert "10.0.0.255" not in hosts


def test_lan_probe_no_responders() -> None:
    """A probe against link-local that nobody answers returns []."""
    probe = LANProbe(port=1, timeout_sec=0.05, max_results=4)
    results = asyncio.run(probe.probe(["169.254.42.1", "169.254.42.2"]))
    assert results == []
