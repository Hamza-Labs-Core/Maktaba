"""The metadata provider: one video's suggestions, from its intel and (optionally) the web.

:class:`MetadataEnrichService` is the Story 26.5 service the enrich worker runs
(:class:`~maktaba_pipeline.enrich.worker.EnrichService`). Per video:

1. gather the internal intel (:mod:`.intel`) and write the **internal** suggestion
   (:mod:`.internal`); always, so a search never answers with nothing;
2. when a language model is configured: build a search query from the intel (or take the
   librarian's words), search the web when a backend is configured, read the top pages,
   and have the model propose the fields from that evidence (:mod:`.reader`); the checked
   answer is the **web** suggestion (``llm`` when no web result was read). Searches,
   pages and answers are cached; each call is counted against the daily caps
   (:mod:`.budget`).

Nothing is applied to the video: the suggestions wait in ``media_metadata_enrichment``
for an accept in the review panel (Story 26.6). Over a daily cap, the internal suggestion
is written and :class:`~.worker.RateLimited` defers the job to the next UTC day; a search
backend's own quota defers it an hour. A backend or model that fails is reported in the
result and the internal suggestion stands.
"""

from __future__ import annotations

import asyncio
import time
import uuid
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from ..engine.sql import dumps, is_pg, now, to_ts
from ..log import get_logger
from .budget import Budget, BudgetExhausted, seconds_to_next_day
from .cache import cache_get, cache_key, cache_put
from .config import EnrichConfig
from .intel import VideoIntel, gather_intel
from .internal import internal_proposal
from .proposal import Proposal, clean_text
from .reader import (
    Evidence,
    LLMBadAnswer,
    LLMClient,
    LLMUnavailable,
    build_messages,
    parse_answer,
)
from .web import (
    SearchBackend,
    SearchFailed,
    SearchRateLimited,
    SearchResult,
    Transport,
    public_address,
    read_page,
    results_from_json,
    results_json,
    urllib_transport,
)
from .worker import RateLimited

__all__ = ["EnrichResult", "MetadataEnrichService", "build_query"]

_log = get_logger(component="enrich_service")

#: seconds a search backend's own quota or rate limit makes the job wait
BACKEND_RETRY_SEC = 3600.0
QUERY_CHARS = 150

_last_search = 0.0
_search_lock = asyncio.Lock()


@dataclass
class EnrichResult:
    """What one run did (the engine job's summary)."""

    internal: bool = False
    web_fields: int = 0
    provider: str = ""
    query: str = ""
    search: str = ""
    model: str = ""
    results: int = 0
    pages: int = 0
    cached: list[str] = field(default_factory=list)
    #: why the web part did not run or stopped (``no_model``, ``budget:search`` ...)
    skipped: str = ""

    def as_json(self) -> dict[str, Any]:
        return {k: v for k, v in self.__dict__.items() if v not in ("", 0, [], None)} | {
            "internal": self.internal
        }


def build_query(intel: VideoIntel, *, query: str | None = None, year: int | None = None) -> str:
    """The web search's words: the librarian's, else the intel's names (title, series,
    the book, the presenter), without file leftovers."""
    if query and query.strip():
        q = clean_text(query, limit=QUERY_CHARS)
        return f"{q} {year}" if year and str(year) not in q else q
    parts: list[str] = []

    def add(s: str) -> None:
        s = clean_text(s, limit=80)
        if s and not any(s in p or p in s for p in parts):
            parts.append(s)

    add(intel.series)
    add(intel.best_title)
    if intel.book is not None:
        add(intel.book.title or intel.book.name)
    if intel.speakers:
        add(intel.speakers[0])
    elif intel.yt_channel:
        add(intel.yt_channel)
    q = " ".join(parts)
    if year:
        q += f" {year}"
    return q[:QUERY_CHARS].strip()


