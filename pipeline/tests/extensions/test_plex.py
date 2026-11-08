"""The Plex extension: only Live TV (the Plex library sync is gone); its health probe and
its manifest."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.extensions.plex import PlexExtension, health_probes
from maktaba_pipeline.extensions.plex import extension as default_extension
from maktaba_pipeline.integrations.plex import PlexClient
from tests.extensions.harness import FakePlex

TOKEN = "SECRET-PLEX-TOKEN"
SETTINGS = {"plex": {"url": "http://plex.test", "token": TOKEN}}


def make_extension(fake: FakePlex) -> PlexExtension:
    return PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))


@pytest.mark.asyncio
async def test_health_probe_reports_ms_or_minus_one() -> None:
    fake = FakePlex()
    ext = make_extension(fake)
    probes = ext.health_probes(SETTINGS["plex"])
    assert set(probes) == {"net.plex_ms"}
    assert await probes["net.plex_ms"]() >= 0
    fake.down = True
    assert await probes["net.plex_ms"]() == -1
    assert ext.health_probes({"url": "http://x"}) == {}  # not configured: no probes
    assert health_probes({}) == {}


def test_manifest_and_configuration() -> None:
    m = default_extension.manifest
    assert m.name == "plex" and m.secret_keys() == {"token"}
    assert m.stages == ("plex_livetv",)
    assert {f.key: f.env for f in m.settings} == {
        "url": "MAKTABA_PLEX_URL",
        "token": "MAKTABA_PLEX_TOKEN",
    }
    assert default_extension.is_configured({"url": "u", "token": "t"})
    assert not default_extension.is_configured({"url": "u"})
    assert not default_extension.is_configured({"token": "t"})
    assert set(default_extension.impls()) == {"plex_livetv"}
    assert asyncio.iscoroutinefunction(default_extension.impls()["plex_livetv"].run)
