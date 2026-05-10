"""Story 9.1 — library config schema, defaults inheritance, and validation.

The `libraries.settings` JSONB blob is the per-library config profile.
This module is the single source of truth for:

1. **Recognised keys** — the closed schema enumerated in Story 9.1 AC-1.
   Unknown keys are *preserved* (forward-compat) but flagged as warnings
   to the API caller.

2. **Defaults inheritance** — the layered merge `library < pipeline.toml`
   so a worker can ask for the *effective* config without knowing which
   layer set each key (AC-2).

3. **Boot-time / PATCH-time validation** — every recognised key gets a
   typed checker; the failures come back as JSON-pointer field paths so
   the API layer maps them straight to a 422 ``FieldError`` array
   (AC-3 wires the NOTIFY; we expose the canonical event name).

The shape mirrors the architecture spec rather than the database row:
this module never touches the DB. It accepts and returns plain ``dict``
objects so the API/Go side can hand it the JSONB blob and the worker
side can hand it the parsed `pipeline.toml` defaults.
"""

from __future__ import annotations

import re
from collections.abc import Mapping
from copy import deepcopy
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "ALLOWED_STT_BACKENDS",
    "EFFECTIVE_DEFAULTS",
    "RECOGNISED_KEYS",
    "SETTINGS_CHANGED_EVENT",
    "ValidationError",
    "ValidationResult",
    "deep_merge",
    "effective_config",
    "validate",
]


#: Canonical NOTIFY channel name fired by the API after a successful
#: PATCH (AC-3). The orchestrator subscribes and starts marking newly-
#: arriving videos with the new model from this point. Existing videos
#: are *not* re-processed automatically.
SETTINGS_CHANGED_EVENT = "library.settings_changed"

#: STT backend whitelist (Epic 3). A PATCH with any other value returns
#: 422 with the offending JSON pointer.
ALLOWED_STT_BACKENDS: frozenset[str] = frozenset({"whisper-mlx", "faster-whisper", "openai-api"})

#: Recognised keys at the top level. Anything outside this set survives
#: a round-trip but is flagged as a warning on PATCH so the UI can
#: surface a "you typed an unknown key" hint (Story 9.1 AC-1 forward-
#: compat clause).
RECOGNISED_KEYS: frozenset[str] = frozenset(
    {
        "language",
        "multi_audio",
        "stt",
        "embedding",
        "diarize",
        "chapter_inference",
        "auto_tag_topics",
        "default_subtitle_lang",
        "ignore_globs",
        "sweep_interval_sec",
        # Story 9.10/9.11 effective-config keys (defaults below).
        "speaker_match_threshold",
        "topic_clusters",
    }
)

#: ISO-639-1 two-letter code (lowercase). The "auto" sentinel is also
#: accepted at validation time but is not an ISO code so we match it
#: separately.
_ISO639_1_RE = re.compile(r"^[a-z]{2}$")

#: Recognised keys per nested object. Used to validate sub-blobs like
#: ``stt`` and ``embedding``.
_NESTED_KEYS: dict[str, frozenset[str]] = {
    "stt": frozenset({"backend", "model", "profile", "initial_prompt", "max_usd_per_month"}),
    "embedding": frozenset({"model", "device"}),
}

#: The defaults a worker gets if neither the library nor `pipeline.toml`
#: ships a value. Mirrors the architecture defaults for keys that have
#: well-known fallbacks; absent for keys the user must set explicitly.
EFFECTIVE_DEFAULTS: dict[str, Any] = {
    "language": "auto",
    "multi_audio": False,
    "diarize": False,
    "chapter_inference": True,
    "auto_tag_topics": True,
    "default_subtitle_lang": "en",
    "ignore_globs": [],
    "sweep_interval_sec": 6 * 60 * 60,  # 6 h, per architecture §3.1
    "speaker_match_threshold": 0.35,
    "topic_clusters": None,  # auto: sqrt(N)/2 capped at 32
    "stt": {"backend": "whisper-mlx", "model": "large-v3", "profile": "default"},
    "embedding": {"model": "intfloat/multilingual-e5-large", "device": "auto"},
}


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


@dataclass(slots=True, frozen=True)
class ValidationError:
    """One field-level validation failure.

    ``path`` is a JSON Pointer-ish slash-separated path that the API
    layer renders as a ``FieldError.field`` (e.g. ``stt/backend``).
    ``message`` is the human-facing detail.
    """

    path: str
    message: str


@dataclass(slots=True)
class ValidationResult:
    """Outcome of :func:`validate`. ``ok`` if no errors."""

    errors: list[ValidationError] = field(default_factory=list)
    warnings: list[ValidationError] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.errors


def validate(settings: Mapping[str, Any]) -> ValidationResult:
    """Validate a `libraries.settings` blob.

    Returns errors for type mismatches, vocabulary violations, and
    nested-key shape drift; returns warnings for unknown top-level or
    nested keys (preserved on round-trip per the forward-compat AC).
    """
    result = ValidationResult()

    if not isinstance(settings, Mapping):
        result.errors.append(ValidationError("", "settings must be an object"))
        return result

    for key, value in settings.items():
        if key not in RECOGNISED_KEYS:
            result.warnings.append(ValidationError(key, f"unknown key {key!r}"))
            continue
        _validate_key(key, value, result)

    return result