class MetadataEnrichService:
    def __init__(
        self,
        cfg: EnrichConfig,
        *,
        search: SearchBackend | None,
        reader: LLMClient | None,
        transport: Transport = urllib_transport,
        resolve: Callable[[str], bool] = public_address,
        budget: Callable[[Any], Budget] | None = None,
        sleep: Callable[[float], Any] = asyncio.sleep,
    ) -> None:
        self.cfg = cfg
        self.search = search
        self.reader = reader
        self.transport = transport
        self.resolve = resolve
        self._budget = budget or (lambda db: Budget(db, cfg))
        self.sleep = sleep
        #: the last run's result (the stage reads it for its summary)
        self.last: EnrichResult | None = None

    async def enrich_video(
        self,
        conn: Any,
        video_id: UUID,
        *,
        force: bool,
        query: str | None = None,
        year: int | None = None,
    ) -> EnrichResult:
        res = EnrichResult()
        self.last = res
        intel = await gather_intel(conn, video_id)
        internal = internal_proposal(intel, query=query or "")
        await _write(conn, video_id, internal)
        res.internal = True
        if self.reader is None:
            res.skipped = "no_model"
            return res
        budget = self._budget(conn)
        q = build_query(intel, query=query, year=year)
        res.query, res.model = q, self.reader.name
        evidence = _intel_evidence(intel)
        web: list[Evidence] = []
        try:
            if self.search is not None and q:
                res.search = self.search.name
                results = await self._search(conn, budget, q, intel.language, res)
                res.results = len(results)
                web = await self._read(conn, budget, results, res)
            elif self.search is None:
                res.skipped = "no_search"
            proposal = await self._ask(conn, budget, [*evidence, *web], q, res,
                                       provider="web" if web else "llm")  # fmt: skip
        except BudgetExhausted as exc:
            res.skipped = f"budget:{exc.kind}"
            raise RateLimited(f"budget:{exc.kind}",
                              retry_after_sec=seconds_to_next_day(now())) from None  # fmt: skip
        except SearchRateLimited as exc:
            res.skipped = "search_rate_limited"
            raise RateLimited(str(exc), retry_after_sec=BACKEND_RETRY_SEC) from None
        except (SearchFailed, LLMUnavailable, LLMBadAnswer) as exc:
            res.skipped = f"{type(exc).__name__}: {exc}"[:300]
            _log.info("enrich_web_skipped", video_id=str(video_id), reason=res.skipped)
            return res
        if proposal is None or proposal.empty:
            res.skipped = res.skipped or "no_answer"
            return res
        proposal.meta = {"query": q, "search": res.search, "model": self.reader.name}
        await _write(conn, video_id, proposal)
        await _drop_stale(conn, video_id, keep=proposal.external_id(str(video_id)))
        res.web_fields = len(proposal.fields)
        res.provider = proposal.provider
        return res

    async def _search(
        self, db: Any, budget: Budget, q: str, lang: str, res: EnrichResult
    ) -> list[SearchResult]:
        assert self.search is not None
        key = cache_key("search", self.search.name, q, lang, str(self.cfg.results))
        hit = await cache_get(db, key)
        if hit is not None:
            res.cached.append("search")
            return results_from_json(hit)
        await budget.check("search", self.cfg.search_usd)
        await self._gap()
        results = await self.search.search(
            q, num=self.cfg.results, language=lang, timeout=self.cfg.timeout_s
        )
        await budget.spend("search", self.cfg.search_usd)
        await cache_put(db, key, self.search.name, results_json(results), days=self.cfg.cache_days)
        return results

    async def _gap(self) -> None:
        global _last_search
        async with _search_lock:
            wait = self.cfg.min_search_gap_s - (time.monotonic() - _last_search)
            if wait > 0:
                await self.sleep(wait)
            _last_search = time.monotonic()

    async def _read(
        self, db: Any, budget: Budget, results: list[SearchResult], res: EnrichResult
    ) -> list[Evidence]:
        out: list[Evidence] = []
        for i, r in enumerate(results, 1):
            text = r.snippet
            title = r.title
            if r.content:
                text = f"{r.snippet}\n{r.content[: self.cfg.page_chars]}"
            elif i <= self.cfg.read_pages:
                page = await self._page(db, budget, r.url, res)
                if page is not None:
                    title = page[0] or title
                    text = f"{r.snippet}\n{page[1]}" if page[1] else r.snippet
            out.append(Evidence(f"S{i}", title or r.url, text, url=r.url, kind="web"))
        return out

    async def _page(
        self, db: Any, budget: Budget, url: str, res: EnrichResult
    ) -> tuple[str, str] | None:
        key = cache_key("page", url, str(self.cfg.page_chars))
        hit = await cache_get(db, key)
        if isinstance(hit, list) and len(hit) == 2:
            res.cached.append("page")
            return str(hit[0]), str(hit[1])
        await budget.check("page")
        page = await read_page(url, transport=self.transport, timeout=self.cfg.timeout_s,
                               chars=self.cfg.page_chars, resolve=self.resolve)  # fmt: skip
        await budget.spend("page")
        if page is None:
            return None
        res.pages += 1
        await cache_put(db, key, "page", list(page), days=self.cfg.cache_days)
        return page

    async def _ask(
        self,
        db: Any,
        budget: Budget,
        evidence: list[Evidence],
        q: str,
        res: EnrichResult,
        *,
        provider: str,
    ) -> Proposal | None:
        assert self.reader is not None
        messages = build_messages(evidence, query=q)
        key = cache_key("llm", self.reader.name, dumps(messages))
        hit = await cache_get(db, key)
        if isinstance(hit, str):
            res.cached.append("llm")
            text = hit
        else:
            est = sum(len(m["content"]) for m in messages) // 3 + 500
            await budget.check("llm", budget.llm_usd(est))
            answer = await self.reader.complete(messages, timeout=self.cfg.llm_timeout_s)
            await budget.spend("llm", budget.llm_usd(answer.tokens))
            text = answer.text
            await cache_put(db, key, "llm", text, days=self.cfg.cache_days)
        return parse_answer(text, evidence, min_confidence=self.cfg.min_confidence,
                            provider=provider)  # fmt: skip


def _intel_evidence(intel: VideoIntel) -> list[Evidence]:
    return [
        Evidence(f"I{i}", label, text[:2500], kind=kind)
        for i, (kind, label, text) in enumerate(intel.summary_lines(), 1)
    ]


async def _write(db: Any, video_id: UUID, p: Proposal) -> None:
    cast = "::jsonb" if is_pg(db) else ""
    ts = to_ts(db, now())
    await db.execute(
        "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
        f" confidence, is_accepted, fetched_at, created_at) VALUES ($1,$2,$3,$4,$5{cast},$6,"
        "false,$7,$7) ON CONFLICT (video_id, provider, external_id) DO UPDATE SET"
        " mapped = EXCLUDED.mapped, confidence = EXCLUDED.confidence,"
        " fetched_at = EXCLUDED.fetched_at",
        str(uuid.uuid4()),
        str(video_id),
        p.provider,
        p.external_id(str(video_id)),
        dumps(p.mapped()),
        p.confidence,
        ts,
    )


async def _drop_stale(db: Any, video_id: UUID, *, keep: str) -> None:
    """A new model answer replaces the earlier ones that were not accepted."""
    await db.execute(
        "DELETE FROM media_metadata_enrichment WHERE video_id = $1 AND provider IN ('web', 'llm')"
        " AND external_id <> $2 AND is_accepted = false",
        str(video_id),
        keep,
    )
