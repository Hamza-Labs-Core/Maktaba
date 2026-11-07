"""The shared redaction list (``shared/redact/rules.json``, Epic 53 story 53.01).

Every Maktaba logger loads the same rules (``redact_rules.json`` here is the copy
``tools/log-schema/sync.py`` writes) and passes the same cases
(``shared/redact/cases.json``). The patterns are written so Go, Python, JavaScript,
ICU, Java and Rust read them alike; a replacement names its groups as ``$1``.
"""

from __future__ import annotations

import hashlib
import hmac
import json
import os
import re
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

__all__ = [
    "LOG_KEY_ENV",
    "LOG_KEY_FILE",
    "LOG_KEY_SIZE",
    "RULES",
    "Rules",
    "hash_user",
    "load_rules",
    "scrub",
    "set_user_hash_key",
    "user_hash",
]

LOG_KEY_ENV = "MAKTABA_LOG_KEY"
LOG_KEY_FILE = "log.key"
LOG_KEY_SIZE = 32

_GROUP_RE = re.compile(r"\$(\d)")


@dataclass(frozen=True)
class _Rule:
    regex: re.Pattern[str]
    replace: str
    needs: tuple[str, ...]
    ignore_case: bool

    def applies(self, text: str, low: str) -> bool:
        if not self.needs:
            return True
        hay = low if self.ignore_case else text
        # a loop, not any() over a generator: half the cost (issue #744)
        for n in self.needs:  # noqa: SIM110
            if n in hay:
                return True
        return False


@dataclass(frozen=True)
class Rules:
    key_mask: str
    value_mask: str
    keys: frozenset[str]
    key_suffixes: tuple[str, ...]
    key_parts: tuple[str, ...]
    values: tuple[_Rule, ...]
    cards: re.Pattern[str]
    card_first: str
    card_min: int
    card_max: int
    card_replace: str
    ipv4: re.Pattern[str]
    ipv4_keep: frozenset[str]
    ipv6: re.Pattern[str]
    raw: dict[str, Any] = field(repr=False, compare=False, default_factory=dict)
    # every value rule's literal needs, case folded (a superset), or None when a rule has
    # none: a value that contains none of them, with too few digits, dots and colons for a
    # card or an address, has nothing to scrub (most values: the rules' loop alone was ~2 µs
    # a value, Epic 53 story 53.13). ``in`` on the lowered text: a regex alternation of the
    # needs, case-insensitive, cost ~1 µs on a 12-character value (issue #744)
    needs_lower: tuple[str, ...] | None = field(repr=False, compare=False, hash=False, default=None)
    # each key's answer, kept on the instance: an lru_cache keyed on the rules hashed every
    # field of this dataclass on every call (most of a log call's cost, Epic 53 story 53.13)
    _keys_seen: dict[tuple[str, bool], bool] = field(
        repr=False, compare=False, hash=False, default_factory=dict
    )

    def key_sensitive(self, key: str, is_string: bool) -> bool:
        low = key.lower()
        hit = self._keys_seen.get((low, is_string))
        if hit is None:
            hit = _key_sensitive(self, low, is_string)
            if len(self._keys_seen) < 4096:
                self._keys_seen[(low, is_string)] = hit
        return hit

    def scrub(self, text: str) -> str:
        if not text:
            return text
        low = text.lower()
        if (
            self.needs_lower is not None
            and not _contains_any(low, self.needs_lower)
            and text.count(".") < 3
            and text.count(":") < 3
            and (len(text) < self.card_min or sum(map(str.isdigit, text)) < self.card_min)
        ):
            return text
        out = text
        for rule in self.values:
            if rule.applies(out, low):
                new = rule.regex.sub(rule.replace, out)
                if new != out:
                    out, low = new, new.lower()
        digits = sum(map(str.isdigit, out))
        if digits >= self.card_min:
            out = _replace_checked(out, self.cards, self._card)
        if out.count(".") >= 3 and digits >= 4:
            out = _replace_checked(out, self.ipv4, self._v4)
        if out.count(":") >= 3:
            out = _replace_checked(out, self.ipv6, _v6)
        return out if out != text else text

    def _card(self, m: str, before: str, after: str) -> str:
        if _is_digit(before) or _is_digit(after):
            return m
        digits = [c for c in m if _is_digit(c)]
        if not (self.card_min <= len(digits) <= self.card_max) or digits[0] not in self.card_first:
            return m
        return self.card_replace if _luhn(digits) else m

    def _v4(self, m: str, before: str, after: str) -> str:
        # part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
        if _is_digit(before) or before == "." or _is_digit(after):
            return m
        parts = m.split(".")
        if any(int(p) > 255 for p in parts) or parts[0] in self.ipv4_keep:
            return m
        return f"{parts[0]}.{parts[1]}.{parts[2]}.0"


def _key_sensitive(rules: Rules, low: str, is_string: bool) -> bool:
    if low in rules.keys or any(low.endswith(s) for s in rules.key_suffixes):
        return True
    return is_string and any(p in low for p in rules.key_parts)


