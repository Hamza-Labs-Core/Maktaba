"""components-live through the real API (Epic 51, stories 51.10 T14 and 51.14 T13): the API
built from the commit on the rig's database, an admin signed in as the web app is, and the
components switched with the Components page's own calls (web/src/lib/componentsApi.ts).

    cd pipeline && MAKTABA_COMPONENTS_API_BIN=<go build -o <bin> ./api> \\
        uv run --extra dev pytest ../tests/components/test_api_live.py

- T14 (51.10): a loop component (analytics) off and on again: its phase reaches Off, then
  Running within 5 s (N2), and its health (since, restarts, version, stale, updated_at) is
  the new run's.
- T13 (51.14): a stages component (transcription) off while its jobs run: the Queues tile
  is grey (off) with the parked count within 5 s, and System health lists it off once its
  cache turns; on again, the tile leaves off and the jobs are claimed within 5 s (N2).
- The transition's histogram (51.14 T13): the runner reports the transition it made.

The measurements go to ``components-api-live.json`` beside the logs.
"""

from __future__ import annotations

import asyncio
import json
import os
import time
from datetime import UTC, datetime
from typing import Any

import pytest

from components import rig as R

#: on CI a missing API binary fails (start_api says so) rather than skipping unseen
pytestmark = pytest.mark.skipif(
    not R.PG_URL or (not R.API_BIN and not os.environ.get("CI")),
    reason="MAKTABA_TEST_PG_URL or MAKTABA_COMPONENTS_API_BIN not set",
)

#: N2 (README section 11): click to Running for a loop or stages component
N2_S = 5.0
#: the same 5 s for the tile and the parked count (51.14 T13)
TILE_S = 5.0
#: System health answers from a cache of system_health.cache_seconds (5 s by default)
OVERVIEW_S = TILE_S + 5.0
STAGES = ("content_hash", "transcribe", "component_catchup")
METRICS: dict[str, Any] = {}


def _keep(key: str, value: Any) -> None:
    METRICS[key] = value
    R.LOGS.mkdir(parents=True, exist_ok=True)
    (R.LOGS / "components-api-live.json").write_text(
        json.dumps(METRICS, indent=1, default=str), encoding="utf-8"
    )


def _when(s: str) -> float:
    return datetime.fromisoformat(s).astimezone(UTC).timestamp()


async def _get(api: R.Api, path: str) -> Any:
    return await asyncio.to_thread(api.get, path)


async def _row(api: R.Api, cid: str) -> dict[str, Any]:
    rows = (await _get(api, "/api/components"))["components"]
    return dict(next(c for c in rows if c["id"] == cid))


async def _phase_is(api: R.Api, cid: str, phase: str) -> bool:
    return bool((await _row(api, cid))["phase"] == phase)


async def _tile(api: R.Api, stage: str) -> dict[str, Any]:
    snap = await _get(api, "/api/queues")
    return dict(next(q for q in snap["queues"] if q["stage"] == stage))


async def _overview_off(api: R.Api) -> list[str]:
    status = await _get(api, "/api/admin/system/status")
    item = next(i for i in status["items"] if i["id"] == "queues")
    return list(item["details"]["off"])


async def _pending(r: R.Rig, stage: str) -> int:
    return (await r.states(stage)).get("pending", 0)


async def _in_flight(r: R.Rig, stage: str) -> int:
    s = await r.states(stage)
    return sum(v for k, v in s.items() if k in ("claimed", "running", "resuming"))


def _parked(tile: dict[str, Any], cid: str) -> int | None:
    """The count of the tile's waiting entry on ``cid``; None when it has none."""
    for w in tile["health"]["waiting"]:
        if w.get("component") == cid and w["on"] == f"component:{cid}":
            return int(w["count"])
    return None


# -- T14 (51.10): a loop component through the page's calls ---------------------------


def test_t14_a_loop_component_flipped_through_the_page() -> None:
    asyncio.run(_t14())


async def _t14() -> None:
    async with R.rig() as r:
        api = await asyncio.to_thread(R.start_api, r.url, "api-t14")
        try:
            await R.until(lambda: _phase_is(api, "analytics", "running"), 30)

            t_off = await asyncio.to_thread(api.set_want, "analytics", "installed")
            await R.until(lambda: _phase_is(api, "analytics", "off"), N2_S)
            _keep("T14_off_s", time.time() - t_off)
            assert METRICS["T14_off_s"] <= N2_S
            part = await r.db.fetchrow(
                "SELECT phase, updated_at FROM component_status"
                " WHERE id = 'analytics' AND part = 'api'"
            )
            assert part["phase"] == "off", dict(part)
            assert part["updated_at"].timestamp() >= t_off - 1

            t_on = await asyncio.to_thread(api.set_want, "analytics", "enabled")
            await R.until(lambda: _phase_is(api, "analytics", "running"), N2_S)
            _keep("T14_N2_s", time.time() - t_on)
            assert METRICS["T14_N2_s"] <= N2_S

            # the drawer's health is the new run's: up since the switch, never restarted,
            # the release that reported it, heard from just now
            show = await asyncio.to_thread(api.component, "analytics")
            health = show["health"]
            _keep("T14_health", health)
            assert _when(health["since"]) >= t_on - 1, health
            assert health["restarts"] == 0, health
            assert health["version"], health
            assert health["stale"] is False, health
            assert _when(health["updated_at"]) >= t_on - 1, health
            home = next(m for m in show["machines"] if m["machine"] == "home")
            assert home["phase"] == "running", home
            assert home["health"] == health, home
        finally:
            api.stop()


