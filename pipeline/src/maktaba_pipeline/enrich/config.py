"""``rules.enrich``: how "Find metadata" and "Re-enrich" find a video's metadata.

No outside catalogue (TMDB, TVDB, OMDb): candidates come from Maktaba's own intel and,
when configured, from a web search a language model reads. The defaults keep today's
behaviour where there was one (nothing is enriched by itself: ``auto`` is off) and cap
what a day may spend.
"""

from __future__ import annotations

from dataclasses import dataclass

__all__ = ["LLM_CHOICES", "SEARCH_CHOICES", "EnrichConfig"]

#: where the web results come from: ``auto`` is Google when its key and engine id are
#: saved, else Ollama's web search when its key is, else none (internal intel only)
SEARCH_CHOICES = ("auto", "google", "ollama", "off")
#: which model reads them: the tag check's Ollama server (what classification uses),
#: any model of the cluster (the LLM door), or none
LLM_CHOICES = ("tagcheck", "cluster", "off")


@dataclass(frozen=True, slots=True)
class EnrichConfig:
    #: new videos get metadata suggestions by themselves (a few per sweep); off: only
    #: when someone asks (Find metadata, Re-enrich)
    auto: bool = False
    #: videos picked per 30 s sweep when ``auto`` is on
    auto_batch: int = 5
    search: str = "auto"
    llm: str = "tagcheck"
    #: the cluster model (``llm = cluster``); empty: the tag check's model
    model: str = ""
    #: web results read per search, and pages of them opened and read
    results: int = 5
    read_pages: int = 3
    #: characters of each page shown to the model
    page_chars: int = 4000
    #: a suggestion below this confidence is not kept
    min_confidence: float = 0.35
    #: daily caps (UTC day); over a cap the web part waits for the next day and the
    #: internal suggestion is still written
    max_searches_per_day: int = 100
    max_pages_per_day: int = 300
    max_llm_calls_per_day: int = 200
    max_usd_per_day: float = 1.0
    #: what one call costs, for the dollar cap (Google: $5 per 1000 queries past the free
    #: 100 a day; a local model: nothing)
    search_usd: float = 0.005
    llm_usd_per_1k_tokens: float = 0.0
    #: seconds between two search calls
    min_search_gap_s: float = 1.0
    #: days a search, a page or an answer is reused from the cache
    cache_days: int = 30
    timeout_s: float = 30.0
    #: the model's answer timeout (a local 7B model on a long prompt is slow)
    llm_timeout_s: float = 180.0
    #: a waiting match at or above this confidence (0-1) is accepted by itself, by the
    #: API's review-queue sweep (api/internal/handlers/enrichment/autoaccept.go); 0: off
    auto_accept_threshold: float = 0.0


def _register_help() -> None:
    from maktaba_pipeline.settings import Help, register

    g = "Finding metadata"
    register(EnrichConfig, {
        "auto": Help(g, "Find metadata for new videos by themselves", "Off: only when "
                     "someone presses Find metadata or Re-enrich. On: a few videos without "
                     "suggestions per sweep, within the daily caps."),
        "auto_batch": Help(g, "Videos per sweep (automatic)", minimum=1),
        "search": Help(g, "Web search", "auto: Google Programmable Search when its key and "
                       "engine id are saved (extension Web search), else Ollama's web search "
                       "when its key is; off: Maktaba's own intel only (transcripts, "
                       "chapters, the book, file and folder names, sibling episodes).",
                       choices=SEARCH_CHOICES),
        "llm": Help(g, "Language model", "tagcheck: the Ollama server and model of the tag "
                    "check; cluster: a model any machine of the cluster serves; off: no "
                    "model (no web suggestion, internal intel only).", choices=LLM_CHOICES),
        "model": Help(g, "Cluster model", "With language model = cluster. Empty: the tag "
                      "check's model."),
        "results": Help(g, "Web results per search", minimum=1, maximum=10),
        "read_pages": Help(g, "Pages read per search", "Of the results, the pages opened "
                           "and shown to the model (0: the result snippets only).",
                           minimum=0, maximum=10),
        "page_chars": Help(g, "Characters read per page", minimum=200),
        "min_confidence": Help(g, "Keep suggestions from (confidence)", minimum=0,
                               maximum=1),
        "max_searches_per_day": Help(g, "Searches per day (most)", minimum=0),
        "max_pages_per_day": Help(g, "Pages read per day (most)", minimum=0),
        "max_llm_calls_per_day": Help(g, "Model questions per day (most)", minimum=0),
        "max_usd_per_day": Help(g, "Spend per day (most, $)", "Searches and model tokens "
                                "at the prices below. Over a cap the web part waits for "
                                "the next day; the internal suggestion is still made.",
                                minimum=0),
        "search_usd": Help(g, "Price of one search ($)", minimum=0),
        "llm_usd_per_1k_tokens": Help(g, "Price of 1000 model tokens ($)", "0 for a local "
                                      "model.", minimum=0),
        "min_search_gap_s": Help(g, "Seconds between two searches", minimum=0),
        "cache_days": Help(g, "Reuse searches and answers for (days)", minimum=0),
        "timeout_s": Help(g, "Search and page timeout (s)", minimum=1),
        "llm_timeout_s": Help(g, "Model answer timeout (s)", minimum=1),
        "auto_accept_threshold": Help(g, "Accept matches by themselves from (confidence)",
                                      "A match waiting in the review queue whose "
                                      "confidence is at least this (0 to 1, 0.9 is 90%) is "
                                      "accepted without a person, checked every minute. "
                                      "Fields you edited yourself are never replaced, and "
                                      "an accepted match can be reverted to the original. "
                                      "0: off, every match waits for review.",
                                      minimum=0, maximum=1),
    })  # fmt: skip


_register_help()
