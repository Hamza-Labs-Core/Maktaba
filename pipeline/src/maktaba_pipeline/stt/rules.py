"""Whisper's timestamp rules as array operations, for the whole batch at once (plan 03-10).

``mlx_whisper.decoding.ApplyTimestampRules`` runs once per decoded token: it copies the
batch's tokens to the CPU (``tolist``, which waits for the GPU and so stops MLX from
preparing the next step while this one runs), loops over the rows in Python, and fills a
(batch x 51,866) NumPy mask. With ~150 tokens a window and 30 windows a pass, that was
most of a pass's decoding time. Here the same rules are a handful of GPU operations.

The rules are OpenAI Whisper's (``whisper/decoding.py``). MLX-Whisper's copy differs in
two places, both fixed here: it bounds the "no earlier timestamp" mask by the *position*
of the last timestamp rather than its value (so the rule never applies), and it compares
the timestamp and text probabilities before the mask rather than after.
"""

from __future__ import annotations

from typing import Any

__all__ = ["TimestampRules"]


class TimestampRules:
    """A logit filter with ``ApplyTimestampRules``' interface."""

    def __init__(
        self,
        mx: Any,
        *,
        sample_begin: int,
        timestamp_begin: int,
        eot: int,
        no_timestamps: int | None,
        max_initial_timestamp_index: int | None,
    ) -> None:
        self.mx = mx
        self.sample_begin = sample_begin
        self.tb = timestamp_begin
        self.eot = eot
        self.no_timestamps = no_timestamps
        self.max_initial = max_initial_timestamp_index

    def apply(self, logits: Any, tokens: Any) -> Any:
        mx = self.mx
        n_batch, n_vocab = logits.shape
        vocab = mx.arange(n_vocab)[None, :]
        is_time = vocab >= self.tb
        is_text = vocab < self.eot
        ban = mx.zeros((n_batch, n_vocab), dtype=mx.bool_)
        if self.no_timestamps is not None:
            ban = ban | (vocab == self.no_timestamps)

        n = tokens.shape[1] - self.sample_begin
        if n == 0:
            # a window starts with a timestamp, no later than max_initial_timestamp
            ban = ban | ~is_time
            if self.max_initial is not None:
                ban = ban | (vocab > self.tb + self.max_initial)
        else:
            seq = tokens[:, self.sample_begin :]
            last = seq[:, -1] >= self.tb
            # a lone first token counts as after a timestamp
            one = mx.ones((n_batch,), dtype=mx.bool_)
            penultimate = seq[:, -2] >= self.tb if n >= 2 else one
            # timestamps come in pairs, except right before end-of-text
            ban = ban | ((last & penultimate)[:, None] & is_time)
            ban = ban | ((last & ~penultimate)[:, None] & is_text)
            # timestamps never go back, and a sentence lasts more than 0 s: the rules
            # keep them non-decreasing, so the last one is the largest
            seen = seq >= self.tb
            has = mx.any(seen, axis=1)
            latest = mx.max(mx.where(seen, seq, 0), axis=1)
            bound = mx.where(last & ~penultimate, latest, latest + 1)
            ban = ban | (has[:, None] & is_time & (vocab < bound[:, None]))

        neg = mx.array(-mx.inf, dtype=logits.dtype)
        logits = mx.where(ban, neg, logits)
        # when the timestamps together are likelier than any one text token, take one
        logprobs = logits.astype(mx.float32)
        logprobs = logprobs - mx.logsumexp(logprobs, axis=-1, keepdims=True)
        time_lp = mx.logsumexp(logprobs[:, self.tb :], axis=-1)
        text_lp = mx.max(logprobs[:, : self.tb], axis=-1)
        force = (time_lp > text_lp)[:, None] & ~is_time
        return mx.where(force, neg, logits)
