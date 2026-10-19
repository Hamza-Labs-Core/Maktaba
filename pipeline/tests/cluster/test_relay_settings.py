"""The extension relay's settings and placement (story 31.R): which client a stage gets,
and which home-only stages a node may take while the relay is on."""

from __future__ import annotations

from collections.abc import Coroutine
from typing import Any, cast

import pytest

from maktaba_pipeline import relay, rules
from maktaba_pipeline.cluster.stages import lifted_placements
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions import discover
from maktaba_pipeline.integrations.plex import UrllibTransport

pytestmark = pytest.mark.unit

PLEX = {"plex_index", "plex_collection", "plex_groups"}


class _Db:
    """app_settings rows by key."""

    def __init__(self, values: dict[str, str] | None = None) -> None:
        self.values = values or {}

    async def fetchrow(self, sql: str, *args: object) -> dict[str, object] | None:
        v = self.values.get(str(args[0]))
        return None if v is None else {"value": v}


def _run[T](coro: Coroutine[Any, Any, T]) -> T:
    try:
        coro.send(None)
    except StopIteration as done:
        return cast(T, done.value)
    raise AssertionError("the coroutine waited")


def test_the_relay_is_a_rules_section_on_by_default() -> None:
    cls, title, _ = rules.SECTIONS["relay"]
    assert cls is relay.RelayConfig and title == "Extension relay"
    cfg = relay.RelayConfig()
    assert cfg.enabled and cfg.max_in_flight == 4 and cfg.timeout_sec == 120
    (sec,) = [s for s in rules.schema()["sections"] if s["key"] == "relay"]
    assert {f["name"] for f in sec["fields"]} == {
        "enabled", "requests_per_minute", "burst", "max_in_flight", "max_response_mb",
        "timeout_sec",
    }  # fmt: skip


def test_a_node_without_the_token_gets_the_relayed_client(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "1")
    direct = relay.plex_client({"url": "http://plex:32400", "token": "T"})
    assert isinstance(direct._transport, UrllibTransport) and direct.token == "T"  # noqa: SLF001
    node = relay.plex_client({"url": "http://plex:32400", "token": ""})
    assert isinstance(node._transport, relay.RelayTransport)  # noqa: SLF001
    assert node.token == "" and node.uri == "relay://plex"
    assert relay.relayed({"url": "x"}) and not relay.relayed({"token": "T"})
    # no agent in this worker: nothing to carry the call, the client is the plain one
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "0")
    relay.detach()
    plain = relay.plex_client({"url": "http://plex:32400", "token": ""})
    assert isinstance(plain._transport, UrllibTransport)  # noqa: SLF001


def test_the_relay_target_is_a_path_never_a_url() -> None:
    t = relay.RelayTransport("plex")
    assert (
        t._target("relay://plex/library/sections/1/all?type=4") == "/library/sections/1/all?type=4"
    )  # noqa: SLF001
    assert t._target("relay://plex/identity") == "/identity"  # noqa: SLF001


def test_plex_stages_lift_onto_a_node_while_the_relay_is_on(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    graph = build_full_graph(discover(include_entry_points=False))
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "1")
    lifted = _run(lifted_placements(_Db(), graph))
    assert lifted & {s.name for s in graph if s.name.startswith(("plex_", "dizquetv_"))} == PLEX
    assert "plex_livetv" not in lifted  # beside write_channels, on the home
    # switched off in the settings: home-only again
    off_db = _Db({"rules.relay": '{"enabled": false}'})
    assert not (_run(lifted_placements(off_db, graph)) & PLEX)
    # a worker without the agent has no channel to carry the calls
    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "0")
    relay.detach()
    assert not (_run(lifted_placements(_Db(), graph)) & PLEX)


def test_unreadable_relay_settings_keep_the_stages_home_only(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    class Broken:
        async def fetchrow(self, sql: str, *args: object) -> None:
            raise RuntimeError("connection lost")

    monkeypatch.setenv("MAKTABA_CLUSTER_AGENT", "1")
    graph = build_full_graph(discover(include_entry_points=False))
    assert not (_run(lifted_placements(Broken(), graph)) & PLEX)
