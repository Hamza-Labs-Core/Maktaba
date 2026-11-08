"""A first-party plugin reaches Plex only through the home's service door (Epic 54 wave 8,
story 54.35): what its job gets of the settings, the way its calls go on the home and on a
joined machine, and what a refusal looks like to its Plex client."""

from __future__ import annotations

import asyncio
import base64
from types import SimpleNamespace
from typing import Any

import pytest

from maktaba_pipeline.plugins import native, service

SETTINGS = {
    "plex": {"url": "http://192.168.1.4:32400", "token": "SECRET"},
    "cloudflare_stream": {"account_id": "acct"},
}


def test_a_plugins_job_gets_no_secret_and_no_service_address() -> None:
    got = service.plugin_settings(SETTINGS, ("plex",))
    assert got["plex"] == {"url": "service:plex"}
    assert got["cloudflare_stream"] == {"account_id": "acct"}  # not a service it reaches
    assert "SECRET" not in repr(got)


def test_the_route_follows_where_the_worker_runs(monkeypatch: pytest.MonkeyPatch) -> None:
    ctx = SimpleNamespace(settings=SETTINGS)
    # on the home the worker has the token: the plugin API's door
    frame = native._service_frame("plex_livetv", ctx)
    assert frame["service_via"] == "api" and frame["extension_settings"]["plex"]["url"] == (
        "service:plex"
    )
    # a joined machine has no token and an agent: its worker's channel to the home
    from maktaba_pipeline import relay

    monkeypatch.setattr(relay, "node_may_relay", lambda: True)
    node = SimpleNamespace(settings={"plex": {"url": "http://192.168.1.4:32400"}})
    assert native._service_frame("plex_livetv", node)["service_via"] == "worker"
    # a stage that reaches no service gets nothing (no settings, no work per job)
    assert native._service_frame("tv_gc", ctx) == {}


@pytest.mark.asyncio
async def test_a_node_asks_its_worker_which_relays_to_the_home(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    from maktaba_pipeline import relay

    seen: list[tuple[str, str, Any]] = []

    class Relayed:
        def __init__(self, ext: str) -> None:
            self.ext = ext

        def request(
            self, method: str, url: str, headers: Any, body: Any, timeout: float
        ) -> tuple[int, bytes]:
            seen.append((method, url, body))
            return 200, b'{"MediaContainer":{}}'

    monkeypatch.setattr(relay, "RelayTransport", Relayed)
    ctx = SimpleNamespace(job=SimpleNamespace(stage="plex_livetv"))
    got = await native._relayed_service(
        ctx, {"service": "plex", "method": "GET", "target": "/livetv/dvrs", "body": ""}
    )
    assert got["status"] == 200 and base64.b64decode(got["body"]) == b'{"MediaContainer":{}}'
    assert seen == [("GET", "relay://plex/livetv/dvrs", None)]
    with pytest.raises(native.EnqueueRefused, match="plugin_capability"):
        await native._relayed_service(
            ctx, {"service": "cloudflare_stream", "method": "GET", "target": "/"}
        )
    with pytest.raises(native.EnqueueRefused, match="plugin_capability"):
        await native._relayed_service(
            SimpleNamespace(job=SimpleNamespace(stage="tv_gc")),
            {"service": "plex", "method": "GET", "target": "/"},
        )


class _Refusing:
    def __init__(self, code: str) -> None:
        self.code = code

    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
        from maktaba_plugin_sdk.native.stmtdb import PluginRefused

        raise PluginRefused(403, self.code, "GET /livetv/sessions: not on the allow-list")


@pytest.mark.asyncio
async def test_off_the_allow_list_is_plexs_forbidden_other_refusals_are_errors() -> None:
    from maktaba_pipeline.integrations.plex import TransportError

    t = service.ServiceTransport("plex")
    db = SimpleNamespace(transport=_Refusing("plugin_service_path"))
    with service.bind(db, "api"):
        got = await asyncio.to_thread(
            t.request, "GET", "service://plex/livetv/sessions", {}, None, 5.0
        )
    assert got == (403, b"")
    db = SimpleNamespace(transport=_Refusing("plugin_capability"))
    with service.bind(db, "api"), pytest.raises(TransportError, match="plugin_capability"):
        await asyncio.to_thread(t.request, "GET", "service://plex/identity", {}, None, 5.0)


def test_unbound_the_client_is_the_workers_own() -> None:
    c = service.plex_client({"url": "http://192.168.1.4:32400", "token": "SECRET"})
    assert c.uri.startswith("http://192.168.1.4") and c.token == "SECRET"
