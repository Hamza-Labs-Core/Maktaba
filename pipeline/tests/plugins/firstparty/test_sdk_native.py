"""The plugin SDK's native mode (Epic 54, stories 54.23 §2, 54.24 §1): named statements, the
values on the wire, and the database a first-party plugin's code sees."""

from __future__ import annotations

import math
from datetime import UTC, date, datetime, timedelta
from decimal import Decimal
from typing import Any
from uuid import UUID, uuid4

import pytest
from maktaba_plugin_sdk.native import PluginRefused, StatementDB, StatementUnregistered
from maktaba_plugin_sdk.native.codec import decode, encode
from maktaba_plugin_sdk.native.records import Record
from maktaba_plugin_sdk.statements import (
    Registry,
    Statement,
    StatementError,
    parse_file,
    render_file,
)

asyncio_test = pytest.mark.asyncio


def test_a_statement_file_round_trips_and_its_header_is_checked() -> None:
    text = (
        "-- name: promo_links.run.2\n-- mode: write\n-- scope: library $1\n-- tables: promo_links*\n"  # noqa: E501
        "DELETE FROM promo_links WHERE library_id = $1\n"
    )
    st = parse_file(text)
    assert (st.name, st.mode, st.scope, st.scope_arg, st.writes) == (
        "promo_links.run.2",
        "write",
        "library",
        1,
        ("promo_links",),
    )
    assert render_file(st) == text
    for bad in (
        text.replace("-- mode: write", "-- mode: maybe"),
        text.replace("-- scope: library $1", "-- scope: library"),
        text.replace("-- scope: library $1", "-- scope: library $3"),
        text.replace("-- mode: write", "-- mode: read"),
        text.replace("-- name: promo_links.run.2\n", ""),
        text.replace("-- tables:", "-- tablez:"),
    ):
        with pytest.raises(StatementError):
            parse_file(bad)


def test_the_registry_refuses_two_names_or_two_texts() -> None:
    a = Statement("a", "SELECT 1")
    with pytest.raises(StatementError):
        Registry.of("p", [a, Statement("a", "SELECT 2")])
    with pytest.raises(StatementError):
        Registry.of("p", [a, Statement("b", " SELECT 1 ")])
    assert Registry.of("p", [a]).lookup("  SELECT 1\n") is a


@pytest.mark.parametrize(
    "value",
    [
        None,
        True,
        7,
        2.5,
        "نص",
        uuid4(),
        datetime(2026, 11, 6, 10, 0, 0, 123456, tzinfo=UTC),
        datetime(2026, 11, 6, 10, 0, 0),
        date(2026, 11, 6),
        Decimal("12.50"),
        b"\x00\x01",
        timedelta(days=1, seconds=3, microseconds=5),
        [1, "a", None, [uuid4()]],
    ],
)
def test_values_keep_their_python_type_on_the_wire(value: Any) -> None:
    assert decode(encode(value)) == value


def test_nan_and_infinity_travel() -> None:
    assert math.isnan(decode(encode(float("nan"))))
    assert decode(encode(float("-inf"))) == float("-inf")


def test_a_record_reads_like_asyncpgs() -> None:
    r = Record(["id", "n", "id"], [1, 2, 3], Record.index_of(["id", "n", "id"]))
    assert (r["id"], r[1], r.get("x", 9), len(r), list(r)) == (1, 2, 9, 3, [1, 2, 3])
    assert dict(zip(r.keys(), r.values(), strict=True)) == {"id": 3, "n": 2}
    assert "n" in r and r == (1, 2, 3)


class FakeTransport:
    def __init__(self, rows: list[list[Any]] | None = None) -> None:
        self.calls: list[tuple[str, dict[str, Any]]] = []
        self.rows = rows or []

    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
        self.calls.append((fn, body))
        if fn == "trusted/tx":
            return {"tx": "tx_1"}
        return {"columns": ["id", "at"], "rows": self.rows, "status": "UPDATE 1"}


@asyncio_test
async def test_the_database_sends_names_never_text() -> None:
    reg = Registry.of("tv.maktaba.x", [Statement("x.read", "SELECT id, at FROM t WHERE id = $1")])
    t = FakeTransport([[{"$t": "uuid", "v": str(UUID(int=1))}, None]])
    db = StatementDB(reg, t)
    row = await db.fetchrow("SELECT id, at FROM t WHERE id = $1", UUID(int=1))
    assert row["id"] == UUID(int=1)
    fn, body = t.calls[-1]
    assert fn == "trusted/stmt" and body["name"] == "x.read" and "SELECT" not in str(body)
    with pytest.raises(StatementUnregistered):
        await db.fetch("SELECT * FROM t")
    assert len(t.calls) == 1  # an unregistered text never leaves the process


@asyncio_test
async def test_a_transaction_holds_its_statements_and_nests_as_savepoints() -> None:
    reg = Registry.of(
        "tv.maktaba.x", [Statement("x.w", "UPDATE t SET a = 1", mode="write", writes=("t",))]
    )
    t = FakeTransport()
    db = StatementDB(reg, t)
    async with db.transaction():
        await db.execute("UPDATE t SET a = 1")
        with pytest.raises(RuntimeError):
            async with db.transaction():
                raise RuntimeError("inner")
    ops = [b.get("op") if f == "trusted/tx" else ("stmt", b.get("tx")) for f, b in t.calls]
    assert ops == ["begin", ("stmt", "tx_1"), "savepoint", "rollback_to", "commit"]


@asyncio_test
async def test_a_refusal_reaches_the_code_as_an_error() -> None:
    reg = Registry.of("tv.maktaba.x", [Statement("x.read", "SELECT 1")])

    class Refusing(FakeTransport):
        async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
            raise PluginRefused(403, "plugin_scope", "video")

    with pytest.raises(PluginRefused) as e:
        await StatementDB(reg, Refusing()).fetch("SELECT 1")
    assert e.value.code == "plugin_scope"


@asyncio_test
async def test_the_transport_reads_chunked_answers_on_a_kept_alive_connection() -> None:
    """Go streams an answer it did not size (a long read) chunked; the next call on the same
    connection must start at its own status line."""
    import asyncio
    import tempfile
    from pathlib import Path

    from maktaba_plugin_sdk.native.transport import UnixTransport

    async def serve(r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        for n in range(2):
            head = await r.readuntil(b"\r\n\r\n")
            length = int(
                [h for h in head.split(b"\r\n") if h.lower().startswith(b"content-length")][
                    0
                ].split(b":")[1]
            )
            await r.readexactly(length)
            body = b'{"rows": [[%d]], "columns": ["n"]}' % n
            half = len(body) // 2
            w.write(b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n")
            w.write(
                b"%x\r\n%s\r\n%x\r\n%s\r\n0\r\n\r\n"
                % (half, body[:half], len(body) - half, body[half:])
            )
            await w.drain()
        w.close()

    with tempfile.TemporaryDirectory(dir="/tmp") as d:
        sock = str(Path(d) / "api.sock")
        server = await asyncio.start_unix_server(serve, path=sock)
        async with server:
            t = UnixTransport(sock, "tok")
            assert (await t.call("trusted/stmt", {}))["rows"] == [[0]]
            assert (await t.call("trusted/stmt", {}))["rows"] == [[1]]
            await t.close()
