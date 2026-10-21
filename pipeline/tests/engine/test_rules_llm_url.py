"""The language model server the supervisor hands every role (``MAKTABA_OLLAMA_URL``:
Maktaba.app's bundled Ollama on its own port, or ``[llm] url``) is the model server of
every LLM stage (``rules.tagcheck.url``) unless the rules name one."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.classify.tag_check import TagCheckConfig
from maktaba_pipeline.engine.sql import dumps, now, to_ts
from maktaba_pipeline.enrich.config import EnrichConfig
from maktaba_pipeline.enrich.providers import pick_reader
from maktaba_pipeline.enrich.reader import OpenAICompatReader
from maktaba_pipeline.runtime import Database

BUNDLED = "http://127.0.0.1:11533"


async def _set(db: Any, section: str, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        f"rules.{section}",
        dumps(value),
        to_ts(db, now()),
    )


def test_env_is_the_default_and_a_set_url_wins() -> None:
    env = {"MAKTABA_OLLAMA_URL": BUNDLED + "/"}
    cfg, bad = rules.config("tagcheck", {}, env)
    assert cfg.url == BUNDLED and bad == {}
    cfg, _ = rules.config("tagcheck", {"model": "llama3.2:3b"}, env)
    assert cfg.url == BUNDLED and cfg.model == "llama3.2:3b"
    # the user's own server always wins
    cfg, _ = rules.config("tagcheck", {"url": "http://localhost:11434"}, env)
    assert cfg.url == "http://localhost:11434"
    # no variable (dev stack, tests): the built-in default
    cfg, _ = rules.config("tagcheck", {}, {})
    assert cfg.url == TagCheckConfig().url
    cfg, _ = rules.config("tagcheck", {}, {"MAKTABA_OLLAMA_URL": "  "})
    assert cfg.url == TagCheckConfig().url
    # other sections are untouched
    assert rules.config("planner", {}, env)[0] == rules.SECTIONS["planner"][0]()


@pytest.mark.asyncio
async def test_every_llm_stage_reads_the_handed_url(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_OLLAMA_URL", BUNDLED)
    # check-tags / check-clips / display titles read rules.tagcheck
    cfg, changes, _ = await rules.read(db, "tagcheck")
    assert cfg.url == BUNDLED and changes == {}
    # enrich's reader asks the same server
    reader = await pick_reader(db, EnrichConfig())
    assert isinstance(reader, OpenAICompatReader) and reader.url == BUNDLED
    # a server set in Settings wins
    await _set(db, "tagcheck", {"url": "http://192.168.1.30:11434"})
    cfg, _, _ = await rules.read(db, "tagcheck")
    assert cfg.url == "http://192.168.1.30:11434"
