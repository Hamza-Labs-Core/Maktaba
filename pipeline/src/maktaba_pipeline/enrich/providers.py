"""Which search backend and which model the provider uses, from the settings.

* the search: ``rules.enrich.search`` and the **Web search** extension (``extensions`` row
  ``web_search``: enabled, ``google_cx``; the keys ``ext.web_search.google_api_key`` and
  ``ext.web_search.ollama_api_key`` sealed in ``home_secrets``, so only the home reads
  them);
* the model: ``rules.enrich.llm``: the tag check's Ollama server (``rules.tagcheck.url``
  and ``model``, what classification already uses) or a model of the cluster (the LLM
  door), or none.

:func:`configured_providers` says what is configured (the enqueue gating and the
summary); Maktaba's own intel always is.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from ..engine.sql import loads
from ..home_secrets import extension_secret_name, get_secret
from .config import EnrichConfig
from .gating import INTERNAL_PROVIDER, ProviderKey
from .reader import ClusterReader, LLMClient, OpenAICompatReader
from .service import MetadataEnrichService
from .web import (
    GoogleSearch,
    OllamaWebSearch,
    SearchBackend,
    Transport,
    public_address,
    urllib_transport,
)

__all__ = [
    "WEB_SEARCH_EXTENSION",
    "WebSearchSettings",
    "build_service",
    "configured_providers",
    "load_web_search",
    "pick_reader",
    "pick_search",
    "use_network",
]

WEB_SEARCH_EXTENSION = "web_search"

#: the network the stage's provider uses: the real one, or a test's fakes
_NET: dict[str, Any] = {"transport": urllib_transport, "resolve": public_address}


def use_network(
    transport: Transport | None = None, resolve: Callable[[str], bool] | None = None
) -> None:
    """Swap the transport (searches, pages, the model server) and the address check the
    ``enrich`` stage uses; ``use_network()`` puts the real ones back. For tests: no test
    may reach Google, Ollama, a model or a page."""
    _NET["transport"] = transport or urllib_transport
    _NET["resolve"] = resolve or public_address


@dataclass(frozen=True, slots=True)
class WebSearchSettings:
    enabled: bool = False
    google_api_key: str = ""
    google_cx: str = ""
    ollama_api_key: str = ""

    @property
    def google(self) -> bool:
        return self.enabled and bool(self.google_api_key and self.google_cx)

    @property
    def ollama(self) -> bool:
        return self.enabled and bool(self.ollama_api_key)

    def __repr__(self) -> str:  # never the keys
        return (f"WebSearchSettings(enabled={self.enabled}, google={self.google}, "
                f"ollama={self.ollama})")  # fmt: skip


async def load_web_search(db: Any) -> WebSearchSettings:
    try:
        row = await db.fetchrow(
            "SELECT enabled, settings FROM extensions WHERE name = $1", WEB_SEARCH_EXTENSION
        )
    except Exception:  # noqa: BLE001 - an old schema: no extension, internal intel only
        return WebSearchSettings()
    if row is None:
        return WebSearchSettings()
    settings = loads(row["settings"]) or {}
    g = await get_secret(db, extension_secret_name(WEB_SEARCH_EXTENSION, "google_api_key"))
    o = await get_secret(db, extension_secret_name(WEB_SEARCH_EXTENSION, "ollama_api_key"))
    return WebSearchSettings(
        enabled=bool(row["enabled"]),
        google_api_key=(g or "").strip(),
        google_cx=str(settings.get("google_cx") or "").strip(),
        ollama_api_key=(o or "").strip(),
    )


def pick_search(
    cfg: EnrichConfig, ws: WebSearchSettings, *, transport: Transport = urllib_transport
) -> SearchBackend | None:
    want = cfg.search
    if want in ("auto", "google") and ws.google:
        return GoogleSearch(ws.google_api_key, ws.google_cx, transport=transport)
    if want in ("auto", "ollama") and ws.ollama:
        return OllamaWebSearch(ws.ollama_api_key, transport=transport)
    return None


async def pick_reader(
    db: Any, cfg: EnrichConfig, *, transport: Transport = urllib_transport
) -> LLMClient | None:
    if cfg.llm == "off":
        return None
    from ..rules import read as read_rules

    tag, _, _ = await read_rules(db, "tagcheck")
    if cfg.llm == "cluster":
        return ClusterReader(cfg.model or tag.model)
    if not tag.url or not tag.model:
        return None
    return OpenAICompatReader(tag.url, tag.model, transport=transport)


async def configured_providers(db: Any, cfg: EnrichConfig) -> list[ProviderKey]:
    ws = await load_web_search(db)
    return [
        INTERNAL_PROVIDER,
        ProviderKey("google", cfg.search in ("auto", "google") and ws.google),
        ProviderKey("ollama", cfg.search in ("auto", "ollama") and ws.ollama),
        ProviderKey("llm", cfg.llm != "off"),
    ]


async def build_service(
    db: Any, cfg: EnrichConfig, *, transport: Transport | None = None
) -> MetadataEnrichService:
    t: Transport = transport or _NET["transport"]
    ws = await load_web_search(db)
    return MetadataEnrichService(
        cfg,
        search=pick_search(cfg, ws, transport=t),
        reader=await pick_reader(db, cfg, transport=t),
        transport=t,
        resolve=_NET["resolve"],
    )
