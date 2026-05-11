"""Pipeline gRPC server (architecture §9.9 / Story 7.18).

The API talks to the pipeline over a small gRPC surface:

- ``Embed(EmbedRequest) -> EmbedResponse`` — embed a text snippet for
  semantic search (Story 5.4).
- ``ListBackends(Empty) -> BackendList`` — enumerate registered STT
  backends with availability + cost metadata (Story 7.15 AC-4).
- ``ExtractEmbeddedSubtitle(ExtractRequest) -> ExtractResponse`` —
  pull one embedded subtitle stream out of the source container by
  index (Story 4.x / 7.x).

The server is implemented with :mod:`grpcio`'s generic RPC handler
interface. Until the ``shared/proto/pipeline.proto`` source-of-truth
proto file lands (Story 7.18 plan §6), this module ships a stable
JSON-on-the-wire shim: each method's request and response are
JSON-encoded bytes carrying flat dictionaries. The API's typed Go
client matches the wire shape; a future migration can switch to
protobuf without changing the service name or method names.

Operators set ``MAKTABA_PIPELINE_GRPC_ADDR`` (e.g. ``0.0.0.0:50051``)
and the worker daemon starts the server in parallel with the claim
loop.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable
from typing import Any

from .log import get_logger
from .stt.registry import BackendRegistry

__all__ = [
    "PIPELINE_SERVICE_NAME",
    "PipelineService",
    "serve_grpc",
]


_log = get_logger()

PIPELINE_SERVICE_NAME = "maktaba.pipeline.v1.Pipeline"


def _identity_serializer(value: bytes) -> bytes:
    return value


def _identity_deserializer(value: bytes) -> bytes:
    return value


class PipelineService:
    """Pure-Python service implementation used by the gRPC handlers.

    Construct with optional ``embedder`` / ``stt_registry`` / ``subtitle_extractor``
    overrides so unit tests can drive each method without touching the
    real backends.
    """

    def __init__(
        self,
        *,
        embedder: Callable[[str], Awaitable[list[float]]] | None = None,
        stt_registry: BackendRegistry | None = None,
        subtitle_extractor: Callable[[str, int], Awaitable[str]] | None = None,
    ) -> None:
        self._embedder = embedder
        self._stt_registry = stt_registry if stt_registry is not None else BackendRegistry()
        self._subtitle_extractor = subtitle_extractor

    async def embed(self, payload: dict[str, Any]) -> dict[str, Any]:
        text = payload.get("text")
        if not isinstance(text, str) or not text:
            raise ValueError("embed: text is required")
        if self._embedder is None:
            raise RuntimeError("embedder backend not configured")
        vector = await self._embedder(text)
        return {"vector": list(vector)}

    async def list_backends(self, _payload: dict[str, Any]) -> dict[str, Any]:
        rows: list[dict[str, Any]] = []
        health = await self._stt_registry.health_map()
        for name in self._stt_registry.names():
            backend_health = health.get(name)
            backend = self._stt_registry.get(name)
            ready = bool(backend_health.ready) if backend_health is not None else False
            version = backend_health.version if backend_health is not None else ""
            device = backend_health.device if backend_health is not None else ""
            rows.append(
                {
                    "name": name,
                    "available": ready,
                    "version": version,
                    "models": list(getattr(backend, "models", []) or []),
                    "hwaccel": device,
                    "cost_per_minute_usd": float(getattr(backend, "cost_per_minute_usd", 0.0)),
                }
            )
        return {"backends": rows}

    async def extract_embedded_subtitle(self, payload: dict[str, Any]) -> dict[str, Any]:
        path = payload.get("path")
        stream_index = payload.get("stream_index")
        if not isinstance(path, str) or not path:
            raise ValueError("extract_embedded_subtitle: path is required")
        if not isinstance(stream_index, int):
            raise ValueError("extract_embedded_subtitle: stream_index is required")
        if self._subtitle_extractor is None:
            raise RuntimeError("subtitle extractor not configured")
        body = await self._subtitle_extractor(path, stream_index)
        return {"body": body}


def _build_generic_handler(service: PipelineService) -> Any:
    """Return a :class:`grpc.GenericRpcHandler` exposing the three RPCs."""

    import grpc  # type: ignore[import-untyped]  # noqa: PLC0415

    async def _embed(request: bytes, _ctx: Any) -> bytes:
        return await _dispatch(request, service.embed)

    async def _list_backends(request: bytes, _ctx: Any) -> bytes:
        return await _dispatch(request, service.list_backends)

    async def _extract(request: bytes, _ctx: Any) -> bytes:
        return await _dispatch(request, service.extract_embedded_subtitle)

    handlers = {
        "Embed": grpc.unary_unary_rpc_method_handler(
            _embed,
            request_deserializer=_identity_deserializer,
            response_serializer=_identity_serializer,
        ),
        "ListBackends": grpc.unary_unary_rpc_method_handler(
            _list_backends,
            request_deserializer=_identity_deserializer,
            response_serializer=_identity_serializer,
        ),
        "ExtractEmbeddedSubtitle": grpc.unary_unary_rpc_method_handler(
            _extract,
            request_deserializer=_identity_deserializer,
            response_serializer=_identity_serializer,
        ),
    }
    return grpc.method_handlers_generic_handler(PIPELINE_SERVICE_NAME, handlers)


async def _dispatch(
    request: bytes,
    handler: Callable[[dict[str, Any]], Awaitable[dict[str, Any]]],
) -> bytes:
    try:
        payload: dict[str, Any] = json.loads(request.decode("utf-8")) if request else {}
    except json.JSONDecodeError as exc:
        return json.dumps({"error": f"invalid request: {exc}"}).encode("utf-8")
    try:
        result = await handler(payload)
    except (ValueError, RuntimeError) as exc:
        return json.dumps({"error": str(exc)}).encode("utf-8")
    return json.dumps(result).encode("utf-8")


async def serve_grpc(
    *,
    addr: str,
    service: PipelineService | None = None,
    max_workers: int = 10,
) -> tuple[Any, asyncio.Task[None]]:
    """Start the gRPC server bound to ``addr`` (e.g. ``0.0.0.0:50051``).

    Returns the server handle (call ``.stop(grace=…)`` to shut down)
    and the background task that drives the server. The caller is
    responsible for awaiting both on graceful shutdown.
    """

    import grpc  # noqa: PLC0415

    if service is None:
        service = PipelineService()

    server = grpc.aio.server()
    server.add_generic_rpc_handlers((_build_generic_handler(service),))
    server.add_insecure_port(addr)
    await server.start()
    _log.info("pipeline_grpc_started", addr=addr, max_workers=max_workers)

    async def _wait() -> None:
        await server.wait_for_termination()

    task = asyncio.create_task(_wait(), name="pipeline-grpc")
    return server, task
