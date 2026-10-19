"""The extension relay over the agent channel (story 31.R), against the fake hub: a node's
Plex client sends method + path only, never a token; the hub's answer is reassembled;
refusals, retries and a channel that goes away."""

from __future__ import annotations

import asyncio
import base64
import json
from typing import Any

import pytest

from maktaba_pipeline import relay
from maktaba_pipeline.integrations.plex import PlexClient, PlexError, TransportError
from tests.cluster.agent.fakes import HubSession, running_hub
from tests.cluster.agent.test_channel import make_agent, node_hub, running, until

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]

SECTIONS = {
    "MediaContainer": {
        "Directory": [{"key": "1", "title": "TV", "type": "show", "Location": [{"path": "/x"}]}]
    },
    "pad": "p" * 5000,
}


async def test_a_relayed_plex_call_carries_no_token_and_is_reassembled() -> None:
    hub, key = node_hub()
    limited: dict[str, int] = {}

    async def answer(s: HubSession, f: dict[str, Any]) -> None:
        if f.get("t") != "svc.request":
            return
        rid, path = f["id"], f["path"]
        if path == "/library/sections":
            body = json.dumps(SECTIONS).encode()
            for i in range(0, len(body), 1000):  # several chunks
                chunk = base64.b64encode(body[i : i + 1000]).decode()
                await s.send({"t": "svc.chunk", "id": rid, "data": chunk})
            await s.send({"t": "svc.done", "id": rid, "status": 200,
                          "content_type": "application/json", "bytes": len(body)})  # fmt: skip
        elif path == "/identity":
            n = limited.get(path, 0)
            limited[path] = n + 1
            if n < 2:  # the home's per-machine limit: tried again after a pause
                await s.send({"t": "svc.done", "id": rid, "status": 0,
                              "error": {"code": "rate_limited", "detail": "x"}})  # fmt: skip
                return
            await s.send({"t": "svc.chunk", "id": rid,
                          "data": base64.b64encode(b'{"MediaContainer":{}}').decode()})  # fmt: skip
            await s.send({"t": "svc.done", "id": rid, "status": 200})
        elif path == "/library/metadata/5":
            await s.ws.close()  # the channel goes away mid-call
        else:
            await s.send({"t": "svc.done", "id": rid, "status": 0,
                          "error": {"code": "not_allowed", "detail": path}})  # fmt: skip

    hub.on_frame = answer
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key)
        loop = asyncio.get_running_loop()
        transport = relay.RelayTransport(
            "plex", channel=agent, loop=loop, connect_wait=3.0, delays=(0.01, 0.01, 0.01)
        )
        client = PlexClient(relay.RELAY_BASE + "plex", "", transport=transport)
        async with running(agent):
            s = await hub.wait_session(1)
            await until(lambda: agent.connected)

            sections = await asyncio.to_thread(client.sections)
            assert [(x.key, x.type) for x in sections] == [("1", "show")]
            sent = await s.next("svc.request")
            assert sent["ext"] == "plex" and sent["method"] == "GET"
            assert sent["path"] == "/library/sections" and sent["accept"] == "application/json"
            assert "token" not in json.dumps(sent).lower()  # nothing secret leaves the node
            assert "relay://" not in json.dumps(sent)  # a path, never a URL

            await asyncio.to_thread(client.ping)  # two refusals, then through
            assert limited["/identity"] == 3

            with pytest.raises(PlexError) as e:  # refused by the home: Plex's "forbidden"
                await asyncio.to_thread(client.get, "/:/prefs")
            assert e.value.status == 403

            with pytest.raises(TransportError):  # the channel closed mid-call
                await asyncio.to_thread(client.get, "/library/metadata/5")


async def test_without_a_channel_the_call_fails_retryably() -> None:
    hub, key = node_hub()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key)
        transport = relay.RelayTransport(
            "plex", channel=agent, loop=asyncio.get_running_loop(), connect_wait=0.2
        )
        client = PlexClient(relay.RELAY_BASE + "plex", "", transport=transport)
        with pytest.raises(TransportError, match="not connected"):
            await asyncio.to_thread(client.ping)
    detached = PlexClient(relay.RELAY_BASE + "plex", "", transport=relay.RelayTransport("plex"))
    relay.detach()
    with pytest.raises(TransportError, match="no channel"):
        await asyncio.to_thread(detached.ping)
