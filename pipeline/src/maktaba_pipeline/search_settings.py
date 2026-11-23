"""The ``search`` rules: how the Search page's results are grouped, paged and kept, and AI
mode (story 49.12). The pipeline never reads them; they live here because every rules
section's schema (``api/internal/handlers/rules/schema.json``) is generated from one place.
The API keeps the same defaults (``api/internal/handlers/search/rules.go``, checked by a
test)."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["SearchConfig"]

_G = "Search results"
_K = "Search history"
_A = "AI mode"


@dataclass(frozen=True)
class SearchConfig:
    """How search results are grouped per video, paged and kept, and AI mode."""

    SCHEMA_VERSION = (1, 1)

    #: passages shown under each video in "In transcripts"
    passages_per_video: int = 3
    #: videos per page in the grid view
    page_size_grid: int = 24
    #: videos per page in the list view
    page_size_list: int = 20
    #: passages per page in "Show all" and on a video's matches page
    passages_page_size: int = 20
    #: most videos one list of one search keeps
    max_videos: int = 500
    #: passages the meaning (semantic) search asks for
    semantic_top_k: int = 200
    #: a search's results are kept this long after their last use
    result_minutes: int = 15
    #: recent searches kept per user
    recent_limit: int = 20
    #: the AI mode tab answers questions from the library's passages
    ai_mode: bool = True
    #: passages AI mode gives the language model
    ai_passages: int = 30
    #: the language model AI mode asks ("": the tag check's)
    ai_model: str = ""
    #: most seconds one AI mode answer may take
    ai_timeout_s: int = 120


register(
    SearchConfig,
    {
        "passages_per_video": Help(
            _G,
            "Passages under each video",
            'How many of a video\'s best passages the "In transcripts" list shows under it. '
            '"Show all" lists the rest.',
            minimum=1,
            maximum=10,
        ),
        "page_size_grid": Help(
            _G,
            "Videos per page (grid)",
            'How many videos one page of "In titles" or "In transcripts" shows in the grid view.',
            minimum=4,
            maximum=100,
        ),
        "page_size_list": Help(
            _G,
            "Videos per page (list)",
            'How many videos one page of "In titles" or "In transcripts" shows in the list view.',
            minimum=4,
            maximum=100,
        ),
        "passages_page_size": Help(
            _G,
            "Passages per page",
            'How many passages "Show all" and a video\'s matches page load at a time.',
            minimum=5,
            maximum=200,
        ),
        "max_videos": Help(
            _G,
            "Most videos per list",
            "A search keeps at most this many videos in each list, the best matches first. "
            "The match counts of the videos it keeps are always complete.",
            minimum=50,
            maximum=5000,
        ),
        "semantic_top_k": Help(
            _G,
            "Passages a meaning search finds",
            "How many passages the meaning (semantic) search asks the index for before they "
            "are grouped per video. More finds more videos and is slower.",
            minimum=10,
            maximum=200,
        ),
        "result_minutes": Help(
            _K,
            "Keep results for (minutes)",
            "A search's results are kept for this many minutes after their last use, so "
            "their order stays the same while you page through them or come back from a "
            "video. Every page you open starts the time again; after that the search runs "
            "again.",
            minimum=1,
            maximum=1440,
        ),
        "recent_limit": Help(
            _K,
            "Recent searches kept",
            "How many of each user's searches the Recent searches list keeps. The query "
            "stays after its results are no longer kept; opening it searches again.",
            minimum=1,
            maximum=200,
        ),
        "ai_mode": Help(
            _A,
            "AI mode",
            "The Search page's AI mode tab: the home's language model reads the best passages "
            "for a question and answers from them, each sentence linked to its video and "
            "moment. Off: the tab says an admin turned it off.",
        ),
        "ai_passages": Help(
            _A,
            "Passages AI mode reads",
            "How many of the best passages, with the lines around each, the language model "
            "is given for one question. More can answer more and is slower.",
            minimum=5,
            maximum=60,
        ),
        "ai_model": Help(
            _A,
            "AI mode's language model",
            "The model AI mode asks, on the tag check's model server. Empty: the tag check's "
            "model.",
        ),
        "ai_timeout_s": Help(
            _A,
            "Longest answer (seconds)",
            "An answer still being written after this long stops where it is.",
            minimum=10,
            maximum=600,
        ),
    },
)
