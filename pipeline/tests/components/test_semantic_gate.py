"""Embed and SemanticSearch follow ``search.semantic``'s gate (Epic 51, story 51.06)."""

from __future__ import annotations

import asyncio
import json
from typing import Any

import pytest

from maktaba_pipeline.components import runner as runner_mod
from maktaba_pipeline.components.runner import ComponentRunner
from maktaba_pipeline.components.stages import gates
from maktaba_pipeline.components.store import Desired
from maktaba_pipeline.grpc_server import SEMANTIC_OFF, PipelineService, _dispatch


async def _embed(_text: str) -> list[float]:
    return [1.0, 0.0]


def _call(method: Any, payload: dict[str, Any]) -> dict[str, Any]:
    out: dict[str, Any] = json.loads(asyncio.run(_dispatch(json.dumps(payload).encode(), method)))
    return out


def test_semantic_methods_answer_off_while_the_component_is_off(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    service = PipelineService(embedder=_embed)
    assert _call(service.embed, {"text": "x"}) == {"vector": [1.0, 0.0]}  # no runner: on

    r = ComponentRunner(None, reconcile=True)
    r.gates = gates(
        {"search.semantic": Desired(id="search.semantic", want="installed", generation=1)}
    )
    monkeypatch.setattr(runner_mod, "_ACTIVE", r)
    assert _call(service.embed, {"text": "x"}) == {"error": SEMANTIC_OFF}
    assert _call(service.semantic_search, {"q": "x"}) == {"error": SEMANTIC_OFF}

    r.reconcile = False  # the kill switch: as before
    assert _call(service.embed, {"text": "x"}) == {"vector": [1.0, 0.0]}
