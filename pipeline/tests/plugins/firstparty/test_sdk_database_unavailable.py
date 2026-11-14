"""A database the plugin API lost (a restart) fails a first-party job as retryable, never for
good: the API answers ``503 plugin_database``, the SDK raises ``DatabaseUnavailable``, the
runner sends it as an exception of the stage's code, and the worker fails the job retryable."""

from __future__ import annotations

import asyncio
import tempfile
from pathlib import Path

import pytest
from maktaba_plugin_sdk.native import DatabaseUnavailable, PluginRefused
from maktaba_plugin_sdk.native.transport import UnixTransport

from maktaba_pipeline.engine.context import StageFailure
from maktaba_pipeline.plugins.native import failure_to_wire, raise_failure

asyncio_test = pytest.mark.asyncio


async def _answer(
    status: str, body: bytes
) -> tuple[asyncio.AbstractServer, str, tempfile.TemporaryDirectory[str]]:
    async def serve(r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        head = await r.readuntil(b"\r\n\r\n")
        length = int(
            [h for h in head.split(b"\r\n") if h.lower().startswith(b"content-length")][0].split(
                b":"
            )[1]
        )
        await r.readexactly(length)
        w.write(
            b"HTTP/1.1 %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n"
            b"Connection: close\r\n\r\n%s" % (status.encode(), len(body), body)
        )
        await w.drain()
        w.close()

    d = tempfile.TemporaryDirectory()
    sock = str(Path(d.name) / "api.sock")
    return await asyncio.start_unix_server(serve, path=sock), sock, d


@asyncio_test
async def test_a_lost_database_is_not_a_refusal() -> None:
    server, sock, d = await _answer(
        "503 Service Unavailable",
        b'{"code": "plugin_database", "detail": "driver: bad connection"}',
    )
    with d:
        async with server:
            t = UnixTransport(sock, "tok")
            with pytest.raises(DatabaseUnavailable) as e:
                await t.call("trusted/stmt", {})
            await t.close()
    assert not isinstance(e.value, PluginRefused)
    assert e.value.detail == "driver: bad connection"
    assert "driver: bad connection" in str(e.value)


@asyncio_test
@pytest.mark.parametrize(
    ("status", "body", "code"),
    [
        # the statement's own failure: the plugin's code reads Postgres's code
        (
            "409 Conflict",
            b'{"code": "plugin_database", "detail": "23505: duplicate key"}',
            "plugin_database",
        ),
        ("403 Forbidden", b'{"code": "plugin_scope"}', "plugin_scope"),
        # another 503 of the API is a refusal as before
        ("503 Service Unavailable", b'{"code": "plugin_held"}', "plugin_held"),
    ],
)
async def test_other_answers_stay_refusals(status: str, body: bytes, code: str) -> None:
    server, sock, d = await _answer(status, body)
    with d:
        async with server:
            t = UnixTransport(sock, "tok")
            with pytest.raises(PluginRefused) as e:
                await t.call("trusted/stmt", {})
            await t.close()
    assert e.value.code == code
    assert e.value.status == int(status.split()[0])


def test_the_worker_fails_the_job_as_retryable() -> None:
    """The runner sends it as any exception of the stage's code; the worker raises one of the
    same class name, which the engine fails as retryable (engine/run.py: every exception that
    is not a StageFailure). A refusal's wire form is a StageFailure that is not."""
    wire = failure_to_wire(DatabaseUnavailable("driver: bad connection"))
    assert wire["status"] == "error"
    assert wire["error_type"] == "DatabaseUnavailable"
    with pytest.raises(Exception) as e:  # noqa: PT011 - the class is made from the name
        raise_failure(wire)
    assert type(e.value).__name__ == "DatabaseUnavailable"
    assert not isinstance(e.value, StageFailure)
    assert "driver: bad connection" in str(e.value)

    with pytest.raises(StageFailure) as refused:
        raise_failure({"status": "refused", "code": "plugin_database", "message": "23505: dup"})
    assert refused.value.retryable is False