# -- T13 (51.14): a stages component's tile and System health --------------------------


def test_t13_a_stages_component_off_greys_its_tile() -> None:
    asyncio.run(_t13())


async def _t13() -> None:
    async with R.rig() as r:
        try:
            await _t13_run(r)
        except BaseException:
            await r.dump_jobs("components-api-live")
            raise


async def _t13_run(r: R.Rig) -> None:
    queued = await r.videos(300)
    await r.enqueue("transcribe", queued)
    for i in (1, 2):
        r.start(f"api-live-{i}", STAGES, sleep=1.0)
    await r.wait_online(2)
    await R.until(lambda: _running(r, 2), 60)
    api = await asyncio.to_thread(R.start_api, r.url, "api-t13")
    try:
        before = await _tile(api, "transcribe")
        assert before["health"]["state"] != "off", before["health"]
        assert "transcribe" not in await _overview_off(api)

        t_off = await asyncio.to_thread(api.set_want, "transcription", "installed")

        async def greyed() -> bool:
            tile = await _tile(api, "transcribe")
            pending = int(tile["counts"]["pending"])
            return bool(
                tile["health"]["state"] == "off"
                and tile["component"] == "transcription"
                and pending > 0
                and _parked(tile, "transcription") == pending
            )

        async def listed_off() -> bool:
            return "transcribe" in await _overview_off(api)

        await R.until(greyed, TILE_S)
        _keep("T13_tile_off_s", time.time() - t_off)
        assert METRICS["T13_tile_off_s"] <= TILE_S
        await R.until(listed_off, OVERVIEW_S)
        _keep("T13_overview_off_s", time.time() - t_off)
        assert METRICS["T13_overview_off_s"] <= OVERVIEW_S

        # once it drained, the parked count is the jobs queued behind it in the database
        await R.until(lambda: _zero(_in_flight(r, "transcribe")), 30)
        tile = await _tile(api, "transcribe")
        queued_now = await _pending(r, "transcribe")
        _keep("T13_parked", {"tile": _parked(tile, "transcription"), "database": queued_now})
        assert tile["health"]["state"] == "off", tile["health"]
        assert queued_now > 0
        assert _parked(tile, "transcription") == queued_now, tile["health"]

        t_on = await asyncio.to_thread(api.set_want, "transcription", "enabled")

        async def back() -> bool:
            tile = await _tile(api, "transcribe")
            return tile["health"]["state"] != "off" and _parked(tile, "transcription") is None

        async def claimed() -> bool:
            return await r.claims_after(["transcribe"], t_on) > 0

        async def not_listed() -> bool:
            return "transcribe" not in await _overview_off(api)

        await R.until(back, N2_S)
        _keep("T13_tile_back_s", time.time() - t_on)
        await R.until(claimed, N2_S)
        _keep("T13_N2_s", time.time() - t_on)
        assert METRICS["T13_tile_back_s"] <= N2_S
        assert METRICS["T13_N2_s"] <= N2_S
        await R.until(not_listed, OVERVIEW_S)
        assert await r.lost() == 0
    finally:
        api.stop()


async def _running(r: R.Rig, n: int) -> bool:
    return await _in_flight(r, "transcribe") >= n


async def _zero(coro: Any) -> bool:
    return bool(await coro == 0)


# -- the transition's histogram (51.14 T13) ---------------------------------------------


def test_t13_the_runner_reports_its_transition() -> None:
    asyncio.run(_transition())


async def _transition() -> None:
    async with R.rig() as r:
        r.start("api-live-t", ("transcribe",), sleep=0.3)
        await r.wait_online(1)

        async def reported(phase: str) -> dict[str, Any] | None:
            row = await r.db.fetchrow(
                "SELECT phase, detail FROM component_status"
                " WHERE id = 'transcription' AND part LIKE 'pipeline%'"
            )
            if row is None or row["phase"] != phase:
                return None
            detail = row["detail"]
            return dict(json.loads(detail) if isinstance(detail, str) else detail)

        async def is_running() -> bool:
            return await reported("running") is not None

        async def is_off() -> bool:
            return await reported("off") is not None

        await R.until(is_running, 30)
        await r.want("transcription", "installed")
        await R.until(is_off, 10)
        detail = await reported("off")
        assert detail is not None
        _keep("T13_transition_detail", detail)
        lt = detail.get("last_transition")
        assert isinstance(lt, dict), detail
        assert lt.get("to") == "off", lt
        assert isinstance(lt.get("seconds"), int | float) and lt["seconds"] >= 0, lt
