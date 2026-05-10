"""Stage handlers — one module per pipeline stage.

Each stage handler is an ``async def`` that takes a :class:`DBConn`,
a video id, and the inputs its stage requires, then returns
``(outcome, summary)``. The orchestrator threads the outcome string
into ``advance_after_stage`` to drive the video FSM.
"""

from __future__ import annotations