def _validate_key(key: str, value: Any, result: ValidationResult) -> None:
    """Dispatch one top-level key to its specific validator."""
    if key == "language":
        if value == "auto":
            return
        if not isinstance(value, str) or not _ISO639_1_RE.match(value):
            result.errors.append(ValidationError("language", "must be 'auto' or an ISO-639-1 code"))
        return

    if key == "default_subtitle_lang":
        if not isinstance(value, str) or not _ISO639_1_RE.match(value):
            result.errors.append(ValidationError(key, "must be an ISO-639-1 code"))
        return

    if key in ("multi_audio", "diarize", "chapter_inference", "auto_tag_topics"):
        if not isinstance(value, bool):
            result.errors.append(ValidationError(key, "must be boolean"))
        return

    if key == "sweep_interval_sec":
        # bool is a subclass of int — reject explicitly to keep the
        # vocabulary tight.
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            result.errors.append(ValidationError(key, "must be a non-negative integer"))
        return

    if key == "ignore_globs":
        if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
            result.errors.append(ValidationError(key, "must be a list of strings"))
        return

    if key == "speaker_match_threshold":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            result.errors.append(ValidationError(key, "must be a number"))
            return
        if not 0.0 <= float(value) <= 1.0:
            result.errors.append(ValidationError(key, "must be in [0, 1]"))
        return

    if key == "topic_clusters":
        if value is None:
            return
        if isinstance(value, bool) or not isinstance(value, int) or value < 1:
            result.errors.append(ValidationError(key, "must be a positive integer or null"))
        return

    if key == "stt":
        _validate_nested("stt", value, result, _validate_stt_field)
        return

    if key == "embedding":
        _validate_nested("embedding", value, result, _validate_embedding_field)
        return


def _validate_nested(
    key: str,
    value: Any,
    result: ValidationResult,
    field_validator: Any,
) -> None:
    if not isinstance(value, Mapping):
        result.errors.append(ValidationError(key, "must be an object"))
        return
    allowed = _NESTED_KEYS.get(key, frozenset())
    for nk, nv in value.items():
        if nk not in allowed:
            result.warnings.append(ValidationError(f"{key}/{nk}", f"unknown key {nk!r}"))
            continue
        field_validator(nk, nv, result)


def _validate_stt_field(name: str, value: Any, result: ValidationResult) -> None:
    if name == "backend":
        if not isinstance(value, str):
            result.errors.append(ValidationError("stt/backend", "must be a string"))
        elif value not in ALLOWED_STT_BACKENDS:
            result.errors.append(
                ValidationError(
                    "stt/backend",
                    f"unknown backend {value!r}; allowed: {sorted(ALLOWED_STT_BACKENDS)}",
                )
            )
    elif name in ("model", "profile", "initial_prompt"):
        if not isinstance(value, str):
            result.errors.append(ValidationError(f"stt/{name}", "must be a string"))
    elif name == "max_usd_per_month" and (
        isinstance(value, bool) or not isinstance(value, (int, float)) or value < 0
    ):
        result.errors.append(ValidationError(f"stt/{name}", "must be a non-negative number"))


def _validate_embedding_field(name: str, value: Any, result: ValidationResult) -> None:
    if name == "model":
        if not isinstance(value, str):
            result.errors.append(ValidationError("embedding/model", "must be a string"))
    elif name == "device" and (
        not isinstance(value, str) or value not in {"auto", "cpu", "cuda", "mps"}
    ):
        result.errors.append(
            ValidationError(
                "embedding/device",
                "must be one of {'auto', 'cpu', 'cuda', 'mps'}",
            )
        )


# ---------------------------------------------------------------------------
# Defaults inheritance (AC-2)
# ---------------------------------------------------------------------------


def deep_merge(base: Mapping[str, Any], override: Mapping[str, Any]) -> dict[str, Any]:
    """Return a deep merge of ``base`` ⊕ ``override`` (override wins).

    Mirrors the Go-side ``DeepMergeJSON`` semantic (libraries handler):
    nested dicts merge recursively; everything else (lists, scalars) is
    replaced wholesale. Inputs are never mutated.
    """
    out: dict[str, Any] = deepcopy(dict(base))
    for k, v in override.items():
        if isinstance(v, Mapping) and k in out and isinstance(out[k], Mapping):
            out[k] = deep_merge(out[k], v)
        else:
            out[k] = deepcopy(v)
    return out


def effective_config(
    library_settings: Mapping[str, Any],
    pipeline_defaults: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Resolve the effective config for a worker.

    Layered merge order (lowest precedence first, library wins):

      ``EFFECTIVE_DEFAULTS  ⊕  pipeline_defaults  ⊕  library_settings``

    This is the AC-2 semantic: a library that only sets
    ``stt.backend`` still gets ``stt.model`` from the lower layer,
    recursively. The library can override any layer below it.
    """
    merged = deep_merge(EFFECTIVE_DEFAULTS, pipeline_defaults or {})
    merged = deep_merge(merged, library_settings)
    return merged
