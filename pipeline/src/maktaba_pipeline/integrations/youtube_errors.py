"""What a yt-dlp failure means for one download (Epic 47, story 47.8).

Ported from Make-It-Offline's ``DownloadService.ParseYtDlpError`` as an ordered list of
regular expressions: the first rule that matches yt-dlp's output wins. Each rule says what
happens to the item (``youtube_downloads``) and to the job:

========================  ================  ==============================  ===============
yt-dlp says               action            item                            job
========================  ================  ==============================  ===============
not a bot / sign in       ``bot_check``     stays ``wanted``                parks on
                                                                            ``dependency:youtube``
HTTP 429                  ``rate_limited``  ``wanted``, ``not_before``      parks >= 1 h
members-only, private,    ``skipped``       ``skipped`` with the reason     continues
age-gated, login, geo,
live/upcoming
removed, unavailable      ``gone``          ``gone``                        continues
network, timeout, 5xx     ``transient``     retried in the job, up to 5     continues
format not available      ``format``        retried once with ``-f b``      continues
anything else             ``unknown``       ``failed``                      continues
========================  ================  ==============================  ===============

The order matters: YouTube says "Video unavailable. This video is private", which is a
private video (skipped), not a removed one (gone).
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Literal

__all__ = [
    "BOT_CHECK_KIND",
    "RATE_LIMITED_KIND",
    "RULES",
    "Action",
    "Classified",
    "classify",
    "error_lines",
]

Action = Literal["bot_check", "rate_limited", "skipped", "gone", "transient", "format", "unknown"]

#: the job's failure kinds (``StageFailure.kind``) for the two that stop a job
BOT_CHECK_KIND = "youtube_bot_check"
RATE_LIMITED_KIND = "youtube_rate_limited"


@dataclass(frozen=True, slots=True)
class Rule:
    name: str
    action: Action
    pattern: re.Pattern[str]


def _rule(name: str, action: Action, pattern: str) -> Rule:
    return Rule(name, action, re.compile(pattern, re.IGNORECASE))


#: first match wins
RULES: tuple[Rule, ...] = (
    _rule(
        "bot_check",
        "bot_check",
        r"not a bot|sign[ _]in[ _]to[ _]confirm(?![ _]your[ _]age)|روبوت",
    ),
    _rule("rate_limited", "rate_limited", r"\b429\b|too many requests|rate[- ]?limit"),
    _rule(
        "members_only",
        "skipped",
        r"members[- ]only|join this channel|available to this channel'?s members"
        r"|subscriber_only|premium_only|youtube premium",
    ),
    _rule("private", "skipped", r"private video|video is private|needs_auth"),
    _rule(
        "age_gate",
        "skipped",
        r"confirm your age|age[- ]restricted|inappropriate for some users|age[- ]gate",
    ),
    _rule(
        "login",
        "skipped",
        r"login required|log in to|sign in to view|requires? (?:a )?(?:log ?in|sign[- ]?in)",
    ),
    _rule(
        "geo",
        "skipped",
        r"not available in your country|geo[- ]?(?:restrict|block)"
        r"|made this video available in your country",
    ),
    _rule(
        "live",
        "skipped",
        r"does not pass filter|live event will begin|premieres? in|is_upcoming"
        r"|this live stream recording is not available",
    ),
    _rule(
        "format",
        "format",
        r"requested format (?:is )?not available|format not available|no suitable format",
    ),
    _rule(
        "removed",
        "gone",
        r"video (?:is )?unavailable|has been removed|no longer available|has been deleted"
        r"|account .*terminated|has been terminated|does not exist|\b404\b"
        r"|copyright (?:claim|grounds)|violat\w* youtube",
    ),
    _rule(
        "network",
        "transient",
        r"unable to download|connection|timed? ?out|timeout|network|socket|\bssl\b"
        r"|certificate|temporary failure|http error 5\d\d|\b50[0-4]\b|remote end closed"
        r"|reset by peer|incomplete ?read|errno (?:54|60|65|104|110|111)",
    ),
    _rule(
        "extractor",
        "unknown",
        r"unsupported url|no video formats found|unable to extract|extractor error",
    ),
)


@dataclass(frozen=True, slots=True)
class Classified:
    """One failure: the action, the rule's name (the item's ``reason``) and yt-dlp's line."""

    action: Action
    reason: str
    message: str

    @property
    def item_state(self) -> str:
        """The ``youtube_downloads.state`` the item takes (``wanted`` while it may still
        download; ``failed`` for a spent retry or an unknown error)."""
        return {
            "skipped": "skipped",
            "gone": "gone",
            "unknown": "failed",
        }.get(self.action, "wanted")


def error_lines(stderr: str) -> list[str]:
    """yt-dlp's ``ERROR:`` lines, or its last non-empty lines when it printed none."""
    lines = [ln.strip() for ln in stderr.splitlines() if ln.strip()]
    errors = [ln for ln in lines if "ERROR" in ln]
    return errors or lines[-3:]


def classify(output: str) -> Classified:
    """Classify yt-dlp's output (its stderr, with stdout when useful)."""
    lines = error_lines(output)
    text = " ".join(lines) if lines else output
    message = (lines[-1] if lines else output.strip())[:500]
    for rule in RULES:
        if rule.pattern.search(text):
            return Classified(rule.action, rule.name, message)
    return Classified("unknown", "error", message or "yt-dlp failed without a message")
