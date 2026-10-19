"""A metadata suggestion: fields, each with its value, confidence and the sources it rests on.

The same shape for both providers: ``internal`` (Maktaba's own intel; a source is a kind
of intel such as ``transcript`` or ``book``) and ``web`` (a web search the language model
read; a source is a page's URL). :meth:`Proposal.mapped` is what goes into
``media_metadata_enrichment.mapped``: the flat values the review panel and accept read
(``title``, ``description``, ``year`` ...), plus ``fields`` (per field: value, confidence,
source ids) and ``sources`` (id -> url or intel kind, label).
"""

from __future__ import annotations

import hashlib
import re
import unicodedata
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "FIELDS",
    "FieldValue",
    "Proposal",
    "Source",
    "clean_text",
]

#: every field a suggestion may carry, and its kind
FIELDS: dict[str, str] = {
    "title": "text",
    "series": "text",
    "season": "int",
    "episode": "int",
    "description": "long",
    "presenter": "text",
    "speakers": "list",
    "language": "lang",
    "topic": "text",
    "book_title": "text",
    "book_author": "text",
    "book_subject": "text",
    "book_chapters": "list",
    "year": "int",
}

_LIMITS = {"text": 200, "long": 2000, "list": 120}
_CTRL = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f​-‏‪-‮⁦-⁩]")
_URL = re.compile(r"(?i)\b(?:https?://|www\.)\S+")
_WS = re.compile(r"\s+")
_LANG = re.compile(r"^[a-z]{2,3}(?:-[A-Za-z]{2,4})?$")


def clean_text(value: Any, *, limit: int, keep_newlines: bool = False) -> str:
    """Text safe to store and show: no control or direction-override characters, no
    links (a suggestion never carries a link a page planted), whitespace collapsed, cut
    at ``limit`` on a word."""
    if not isinstance(value, str):
        return ""
    s = unicodedata.normalize("NFC", value)
    s = _CTRL.sub(" ", s)
    s = _URL.sub("", s)
    if keep_newlines:
        s = "\n".join(_WS.sub(" ", line).strip() for line in s.splitlines())
        s = re.sub(r"\n{3,}", "\n\n", s).strip()
    else:
        s = _WS.sub(" ", s).strip()
    if len(s) > limit:
        cut = s[:limit]
        sp = cut.rfind(" ")
        s = (cut[:sp] if sp > limit * 0.6 else cut).rstrip(" ,.;:-") + "…"
    return s


def _norm_value(kind: str, raw: Any) -> Any:
    """The field's value in its kind, or None when it does not fit."""
    if raw is None:
        return None
    if kind == "int":
        if isinstance(raw, bool):
            return None
        if isinstance(raw, int):
            n = raw
        elif isinstance(raw, str) and raw.strip().isdigit():
            n = int(raw.strip())
        elif isinstance(raw, float) and raw.is_integer():
            n = int(raw)
        else:
            return None
        return n if 0 <= n <= 9999 else None
    if kind == "lang":
        s = raw.strip() if isinstance(raw, str) else ""
        return s.lower() if _LANG.match(s) else None
    if kind == "list":
        items = raw if isinstance(raw, list) else []
        out = []
        for it in items[:30]:
            s = clean_text(it, limit=_LIMITS["list"])
            if s and s not in out:
                out.append(s)
        return out or None
    s = clean_text(raw, limit=_LIMITS[kind], keep_newlines=kind == "long")
    return s or None


@dataclass(frozen=True, slots=True)
class Source:
    """Where a field came from: ``url`` for a web page, ``kind`` for Maktaba's intel."""

    id: str
    label: str
    url: str = ""
    kind: str = ""

    def as_json(self) -> dict[str, str]:
        out = {"id": self.id, "label": self.label}
        if self.url:
            out["url"] = self.url
        if self.kind:
            out["kind"] = self.kind
        return out


@dataclass(frozen=True, slots=True)
class FieldValue:
    value: Any
    confidence: float
    sources: tuple[str, ...]


@dataclass
class Proposal:
    """One candidate for a video."""

    provider: str  #: ``internal`` | ``web``
    fields: dict[str, FieldValue] = field(default_factory=dict)
    sources: dict[str, Source] = field(default_factory=dict)
    #: what produced it (query, search backend, model), shown in the panel
    meta: dict[str, Any] = field(default_factory=dict)

    def set(self, name: str, raw: Any, confidence: float, sources: Sequence[str]) -> bool:
        """Set a field after checking it; False (and nothing set) when it does not fit."""
        kind = FIELDS.get(name)
        if kind is None:
            return False
        value = _norm_value(kind, raw)
        if value is None:
            return False
        srcs = tuple(dict.fromkeys(s for s in sources if s in self.sources))
        if not srcs:
            return False
        c = max(0.0, min(1.0, float(confidence)))
        self.fields[name] = FieldValue(value, round(c, 3), srcs)
        return True

    def add_source(self, src: Source) -> str:
        self.sources.setdefault(src.id, src)
        return src.id

    @property
    def empty(self) -> bool:
        return not self.fields

    @property
    def confidence(self) -> float:
        """The suggestion's overall confidence: its title's, else the mean of its fields."""
        if "title" in self.fields:
            return self.fields["title"].confidence
        if not self.fields:
            return 0.0
        return round(sum(f.confidence for f in self.fields.values()) / len(self.fields), 3)

    def external_id(self, video_id: str) -> str:
        """Stable per provider and video: a re-run refreshes the same row."""
        if self.provider == "internal":
            return f"internal:{video_id}"
        h = hashlib.sha256(
            "|".join(
                str(self.fields[k].value) for k in ("title", "series") if k in self.fields
            ).encode()
        ).hexdigest()[:16]
        return f"web:{h}"

    def mapped(self) -> dict[str, Any]:
        used = {s for f in self.fields.values() for s in f.sources}
        out: dict[str, Any] = {k: f.value for k, f in self.fields.items()}
        out["fields"] = {
            k: {"value": f.value, "confidence": f.confidence, "sources": list(f.sources)}
            for k, f in self.fields.items()
        }
        out["sources"] = [s.as_json() for sid, s in self.sources.items() if sid in used]
        out["provider"] = self.provider
        if self.meta:
            out["meta"] = dict(self.meta)
        return out

    @classmethod
    def from_mapped(cls, provider: str, mapped: Mapping[str, Any]) -> Proposal:
        """Back from a stored row (the cache of a model's answer)."""
        p = cls(provider)
        for s in mapped.get("sources") or []:
            if isinstance(s, Mapping) and s.get("id"):
                p.add_source(
                    Source(
                        str(s["id"]),
                        str(s.get("label", "")),
                        str(s.get("url", "")),
                        str(s.get("kind", "")),
                    )  # fmt: skip
                )
        for name, f in (mapped.get("fields") or {}).items():
            if isinstance(f, Mapping):
                p.set(name, f.get("value"), float(f.get("confidence") or 0), f.get("sources") or [])
        p.meta = dict(mapped.get("meta") or {})
        return p
