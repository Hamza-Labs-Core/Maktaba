"""The batched timestamp rules against OpenAI Whisper's row-by-row ones."""

from __future__ import annotations

import numpy as np
import pytest

mx = pytest.importorskip("mlx.core")

from maktaba_pipeline.stt.rules import TimestampRules  # noqa: E402

EOT, NO_TS, TB, V = 40, 45, 50, 80
SAMPLE_BEGIN = 3


def reference(logits: np.ndarray, tokens: np.ndarray, max_init: int | None) -> np.ndarray:
    """``ApplyTimestampRules.apply`` from openai/whisper, on NumPy."""
    logits = logits.copy()
    logits[:, NO_TS] = -np.inf
    for k in range(tokens.shape[0]):
        seq = tokens[k, SAMPLE_BEGIN:]
        last = len(seq) >= 1 and seq[-1] >= TB
        pen = len(seq) < 2 or seq[-2] >= TB
        if last:
            if pen:
                logits[k, TB:] = -np.inf
            else:
                logits[k, :EOT] = -np.inf
        ts = seq[seq >= TB]
        if ts.size > 0:
            end = ts[-1] if last and not pen else ts[-1] + 1
            logits[k, TB:end] = -np.inf
    if tokens.shape[1] == SAMPLE_BEGIN:
        logits[:, :TB] = -np.inf
        if max_init is not None:
            logits[:, TB + max_init + 1 :] = -np.inf
    with np.errstate(invalid="ignore"):
        lp = logits - np.logaddexp.reduce(logits, axis=-1, keepdims=True)
    for k in range(tokens.shape[0]):
        if np.logaddexp.reduce(lp[k, TB:]) > lp[k, :TB].max():
            logits[k, :TB] = -np.inf
    return logits


@pytest.mark.parametrize("max_init", [None, 5])
def test_the_same_masks_as_openai_whisper_step_after_step(max_init: int | None) -> None:
    rng = np.random.default_rng(7)
    rules = TimestampRules(
        mx, sample_begin=SAMPLE_BEGIN, timestamp_begin=TB, eot=EOT, no_timestamps=NO_TS,
        max_initial_timestamp_index=max_init,
    )  # fmt: skip
    batch = 16
    tokens = np.tile(np.array([1, 2, 3]), (batch, 1))
    for _ in range(25):
        logits = rng.normal(0, 2, (batch, V)).astype(np.float32)
        want = reference(logits, tokens, max_init)
        got = np.array(rules.apply(mx.array(logits), mx.array(tokens)))
        assert np.array_equal(np.isneginf(got), np.isneginf(want))
        # sample the next token from what is allowed, as decoding would
        p = np.exp(want - want.max(axis=1, keepdims=True))
        p /= p.sum(axis=1, keepdims=True)
        nxt = np.array([rng.choice(V, p=row) for row in p])
        tokens = np.concatenate([tokens, nxt[:, None]], axis=1)
    # the walk exercised every rule: pairs, text, and timestamps after the first
    tail = tokens[:, SAMPLE_BEGIN:]
    assert (tail >= TB).sum() > batch and (tail < EOT).sum() > batch
