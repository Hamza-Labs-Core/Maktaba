"""The pipeline gRPC server's dispatch (Epic 53 story 53.08: the request id)."""

from __future__ import annotations

import pytest


@pytest.mark.asyncio
async def test_a_calls_lines_carry_its_request_id() -> None:
    """Epic 53 story 53.08 T03: the request id in the API's gRPC metadata is bound for the
    call's lines; a call without one binds nothing."""
    import structlog

    from maktaba_pipeline.grpc_server import _dispatch, request_id_of

    class Ctx:
        def __init__(self, md: list[tuple[str, str]]) -> None:
            self.md = md

        def invocation_metadata(self) -> list[tuple[str, str]]:
            return self.md

    seen: list[object] = []

    async def handler(_payload: dict[str, object]) -> dict[str, object]:
        seen.append(structlog.contextvars.get_contextvars().get("request_id"))
        return {}

    rid = "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60"
    assert request_id_of(Ctx([("maktaba-request-id", rid)])) == rid
    assert request_id_of(None) == ""
    await _dispatch(b"{}", handler, Ctx([("maktaba-request-id", rid)]))
    await _dispatch(b"{}", handler, Ctx([]))
    assert seen == [rid, None]


@pytest.mark.asyncio
async def test_embed_texts_answers_the_model_and_one_vector_per_text(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Epic 54 story 54.21: a plugin's compute.embed, as passages with the search model."""
    from maktaba_pipeline import grpc_server
    from maktaba_pipeline.grpc_server import PipelineService, passages_fn

    class Embedder:
        model_name = "intfloat/multilingual-e5-small"

        def embed_passages(self, texts: list[str]) -> list[list[float]]:
            return [[float(len(t)), 0.5] for t in texts]

    monkeypatch.setattr(grpc_server, "_semantic_on", lambda: True)
    svc = PipelineService(embed_texts=passages_fn(Embedder()))
    out = await svc.embed_texts({"texts": ["ab", "c"]})
    assert out == {"model": "intfloat/multilingual-e5-small", "vectors": [[2.0, 0.5], [1.0, 0.5]]}
    for bad in ({"texts": []}, {"texts": "x"}, {"texts": ["x"] * 65}, {"texts": ["x" * 2001]}):
        with pytest.raises(ValueError):
            await svc.embed_texts(bad)
    monkeypatch.setattr(grpc_server, "_semantic_on", lambda: False)
    assert await svc.embed_texts({"texts": ["a"]}) == {"error": grpc_server.SEMANTIC_OFF}