def _contains_any(low: str, needs: tuple[str, ...]) -> bool:
    # a loop, not any() over a generator: half the cost (issue #744)
    for n in needs:  # noqa: SIM110
        if n in low:
            return True
    return False


def _is_digit(c: str) -> bool:
    return "0" <= c <= "9" if c else False


def _is_hex(c: str) -> bool:
    return bool(c) and (c in "0123456789abcdefABCDEF")


def _luhn(digits: list[str]) -> bool:
    total = 0
    for i, d in enumerate(reversed(digits)):
        n = int(d)
        if i % 2:
            n *= 2
            if n > 9:
                n -= 9
        total += n
    return total % 10 == 0


def _v6(m: str, before: str, after: str) -> str:
    if _is_hex(before) or before == ":" or _is_hex(after) or after == ":":
        return m
    groups = _ipv6_groups(m)
    if groups is None:
        return m
    if all(int(g, 16) == 0 for g in groups[:7]) and int(groups[7], 16) == 1:
        return m
    return f"{groups[0]}:{groups[1]}:{groups[2]}::"


def _ipv6_groups(m: str) -> list[str] | None:
    if ":::" in m or m.count("::") > 1:
        return None
    if "::" in m:
        left_s, right_s = m.split("::", 1)
        left = left_s.split(":") if left_s else []
        right = right_s.split(":") if right_s else []
        if len(left) + len(right) > 7:
            return None
        groups = left + ["0"] * (8 - len(left) - len(right)) + right
    else:
        groups = m.split(":")
        if len(groups) != 8:
            return None
    return None if any(g == "" for g in groups) else groups


def _replace_checked(text: str, regex: re.Pattern[str], fn: Callable[[str, str, str], str]) -> str:
    def sub(m: re.Match[str]) -> str:
        s, e = m.span()
        before = text[s - 1] if s > 0 else ""
        after = text[e] if e < len(text) else ""
        return fn(m.group(0), before, after)

    return regex.sub(sub, text)


def load_rules(raw: dict[str, Any]) -> Rules:
    values = []
    for v in raw["values"]:
        ic = bool(v.get("ignore_case"))
        values.append(
            _Rule(
                regex=re.compile(v["pattern"], re.IGNORECASE if ic else 0),
                # $1 -> \g<1> (Python's group reference)
                replace=_GROUP_RE.sub(r"\\g<\1>", v["replace"]),
                needs=tuple(n.lower() if ic else n for n in v.get("needs", [])),
                ignore_case=ic,
            )
        )
    cards = raw["cards"]
    return Rules(
        key_mask=raw["key_mask"],
        value_mask=raw["value_mask"],
        keys=frozenset(k.lower() for k in raw["keys"]),
        key_suffixes=tuple(raw["key_suffixes"]),
        key_parts=tuple(raw["key_parts_string_only"]),
        values=tuple(values),
        needs_lower=(
            tuple(dict.fromkeys(n.lower() for v in values for n in v.needs))
            if values and all(v.needs for v in values)
            else None
        ),
        cards=re.compile(cards["pattern"]),
        card_first=cards["first_digits"],
        card_min=int(cards["min_digits"]),
        card_max=int(cards["max_digits"]),
        card_replace=cards["replace"],
        ipv4=re.compile(raw["ipv4"]["pattern"]),
        ipv4_keep=frozenset(raw["ipv4"]["keep_first_octet"]),
        ipv6=re.compile(raw["ipv6"]["pattern"]),
        raw=raw,
    )


RULES_PATH = Path(__file__).with_name("redact_rules.json")
RULES = load_rules(json.loads(RULES_PATH.read_text(encoding="utf-8")))


def scrub(text: str) -> str:
    """``text`` with the shared value rules applied (the same object when unchanged)."""
    return RULES.scrub(text)


# --- hashed user ids (story 53.01 §3) ------------------------------------------------

_user_key: bytes | None = None
_user_key_loaded = False


def set_user_hash_key(key: bytes | None) -> None:
    """The key user ids are hashed with; ``None`` drops user ids from lines."""
    global _user_key, _user_key_loaded
    _user_key = bytes(key) if key else None
    _user_key_loaded = True


def _current_key() -> bytes | None:
    global _user_key, _user_key_loaded
    if not _user_key_loaded:
        _user_key_loaded = True
        path = os.environ.get(LOG_KEY_ENV, "").strip()
        if path:
            try:
                data = Path(path).read_bytes()
            except OSError:
                data = b""
            _user_key = data if len(data) == LOG_KEY_SIZE else None
    return _user_key


def user_hash(key: bytes, user_id: str) -> str:
    """``user_h``: the first 16 hex of HMAC-SHA256(key, user id)."""
    return hmac.new(key, user_id.encode("utf-8"), hashlib.sha256).hexdigest()[:16]


def hash_user(user_id: object) -> str:
    """``user_h`` under this process's log key; ``""`` when there is none."""
    key = _current_key()
    text = str(user_id) if user_id is not None else ""
    if not key or not text:
        return ""
    return user_hash(key, text)
