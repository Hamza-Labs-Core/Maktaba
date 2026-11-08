"""The fake ffmpeg of ``tests.stages.world`` answers exactly as it did before it was made fast:
the bytes it gives decide the style metrics, the fingerprints and every parity row built on
them, so a faster form must give the same bytes."""

from __future__ import annotations

import random

import pytest

from tests.stages import world

pytestmark = pytest.mark.unit


@pytest.mark.parametrize("seed", [0, 54, world._seed("show-a")])
def test_bytes_are_the_per_byte_draws(seed: int) -> None:
    slow, fast = random.Random(seed), random.Random(seed)
    want = bytes(slow.getrandbits(8) for _ in range(3000))
    assert world._bytes(fast, 3000) == want
    # and the generator is left where the per-byte draws left it
    assert fast.getrandbits(64) == slow.getrandbits(64)


def test_an_answer_depends_only_on_the_family_and_the_call() -> None:
    rng = random.Random(world._seed("fam"))
    thumbs, log = world._thumbs(rng), world._style_log(rng).encode()
    assert world._fake_output("fam", "style") == (thumbs, log)
    assert world._fake_output("fam", "style") is world._fake_output("fam", "style")
    assert world._fake_output("other", "style") != (thumbs, log)
