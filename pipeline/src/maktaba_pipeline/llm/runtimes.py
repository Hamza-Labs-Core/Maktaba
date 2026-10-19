"""The LLM runtimes on this machine (design-agent.md section 6).

- Ollama at ``MAKTABA_OLLAMA_URL`` (default ``http://127.0.0.1:11434``): ``/api/tags`` for
  the models, ``/api/ps`` for the loaded ones.
- OpenAI-compatible servers (LM Studio, vLLM, llama.cpp server) listed in
  ``MAKTABA_LLM_ENDPOINTS`` (comma-separated base URLs): ``/v1/models``.

``MAKTABA_LLM_MAX_PARALLEL`` (default 1) requests at a time per runtime. The agent
re-checks every 30 s and sends ``llm.models`` when something changed.
"""

from __future__ import annotations

import asyncio
import os
import time
from collections import deque
from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

from . import http

__all__ = [
    "DEFAULT_OLLAMA_URL",
    "LLMRuntimes",
    "ModelInfo",
    "Runtime",
    "detect",
    "endpoints_from_env",
]

DEFAULT_OLLAMA_URL = "http://127.0.0.1:11434"
RECHECK_SEC = 30.0
TOKENS_WINDOW_SEC = 10.0


@dataclass(frozen=True, slots=True)
class ModelInfo:
    name: str
    size: int | None = None
    family: str | None = None
    quant: str | None = None
    loaded: bool = False

    def as_json(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "size": self.size,
            "family": self.family,
            "quant": self.quant,
            "loaded": self.loaded,
        }


@dataclass(frozen=True, slots=True)
class Runtime:
    provider: str  #: ``ollama`` or ``openai``
    base_url: str
    models: tuple[ModelInfo, ...] = ()
    max_parallel: int = 1

    def as_json(self) -> dict[str, Any]:
        return {
            "provider": self.provider,
            "base_url": self.base_url,
            "models": [m.as_json() for m in self.models],
            "max_parallel": self.max_parallel,
        }

    def model(self, name: str) -> ModelInfo | None:
        for m in self.models:
            if _same_model(m.name, name):
                return m
        return None


def _same_model(have: str, want: str) -> bool:
    if have == want:
        return True
    # Ollama: "llama3" is "llama3:latest"
    return have == f"{want}:latest" or want == f"{have}:latest"


def endpoints_from_env(env: Mapping[str, str] | None = None) -> tuple[str, list[str], int]:
    """``(ollama url or "", OpenAI-compatible base URLs, max_parallel)``."""
    env = os.environ if env is None else env
    ollama = env.get("MAKTABA_OLLAMA_URL", DEFAULT_OLLAMA_URL).strip().rstrip("/")
    others = [
        u.strip().rstrip("/") for u in env.get("MAKTABA_LLM_ENDPOINTS", "").split(",") if u.strip()
    ]
    try:
        par = max(1, int(env.get("MAKTABA_LLM_MAX_PARALLEL", "") or 1))
    except ValueError:
        par = 1
    return ollama, others, par


def _openai_root(base: str) -> str:
    """``http://h:1234`` and ``http://h:1234/v1`` both name the same server."""
    return base[:-3] if base.endswith("/v1") else base


async def _ollama(url: str, par: int, timeout: float) -> Runtime | None:
    try:
        tags = await http.get_json(f"{url}/api/tags", timeout)
    except Exception:  # noqa: BLE001 - not running here
        return None
    loaded: set[str] = set()
    try:
        ps = await http.get_json(f"{url}/api/ps", timeout)
        loaded = {str(m.get("name") or m.get("model")) for m in (ps or {}).get("models") or []}
    except Exception:  # noqa: BLE001
        pass
    models = []
    for m in (tags or {}).get("models") or []:
        name = str(m.get("name") or m.get("model") or "")
        if not name:
            continue
        details = m.get("details") or {}
        models.append(
            ModelInfo(
                name=name,
                size=int(m["size"]) if isinstance(m.get("size"), int) else None,
                family=details.get("family"),
                quant=details.get("quantization_level"),
                loaded=name in loaded,
            )
        )
    return Runtime("ollama", url, tuple(sorted(models, key=lambda x: x.name)), par)


async def _openai(base: str, par: int, timeout: float) -> Runtime | None:
    root = _openai_root(base)
    try:
        data = await http.get_json(f"{root}/v1/models", timeout)
    except Exception:  # noqa: BLE001
        return None
    models = [
        ModelInfo(name=str(m.get("id")), loaded=bool(m.get("loaded", False)))
        for m in (data or {}).get("data") or []
        if m.get("id")
    ]
    return Runtime("openai", root, tuple(sorted(models, key=lambda x: x.name)), par)


