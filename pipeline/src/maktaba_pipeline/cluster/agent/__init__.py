"""The cluster agent: every pipeline process's live channel to the home
(``specs/epics/31-multi-instance/design-agent.md``).

Kept light on import: the engine imports :mod:`.jobs` on its hot path, and processes
that never start an agent must not pay for websockets or psutil.
"""

from __future__ import annotations

import os

__all__ = ["enabled"]


def enabled(env: dict[str, str] | None = None) -> bool:
    """``MAKTABA_CLUSTER_AGENT=0`` turns the agent off (tests, single-machine debugging)."""
    env = dict(os.environ) if env is None else env
    return env.get("MAKTABA_CLUSTER_AGENT", "1").strip().lower() not in ("0", "false", "no", "off")
