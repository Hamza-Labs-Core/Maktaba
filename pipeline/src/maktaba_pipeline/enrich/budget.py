"""The daily caps of the web part: calls and dollars per UTC day (``enrich_usage``).

:meth:`Budget.check` is asked before a call (``BudgetExhausted`` when it would go over a
cap); :meth:`Budget.spend` records the call after it was made. A call answered from the
cache costs nothing and is never counted. Over a cap the web part waits for the next UTC
day (the worker defers the job; see :mod:`.worker`), the internal suggestion is still made.
"""

from __future__ import annotations

from collections.abc import Callable
from datetime import UTC, datetime, timedelta
from typing import Any

from ..engine.sql import now as _now
from ..engine.sql import to_ts
from .config import EnrichConfig

__all__ = ["KINDS", "Budget", "BudgetExhausted", "seconds_to_next_day"]

KINDS = ("search", "page", "llm")


class BudgetExhausted(Exception):
    """A daily cap is reached; ``kind`` names it (``search`` / ``page`` / ``llm`` / ``usd``)."""

    def __init__(self, kind: str, detail: str) -> None:
        super().__init__(f"daily cap reached ({kind}): {detail}")
        self.kind = kind
        self.detail = detail


def seconds_to_next_day(at: datetime) -> float:
    nxt = (at.astimezone(UTC) + timedelta(days=1)).replace(hour=0, minute=0, second=5,
                                                            microsecond=0)  # fmt: skip
    return max(60.0, (nxt - at.astimezone(UTC)).total_seconds())


class Budget:
    def __init__(self, db: Any, cfg: EnrichConfig, *, clock: Callable[[], datetime] = _now) -> None:
        self.db = db
        self.cfg = cfg
        self.clock = clock

    def day(self) -> str:
        return self.clock().astimezone(UTC).strftime("%Y-%m-%d")

    def _cap(self, kind: str) -> int:
        return {
            "search": self.cfg.max_searches_per_day,
            "page": self.cfg.max_pages_per_day,
            "llm": self.cfg.max_llm_calls_per_day,
        }[kind]

    async def usage(self) -> dict[str, tuple[int, float]]:
        rows = await self.db.fetch(
            "SELECT kind, calls, usd FROM enrich_usage WHERE day = $1", self.day()
        )
        out = {k: (0, 0.0) for k in KINDS}
        for r in rows:
            out[str(r["kind"])] = (int(r["calls"]), float(r["usd"]))
        return out

    async def check(self, kind: str, usd: float = 0.0) -> None:
        """Raise :class:`BudgetExhausted` when one more ``kind`` call (costing ``usd``)
        would pass its cap or the day's dollars."""
        use = await self.usage()
        calls, _ = use[kind]
        if calls + 1 > self._cap(kind):
            raise BudgetExhausted(kind, f"{calls} of {self._cap(kind)} {kind} calls today")
        spent = sum(u for _, u in use.values())
        if usd > 0 and spent + usd > self.cfg.max_usd_per_day + 1e-9:
            raise BudgetExhausted(
                "usd", f"${spent:.3f} of ${self.cfg.max_usd_per_day:.2f} spent today"
            )

    async def spend(self, kind: str, usd: float = 0.0) -> None:
        await self.db.execute(
            "INSERT INTO enrich_usage (day, kind, calls, usd, updated_at) VALUES ($1,$2,1,$3,$4)"
            " ON CONFLICT (day, kind) DO UPDATE SET calls = enrich_usage.calls + 1,"
            " usd = enrich_usage.usd + EXCLUDED.usd, updated_at = EXCLUDED.updated_at",
            self.day(),
            kind,
            float(usd),
            to_ts(self.db, self.clock()),
        )

    def llm_usd(self, tokens: int) -> float:
        return tokens / 1000.0 * self.cfg.llm_usd_per_1k_tokens
