"""The pipeline's component runner (Epic 51, story 51.06): a pass on desired state, the
home's stage rows and the catch-up, the loops, the idle cost (T10) and the kill switch
(T12)."""

from __future__ import annotations

import asyncio
from typing import Any

import pytest

from maktaba_pipeline.cluster import stages as cluster_stages
from maktaba_pipeline.cluster.stages import StageSelector
from maktaba_pipeline.components import runner as runner_mod
from maktaba_pipeline.components import store
from maktaba_pipeline.components.catalog import default as default_catalog
from maktaba_pipeline.components.runner import ComponentRunner, gate_open, instance_part
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.components._helpers import set_want, stage_row

SKIPPED = ("transcribe", "extract")


@pytest.fixture(autouse=True)
def _every_stage_can_run(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)


def _runner(db: Any, **kw: Any) -> tuple[ComponentRunner, StageSelector]:
    sel = StageSelector(graph=GRAPH)
    sel.compute()
    r = ComponentRunner(
        db, graph=GRAPH, selector=sel, worker_id="w-test", reconcile=True, drain_poll_s=0.02, **kw
    )
    return r, sel


async def _pass(r: ComponentRunner) -> None:
    desired, rev = await store.desired(r.db)
    await r.apply(desired, rev)
    await r.wait_drains()
    await r._report()  # the pass a drain's end wakes


@pytest.mark.asyncio
async def test_off_then_on_flips_the_selection_the_rows_and_queues_a_catchup(
    db: Database,
) -> None:
    await sync_graph(db, GRAPH, gates={})
    r, sel = _runner(db)
    assert await r.prime() == {}
    await _pass(r)
    assert "transcribe" in sel.served and r.machine_reasons() == {}

    await set_want(db, "transcription", "installed")
    await _pass(r)
    for st in SKIPPED:
        assert st not in sel.served
        assert sel.stages_off[st] == "component_off:transcription"
        assert await stage_row(db, st) == (False, "component_off")
    assert not gate_open("transcription")
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].phase == "off"
    assert "off_since" in rows["transcription"].detail
    assert rows["subtitles"].reason == "dependency_off"

    await set_want(db, "transcription", "enabled")
    await _pass(r)
    assert "transcribe" in sel.served
    for st in SKIPPED:
        assert await stage_row(db, st) == (True, None)
    jobs = await db.fetch("SELECT payload FROM processing_jobs WHERE stage = 'component_catchup'")
    assert len(jobs) == 1
    entries = loads(jobs[0]["payload"])["components"]
    assert [e["component"] for e in entries] == ["subtitles", "transcription"]
    assert all(e["off_since"] for e in entries)
    await r.close()


@pytest.mark.asyncio
async def test_the_catchup_is_queued_when_another_worker_turns_the_stages_back_on(
    db: Database,
) -> None:
    """#646: two home workers race for the leader lock on every pass. When one turned
    the stages off and the other turns them on, the catch-up is still queued (once), with
    the time the first one reported."""
    await sync_graph(db, GRAPH, gates={})
    a, _ = _runner(db)
    b, _ = _runner(db)
    for r in (a, b):
        await r.prime()
        await _pass(r)

    await set_want(db, "transcription", "installed")
    await _pass(a)  # a leads the off; b's pass lost the lock
    assert await stage_row(db, "transcribe") == (False, "component_off")
    reported = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    off_since = reported["transcription"].detail["off_since"]

    await set_want(db, "transcription", "enabled")
    await _pass(b)  # b leads the on
    assert await stage_row(db, "transcribe") == (True, None)
    jobs = await db.fetch("SELECT payload FROM processing_jobs WHERE stage = 'component_catchup'")
    assert len(jobs) == 1
    entries = {e["component"]: e["off_since"] for e in loads(jobs[0]["payload"])["components"]}
    assert entries["transcription"] == off_since

    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE stage = 'component_catchup'")
    await _pass(a)  # a leads next: b queued the catch-up, a does not queue another
    n = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'component_catchup'")
    assert n == 1
    assert "transcription" not in a._off_since
    await a.close()
    await b.close()


