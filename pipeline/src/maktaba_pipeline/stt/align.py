"""Word timings for many windows in one model call (plan 03-10).

``mlx_whisper.timing.add_word_timestamps`` aligns one 30 s window per call: a forward
pass with the window's text, the cross-attention of the alignment heads, then dynamic
time warping per window. Run per window it costs a model call each, which is most of
what batching the decode saved. Here every window of a GPU pass is aligned in one
forward pass (token sequences padded with end-of-text: the decoder is causal, so the
padding changes no real position), and the per-window work after it (DTW, punctuation,
the word / sentence bookkeeping) is MLX-Whisper's, unchanged (MIT licence). The audio
is not encoded again: the decode's encoder output is reused (encoding is the costly half
of a forward pass, and the decoder alone takes ~0.2 s for a 24-window pass).
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from typing import Any

import numpy as np

__all__ = ["AlignItem", "align_batch"]

HOP_LENGTH = 160
SAMPLE_RATE = 16_000
TOKENS_PER_SECOND = SAMPLE_RATE / HOP_LENGTH / 2  # 50 audio tokens per second
PREPEND = "\"'“¿([{-"
APPEND = "\"'.。,，!！?？:：”)]}、"


@dataclass
class AlignItem:
    segments: list[dict[str, Any]]  # one window's sentences ("seek", "start", "end", "tokens")
    features: Any  # the window's encoder output (n_audio_ctx, n_audio_state)
    num_frames: int
    last_speech: float


def align_batch(model: Any, tokenizer: Any, items: list[AlignItem]) -> None:
    """Add ``words`` to every segment of every item, as ``add_word_timestamps`` would."""
    import mlx.core as mx  # type: ignore[import-untyped,import-not-found,unused-ignore]
    from mlx_whisper.timing import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        WordTiming,
        dtw,
        median_filter,
        merge_punctuations,
    )

    work = []
    for it in items:
        per_seg = [[t for t in s["tokens"] if t < tokenizer.eot] for s in it.segments]
        text = list(itertools.chain.from_iterable(per_seg))
        if it.segments and text:
            work.append((it, per_seg, text))
    if not work:
        for it in items:
            for s in it.segments:
                s["words"] = []
        return

    sot = list(tokenizer.sot_sequence)
    seqs = [[*sot, tokenizer.no_timestamps, *text, tokenizer.eot] for _, _, text in work]
    width = max(len(q) for q in seqs)
    tokens = mx.array([q + [tokenizer.eot] * (width - len(q)) for q in seqs])
    features = mx.stack([it.features for it, _, _ in work])
    logits, _, cross_qk = model.decoder(tokens, features)
    heads = model.alignment_heads.tolist()

    for b, ((it, per_seg, text), q) in enumerate(zip(work, seqs, strict=True)):
        n = len(q)
        sampled = logits[b][len(sot) : n - 2, : tokenizer.eot]
        probs = mx.softmax(sampled, precise=True, axis=-1)
        text_probs = np.array(mx.take_along_axis(probs, mx.array(text)[:, None], axis=1).squeeze(1))
        weights = mx.stack([cross_qk[layer][b, head][:n] for layer, head in heads])
        weights = weights[:, :, : it.num_frames // 2]
        weights = mx.softmax(weights, axis=-1, precise=True).astype(mx.float32)
        mean = mx.mean(weights, axis=-2, keepdims=True)
        std = mx.var(weights, axis=-2, keepdims=True, ddof=0).sqrt()
        w = median_filter(np.array((weights - mean) / std), 7)
        matrix = w.mean(axis=0)[len(sot) : -1]
        text_idx, time_idx = dtw(-matrix)

        words, word_tokens = tokenizer.split_to_word_tokens(text + [tokenizer.eot])
        alignment: list[Any] = []
        if len(word_tokens) > 1:
            bounds = np.pad(np.cumsum([len(t) for t in word_tokens[:-1]]), (1, 0))
            jumps = np.pad(np.diff(text_idx), (1, 0), constant_values=1).astype(bool)
            jump_times = time_idx[jumps] / TOKENS_PER_SECOND
            starts, ends = jump_times[bounds[:-1]], jump_times[bounds[1:]]
            pairs = zip(bounds[:-1], bounds[1:], strict=False)
            word_p = [np.mean(text_probs[i:j]) for i, j in pairs]
            alignment = [
                WordTiming(wd, tk, s, e, p)
                for wd, tk, s, e, p in zip(words, word_tokens, starts, ends, word_p, strict=False)
            ]
        _place(it, per_seg, alignment, merge_punctuations)

    for it in items:  # windows with no text
        for s in it.segments:
            s.setdefault("words", [])


def _place(it: AlignItem, per_seg: list[list[int]], alignment: list[Any], merge: Any) -> None:
    """``add_word_timestamps`` after ``find_alignment``, unchanged."""
    durations = np.array([t.end - t.start for t in alignment])
    durations = durations[durations.nonzero()]
    median = min(0.7, float(np.median(durations)) if len(durations) > 0 else 0.0)
    max_d = median * 2
    if len(durations) > 0:
        marks = ".。!！?？"
        for i in range(1, len(alignment)):
            if alignment[i].end - alignment[i].start > max_d:
                if alignment[i].word in marks:
                    alignment[i].end = alignment[i].start + max_d
                elif alignment[i - 1].word in marks:
                    alignment[i].start = alignment[i].end - max_d
    merge(alignment, PREPEND, APPEND)

    offset = it.segments[0]["seek"] * HOP_LENGTH / SAMPLE_RATE
    last_speech = it.last_speech
    wi = 0
    for seg, text_tokens in zip(it.segments, per_seg, strict=True):
        saved = 0
        words: list[dict[str, Any]] = []
        while wi < len(alignment) and saved < len(text_tokens):
            t = alignment[wi]
            if t.word:
                words.append(
                    {
                        "word": t.word,
                        "start": round(offset + t.start, 2),
                        "end": round(offset + t.end, 2),
                        "probability": float(t.probability),
                    }
                )
            saved += len(t.tokens)
            wi += 1
        if words:
            if words[0]["end"] - last_speech > median * 4 and (
                words[0]["end"] - words[0]["start"] > max_d
                or (len(words) > 1 and words[1]["end"] - words[0]["start"] > max_d * 2)
            ):
                if len(words) > 1 and words[1]["end"] - words[1]["start"] > max_d:
                    boundary = max(words[1]["end"] / 2, words[1]["end"] - max_d)
                    words[0]["end"] = words[1]["start"] = boundary
                words[0]["start"] = max(0, words[0]["end"] - max_d)
            if seg["start"] < words[0]["end"] and seg["start"] - 0.5 > words[0]["start"]:
                words[0]["start"] = max(0, min(words[0]["end"] - median, seg["start"]))
            else:
                seg["start"] = words[0]["start"]
            if seg["end"] > words[-1]["start"] and seg["end"] + 0.5 < words[-1]["end"]:
                words[-1]["end"] = max(words[-1]["start"] + median, seg["end"])
            else:
                seg["end"] = words[-1]["end"]
            last_speech = seg["end"]
        seg["words"] = words