async def detect(env: Mapping[str, str] | None = None, timeout: float = 3.0) -> list[Runtime]:
    ollama, others, par = endpoints_from_env(env)
    probes = []
    if ollama:
        probes.append(_ollama(ollama, par, timeout))
    probes += [_openai(u, par, timeout) for u in others]
    found = await asyncio.gather(*probes)
    return [r for r in found if r is not None]


@dataclass
class _Slot:
    limit: int
    in_flight: int = 0
    waiters: deque[asyncio.Future[None]] = field(default_factory=deque)


@dataclass
class LLMRuntimes:
    """The detected runtimes, their capacity and this process's LLM load."""

    env: Mapping[str, str] | None = None
    runtimes: list[Runtime] = field(default_factory=list)
    checked_at: float = float("-inf")
    _slots: dict[str, _Slot] = field(default_factory=dict)
    _tokens: deque[tuple[float, int]] = field(default_factory=deque)
    _refresh_lock: asyncio.Lock | None = None

    async def refresh(self) -> bool:
        """Detect again; True when the runtimes or their models changed."""
        found = await detect(self.env)
        self.checked_at = time.monotonic()
        changed = [r.as_json() for r in found] != [r.as_json() for r in self.runtimes]
        self.runtimes = found
        return changed

    async def ensure_fresh(self, max_age: float = RECHECK_SEC) -> None:
        if self._refresh_lock is None:
            self._refresh_lock = asyncio.Lock()
        async with self._refresh_lock:
            if time.monotonic() - self.checked_at >= max_age:
                await self.refresh()

    def find(self, model: str) -> Runtime | None:
        """The runtime to run ``model`` on: one that has it loaded, else any that has it."""
        having = [r for r in self.runtimes if r.model(model) is not None]
        for r in having:
            m = r.model(model)
            if m is not None and m.loaded:
                return r
        return having[0] if having else None

    def frame(self) -> dict[str, Any]:
        return {"t": "llm.models", "runtimes": [r.as_json() for r in self.runtimes]}

    # -- capacity ---------------------------------------------------------------------

    def _slot(self, rt: Runtime) -> _Slot:
        slot = self._slots.get(rt.base_url)
        if slot is None:
            slot = self._slots[rt.base_url] = _Slot(limit=rt.max_parallel)
        slot.limit = rt.max_parallel
        return slot

    def try_acquire(self, rt: Runtime) -> bool:
        """Take a slot now, or False when the runtime is at ``max_parallel``."""
        slot = self._slot(rt)
        if slot.in_flight >= slot.limit or slot.waiters:
            return False
        slot.in_flight += 1
        return True

    async def acquire(self, rt: Runtime) -> None:
        """Take a slot, waiting in line (FIFO) when the runtime is busy."""
        slot = self._slot(rt)
        while slot.in_flight >= slot.limit:
            fut: asyncio.Future[None] = asyncio.get_running_loop().create_future()
            slot.waiters.append(fut)
            try:
                await fut
            except asyncio.CancelledError:
                if fut in slot.waiters:
                    slot.waiters.remove(fut)
                elif fut.done() and not fut.cancelled():
                    self._wake_next(slot)  # woken, then cancelled: pass the turn on
                raise
        slot.in_flight += 1

    def release(self, rt: Runtime) -> None:
        slot = self._slot(rt)
        slot.in_flight = max(0, slot.in_flight - 1)
        self._wake_next(slot)

    @staticmethod
    def _wake_next(slot: _Slot) -> None:
        while slot.waiters:
            fut = slot.waiters.popleft()
            if not fut.done():
                fut.set_result(None)
                break

    # -- load -----------------------------------------------------------------------------

    def add_tokens(self, n: int) -> None:
        if n > 0:
            self._tokens.append((time.monotonic(), n))

    def stats(self) -> dict[str, Any]:
        now = time.monotonic()
        while self._tokens and now - self._tokens[0][0] > TOKENS_WINDOW_SEC:
            self._tokens.popleft()
        tokens = sum(n for _, n in self._tokens)
        return {
            "in_flight": sum(s.in_flight for s in self._slots.values()),
            "tokens_per_s": round(tokens / TOKENS_WINDOW_SEC, 1),
        }