@pytest.mark.asyncio
async def test_a_worker_that_read_the_off_time_at_start_queues_no_second_catchup(
    db: Database,
) -> None:
    """#659 review, three home workers: b turns transcription off; c starts meanwhile and
    reads its off time from b's status row; b leads the on and queues the catch-up, which
    finishes; c leads next. c must see the stages already open and queue nothing: a
    second catch-up would run the whole time off again (all history when the time is
    unknown)."""
    await sync_graph(db, GRAPH, gates={})
    a, _ = _runner(db)
    b, _ = _runner(db)
    for r in (a, b):
        await r.prime()
        await _pass(r)

    await set_want(db, "transcription", "installed")
    await _pass(b)  # b leads the off
    c, _ = _runner(db)  # c starts while it is off
    await c.prime()
    await _pass(c)  # c leads once: it reads the off time b reported
    assert "transcription" in c._off_since

    await set_want(db, "transcription", "enabled")
    await _pass(b)  # b leads the on and queues the catch-up
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE stage = 'component_catchup'")
    await _pass(c)  # c leads next
    n = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'component_catchup'")
    assert n == 1
    assert "transcription" not in c._off_since
    for r in (a, b, c):
        await r.close()


@pytest.mark.asyncio
async def test_a_worker_that_does_not_lead_forgets_an_off_time_once_it_is_on_again(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """#659 review: a worker that turned a component off and then lost the leader lock kept
    reporting its off time while the component was on (the Components page showed it off
    since then) and a later catch-up started from it. A pass that does not lead drops the
    times the database says are over."""
    if not is_pg(db):
        pytest.skip("SQLite has one process: it always leads")
    import contextlib

    from maktaba_pipeline.engine import leader as leader_mod

    await sync_graph(db, GRAPH, gates={})
    a, _ = _runner(db)
    b, _ = _runner(db)
    for r in (a, b):
        await r.prime()
        await _pass(r)
    await set_want(db, "transcription", "installed")
    await _pass(a)  # a leads the off
    await set_want(db, "transcription", "enabled")
    await _pass(b)  # b leads the on

    real = leader_mod.leader

    @contextlib.asynccontextmanager
    async def lost(*_a: Any, **_k: Any) -> Any:
        yield False

    monkeypatch.setattr(leader_mod, "leader", lost)
    await _pass(a)  # a does not lead
    assert "transcription" not in a._off_since
    rows = [s for s in await store.statuses(db) if s.id == "transcription"]
    assert rows and all("off_since" not in s.detail for s in rows), [s.detail for s in rows]
    monkeypatch.setattr(leader_mod, "leader", real)
    for r in (a, b):
        await r.close()


@pytest.mark.asyncio
async def test_held_stops_claiming_but_no_catchup_follows(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    r, sel = _runner(db)
    await set_want(db, "transcription", "enabled", hold=True)
    await _pass(r)
    assert sel.stages_off["transcribe"] == "component_held:transcription"
    assert await stage_row(db, "transcribe") == (False, "component_held")
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert (rows["transcription"].phase, rows["transcription"].reason) == ("stopped", "held")

    await set_want(db, "transcription", "enabled")
    await _pass(r)
    assert await stage_row(db, "transcribe") == (True, None)
    # a hold queues its jobs (they wait): nothing was skipped, nothing to catch up
    n = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'component_catchup'")
    assert n == 0
    await r.close()


@pytest.mark.asyncio
async def test_a_node_stops_claiming_and_leaves_the_home_rows(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    r, sel = _runner(db, role="node")
    await set_want(db, "transcription", "installed")
    await _pass(r)
    assert sel.stages_off["transcribe"] == "component_off:transcription"
    # only the home's leader writes job_stages
    assert await stage_row(db, "transcribe") == (True, None)
    await r.close()


@pytest.mark.asyncio
async def test_loops_follow_their_component(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    r, _ = _runner(db)
    ran: list[str] = []

    async def topup(stop: asyncio.Event) -> None:
        ran.append("start")
        await stop.wait()
        ran.append("stop")

    r.declare("channel-topup", topup)
    await _pass(r)
    await asyncio.sleep(0)
    assert r.loops["channel-topup"].running()

    await set_want(db, "livetv", "installed")
    await _pass(r)
    for _ in range(50):
        if not r.loops["channel-topup"].running():
            break
        await asyncio.sleep(0.01)
    assert ran == ["start", "stop"]

    await set_want(db, "livetv", "enabled")
    await _pass(r)
    await asyncio.sleep(0)
    assert r.loops["channel-topup"].running()
    await r.close()
    assert ran == ["start", "stop", "start", "stop"]


@pytest.mark.asyncio
async def test_a_restart_request_restarts_the_pipelines_loops(db: Database) -> None:
    """#636 (slot 0185): POST /api/components/livetv/restart writes a restart request for
    the pipeline (its loop channel-topup runs here, where the API cannot reach it); the
    runner's next pass stops the loop within its grace and starts it again. A request
    made before the runner's first read restarts nothing, and one read again neither."""
    await sync_graph(db, GRAPH, gates={})
    ran: list[str] = []

    async def topup(stop: asyncio.Event) -> None:
        ran.append("start")
        await stop.wait()
        ran.append("stop")

    async def request(cid: str) -> None:
        await db.execute(
            "INSERT INTO component_restarts AS r (machine, id, host)"
            " VALUES ('home', $1, 'pipeline')"
            " ON CONFLICT (machine, id, host) DO UPDATE SET seq = r.seq + 1",
            cid,
        )

    await request("livetv")
    r, _ = _runner(db)
    r.declare("channel-topup", topup)
    await _pass(r)
    await asyncio.sleep(0)
    assert ran == ["start"]
    rev = await store.revision(db)

    await request("livetv")
    assert await store.revision(db) == rev + 1  # the runners hear it
    await _pass(r)
    for _ in range(100):
        if ran == ["start", "stop"] and not r._stoppers:
            break
        await asyncio.sleep(0.01)
    await _pass(r)  # the pass the stopped loop wakes
    await asyncio.sleep(0)
    assert ran == ["start", "stop", "start"]
    assert r.loops["channel-topup"].running()

    await _pass(r)  # the same request read again
    await asyncio.sleep(0)
    assert ran == ["start", "stop", "start"]
    assert r.take_restarts({"livetv": 2, "queue_health": 1}) == ["queue_health"]
    await r.close()


def test_a_loop_not_in_the_catalog_is_refused(db: Any = None) -> None:
    r = ComponentRunner(None, reconcile=True)

    async def fn(_stop: asyncio.Event) -> None:
        return None

    with pytest.raises(ValueError, match="not in the catalog"):
        r.declare("no-such-loop", fn)


def test_instance_part() -> None:
    assert instance_part("") == "pipeline"
    assert instance_part("stt-1") == "pipeline/stt-1"


# -- T10: idle -------------------------------------------------------------------


class _Counting:
    """The database, counting every statement."""

    def __init__(self, db: Any) -> None:
        self._db = db
        self.n = 0

    def __getattr__(self, name: str) -> Any:
        attr = getattr(self._db, name)
        if name in ("fetch", "fetchrow", "fetchval", "execute"):

            async def counted(*a: Any, **kw: Any) -> Any:
                self.n += 1
                return await attr(*a, **kw)

            return counted
        return attr


@pytest.mark.asyncio
async def test_t10_an_idle_runner_costs_one_query_per_poll(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    counting = _Counting(db)
    r, _ = _runner(counting)
    await r._pass(force=True)
    counting.n = 0
    # five idle minutes at the 30 s poll
    polls = int(300 / store.POLL_S)
    for _ in range(polls):
        await r._pass(force=False)
    assert counting.n <= 10
    assert counting.n == polls
    await r.close()


# -- T12: the kill switch ----------------------------------------------------------


@pytest.mark.asyncio
async def test_t12_reconcile_off_changes_nothing_and_says_so(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_COMPONENTS_RECONCILE", "off")
    assert not runner_mod.reconcile_enabled()
    await sync_graph(db, GRAPH, gates={})
    sel = StageSelector(graph=GRAPH)
    sel.compute()
    r = ComponentRunner(db, graph=GRAPH, selector=sel, worker_id="w-test")
    ran: list[str] = []

    async def topup(stop: asyncio.Event) -> None:
        ran.append("start")
        await stop.wait()

    r.declare("channel-topup", topup)
    await set_want(db, "transcription", "installed")
    await set_want(db, "livetv", "installed")
    assert await r.prime() is None
    await _pass(r)
    await asyncio.sleep(0)
    assert "transcribe" in sel.served and sel.components == {}
    assert await stage_row(db, "transcribe") == (True, None)
    assert r.loops["channel-topup"].running() and ran == ["start"]
    assert gate_open("transcription")
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].reason == "reconcile_off"
    assert rows["livetv"].reason == "reconcile_off"
    await r.close()


# -- story 51.13: a component whose settings failed to move stays off ----------------


async def _mark_failed(db: Database, section: str, error: str) -> None:
    """What the home's settings pass writes when a section's migration raises."""
    cast = "::jsonb" if is_pg(db) else ""
    await db.execute(
        f"INSERT INTO app_settings (key, value) VALUES ($1, $2{cast})",
        f"rules_version.{section}",
        dumps({"major": 1, "minor": 0, "failed": error}),
    )


@pytest.mark.asyncio
async def test_a_failed_settings_section_keeps_its_component_off_until_saved(
    db: Database,
) -> None:
    await sync_graph(db, GRAPH, gates={})
    r, sel = _runner(db)
    await _mark_failed(db, "transcription", "cannot read relisten_tries")
    await r.prime()
    await _pass(r)
    # never on the defaults: its stages are not claimed, the home's rows turn them off
    assert sel.stages_off["transcribe"] == "component_off:transcription"
    assert await stage_row(db, "transcribe") == (False, "component_off")
    assert not gate_open("transcription")
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert (rows["transcription"].phase, rows["transcription"].reason) == (
        "failed",
        "settings_invalid",
    )
    assert rows["transcription"].conditions[0].params["section"] == "transcription"
    # what requires it waits; a component that does not own the section runs
    assert rows["subtitles"].reason == "dependency_off"
    assert rows["livetv"].phase == "running"

    # a full save clears the mark without moving the revision: the next idle poll sees it
    await db.execute("DELETE FROM app_settings WHERE key = 'rules_version.transcription'")
    await r._pass(force=False)
    await r.wait_drains()
    assert "transcribe" in sel.served
    assert await stage_row(db, "transcribe") == (True, None)
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].phase == "running"
    await r.close()


@pytest.mark.asyncio
async def test_a_component_whose_stages_lack_their_model_here_waits_on_it(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Enabled, but none of its stages can run here (the engine cannot load the model:
    stage_unavailable_here): the row says it waits on the model, never running."""
    await sync_graph(db, GRAPH, gates={})
    comp = default_catalog().get("transcription")
    assert comp is not None
    mine = set(comp.stages)
    monkeypatch.setattr(
        cluster_stages,
        "_cannot_run",
        lambda st, _h: "no Whisper model on this machine" if st.name in mine else None,
    )
    r, sel = _runner(db)
    await r.prime()
    sel.compute()
    await _pass(r)
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    tr = rows["transcription"]
    req = next(c for c in tr.conditions if c.type == "RequirementsMet")
    assert tr.phase == "waiting"
    assert (req.status, req.params["component"]) == ("unknown", "model.whisper-turbo")
    assert rows["livetv"].phase == "running"

    # the model in: its stages run, and so does it
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)
    sel.compute()
    await r._report()
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].phase == "running"
    await r.close()


@pytest.mark.asyncio
async def test_a_process_stopped_before_its_pass_saw_the_off_reports_off_at_close(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The supervisor stops a process whose component was just turned off, often before
    the runner's pass read the change. Its rows are written by nothing else: at close it
    reads desired state once more and reports off at the new generation, not the waiting
    (its model missing) of the old one."""
    await sync_graph(db, GRAPH, gates={})
    comp = default_catalog().get("transcription")
    assert comp is not None
    mine = set(comp.stages)
    monkeypatch.setattr(
        cluster_stages,
        "_cannot_run",
        lambda st, _h: "no Whisper model on this machine" if st.name in mine else None,
    )
    r, sel = _runner(db)
    await r.prime()
    sel.compute()
    await _pass(r)
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].phase == "waiting"
    before = rows["transcription"].observed_generation

    await set_want(db, "transcription", "installed")  # no pass: the process is stopped first
    await r.close()
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    desired, _ = await store.desired(db)
    assert rows["transcription"].phase == "off"
    assert rows["transcription"].observed_generation == desired["transcription"].generation
    assert rows["transcription"].observed_generation > before


@pytest.mark.asyncio
async def test_the_rows_carry_the_last_transition(db: Database) -> None:
    """Story 51.14: each row's detail.last_transition is the time from the generation the
    runner acted on to the settled phase it reached (the supervisor's histogram reads it)."""
    await sync_graph(db, GRAPH, gates={})
    r, _ = _runner(db)
    await r.prime()
    await _pass(r)
    await set_want(db, "transcription", "installed")
    await _pass(r)
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    lt = rows["transcription"].detail["last_transition"]
    assert lt["to"] == "off" and isinstance(lt["seconds"], float) and lt["seconds"] >= 0
    await set_want(db, "transcription", "enabled")
    await _pass(r)
    rows = {s.id: s for s in await store.statuses(db) if s.part == "pipeline"}
    assert rows["transcription"].detail["last_transition"]["to"] == "running"
    await r.close()


@pytest.mark.asyncio
async def test_once_nothing_failed_an_idle_poll_is_one_query_again(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    await _mark_failed(db, "transcription", "bad")
    counting = _Counting(db)
    r, _ = _runner(counting)
    await r._pass(force=True)
    assert set(r.settings_blocked) == {"transcription"}
    await db.execute("DELETE FROM app_settings WHERE key = 'rules_version.transcription'")
    await r._pass(force=False)  # the poll sees the save
    assert r.settings_blocked == {}
    counting.n = 0
    for _ in range(5):
        await r._pass(force=False)
    assert counting.n == 5
    await r.close()


@pytest.mark.asyncio
async def test_the_listener_is_up_before_the_first_pass(db: Database) -> None:
    # the first pass reports the phases; a change written just after it must be notified,
    # not left to the next poll (components-live T13 waited 10 s for "off")
    r, _ = _runner(db)
    order: list[str] = []

    async def listen() -> None:
        order.append("listen")

    async def a_pass(*, force: bool) -> None:
        order.append("pass")
        stop.set()

    stop = asyncio.Event()
    r._listen = listen  # type: ignore[method-assign]
    r._pass = a_pass  # type: ignore[method-assign]
    await r.run(stop)
    assert order[:2] == ["listen", "pass"], order


@pytest.mark.asyncio
async def test_a_loops_lines_carry_its_component(db: Database) -> None:
    """Epic 53 story 53.01 T07: a declared loop logs with its catalog component."""
    import structlog

    await sync_graph(db, GRAPH, gates={})
    r, _ = _runner(db)
    seen: list[Any] = []

    async def topup(stop: asyncio.Event) -> None:
        seen.append(structlog.contextvars.get_contextvars().get("component"))
        await stop.wait()

    r.declare("channel-topup", topup)
    await _pass(r)
    for _ in range(50):
        if seen:
            break
        await asyncio.sleep(0.01)
    await r.close()
    comp = default_catalog().by_loop("pipeline", "channel-topup")
    assert comp is not None and seen == [comp.id]
    assert structlog.contextvars.get_contextvars().get("component") is None
