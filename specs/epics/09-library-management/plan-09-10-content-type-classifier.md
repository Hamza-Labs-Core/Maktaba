# Plan 9.10 — Auto-categorization: content type classifier — implementation

> Implementation plan for [story-09-10-content-type-classifier.md](story-09-10-content-type-classifier.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: features are populated at the tail of the
> probe stage from Epic 1 (`media_features` row written there);
> reads diarization stats from Epic 3 Story 3.9 if available, falls back
> to segment density from the transcribe stage; the `videos.content_type`
> column is read by the search filter (Epic 7 Story 7.4 `?type=lecture`)
> and by the stats trigger (Plan 9.7); user PATCH override behaviour
> mirrors Plan 9.8's language-source pattern.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Model: `sklearn.linear_model.LogisticRegression`** with one-hot input transformation, persisted as a pickle (`pipeline/src/maktaba_pipeline/categorize/model/v1.pkl`). The model is shipped in-tree, versioned, and loaded once per worker. | Story §"A small classifier (§5.2)"; architecture §5.2 ("a small classifier over duration, segment density, music-vs-speech ratio"). | LogisticRegression with 5 features is ~50 lines of state, deterministic at inference, ~10 µs per call. RandomForest is fine but adds ~200 KB of pickled state and 100× the inference cost for no measurable accuracy gain on this feature set. The pickle is reproducible because we commit the training script + fixture set. |
| D2 | **Confidence threshold 0.55 → `unknown`.** When `model.predict_proba(...).max() < 0.55`, we write `videos.content_type = 'unknown'`. The threshold is per-library overridable via `library.settings.content_type_threshold` (default 0.55). | Story AC-2 explicit. | 0.55 is just above random (1/6 ≈ 0.17) but conservative enough to avoid mislabeling an ambiguous clip. Per-library override lets a high-stakes archive tighten this to 0.7. |
| D3 | **Features written by the probe stage tail** as a single INSERT into `media_features`. The probe runs ffprobe + a one-shot `silencedetect`/`loudnorm` pass and computes the 5 features in-process; no separate "feature extraction" stage. | Story AC-1: "Feature extraction during probe — when probe completes, `media_features` is populated". | Adding a stage to the canonical pipeline would ripple through queue arithmetic and resume code. The features are derived from probe outputs we already compute (duration, audio loudness, silence segments) — folding them into the probe's tail is a ~30 µs addition. |
| D4 | **Classifier inference at the new `categorize` stage** that runs after `INDEXED`. The stage reads `media_features`, runs the model, writes `videos.content_type` and `videos.content_type_confidence`. Manual override (`videos.content_type_source = 'user'`) short-circuits inference. | Architecture §5.2 ("Categorization is done lazily after `INDEXED`"); story AC-2/AC-3. | Running classification at INDEXED rather than at probe lets us (in v1.1) condition on transcript-derived features (e.g., word-rate). For v1 we don't use those, but the stage placement future-proofs that path. |
| D5 | **Manual override flag on the row, not on a side table.** `videos.content_type_source TEXT NOT NULL DEFAULT 'auto'` with the same shape as Plan 9.8's `language_source`: values `auto`, `auto_low_conf`, `user`. PATCH writes `'user'`; auto-classifier respects it unless `?force=true`. | Story AC-3: "the user value is preserved unless `?force=true` is set". | Mirroring the language pattern means one mental model for "user-pinned values"; the `?force=true` flag lets admins re-run after a model upgrade. |
| D6 | **Index for filtering** — exactly the index the story names. `CREATE INDEX videos_content_type ON videos (content_type)`. Combined with `videos (library_id, state)` from architecture §8.1, the filter `?type=lecture&library_id=X&state=READY` is index-covered. | Story AC-4 explicit. | The story names this index; the migration provides it. |
| D7 | **Music-vs-speech ratio derived from `silencedetect` + `loudnorm`.** We classify a frame as "speech-likely" when loudness is in the [−20, −10] LUFS speech band and the segment length is < 5 s; "music-likely" when loudness is steady at [−18, −12] LUFS for ≥ 30 s. The ratio is `music_seconds / total_audio_seconds`. | Refines architecture §5.2 ("from FFmpeg `silencedetect` and `loudnorm` stats"). | A real music classifier needs a CNN over spectrograms, which is out of scope for v1. The loudness-band heuristic is correct enough for the gross "concert vs lecture" signal the classifier needs and adds zero new dependencies. |
| D8 | **Diarization-turn-density (when available) > segment-density.** When the library has diarization on (Epic 3 Story 3.9), `diarization_turn_density = num_speaker_turns / duration_minutes`. When diarization is off, `segment_density = num_segments / duration_minutes` from `transcript_segments` is the proxy. We carry both columns and the model uses whichever is non-NULL. | Architecture §5.2: "speaker turn density (from diarization if on, segment density otherwise)". | Both are proxies for "how often does the speaker change", which is the strongest single signal separating interview/podcast (high turns) from lecture/sermon (low turns). The model learns the sensible weighting from the training fixture set. |
| D9 | **Training fixture set is checked in.** `pipeline/tests/fixtures/categorize/train.csv` carries ~120 hand-labeled examples spanning the 6 classes; `pipeline/scripts/train_content_type.py` re-trains the pickle from the CSV. The CI lint step runs the trainer and diffs the resulting pickle hash against the committed file — drift fails the check. | Reproducibility; story test "deterministic classifier output for a 5-fixture set covering each class". | A drifted pickle in tree is the worst kind of bug — silent, untestable, and only visible on retraining. The hash check in CI catches it. |
| D10 | **`unknown` is a class in the model AND a fallback when confidence is low.** The model is trained with 6 classes (the 5 from the story + `unknown` as a "ragged tail" class). The threshold check (D2) is a *separate* fallback: even if the model says `lecture` with 0.4 confidence, we write `unknown`. | Refines the story; story AC-2 has confidence< 0.55 → unknown but also lists `unknown` as one of the predictable classes. | Having `unknown` as a model class lets the model itself learn to say "I don't know"; the threshold check is the safety net. Both are needed. |
| D11 | **Re-categorize on `?force=true` PATCH parameter** to the auto-categorize trigger endpoint, NOT on every PATCH to the row. Re-categorize is opt-in via either `force=true` on a re-run command or via the `categorize` stage when content_type_source != 'user'. | Story AC-3: "the user value is preserved unless `?force=true` is set". | Auto-overriding user PATCH on every re-probe would defeat the user override. The opt-in flag honors user intent. |

If D1 is rejected (RandomForest): inference time grows ~50× (~500 µs) and the pickle balloons to ~200 KB. Neither is fatal, but LogisticRegression is the right size for the problem.

If D9 is rejected (no training script in tree): the pickle becomes a magical artifact that nobody can reproduce. The CI hash check is what makes the pickle trustworthy.

---

## 1. Architecture diagram — feature extraction + inference

```
   Probe stage (Epic 1)
            │
            ├─ ffprobe → container/codec/streams/duration
            ├─ silencedetect → silence segments
            ├─ loudnorm → loudness measurements
            │
            ▼
   ┌────────────────────────────────────────────────┐
   │ media_features writer (D3)                     │
   │                                                │
   │   silence_pct = sum(silence) / duration        │
   │   mean_loudness_lufs = loudnorm.input_i        │
   │   music_speech_ratio = compute_band_ratio(D7)  │
   │   segment_density = NULL (filled after         │
   │     transcribe)                                │
   │   diarization_turn_density = NULL (filled      │
   │     after diarize, if applicable)              │
   │                                                │
   │   INSERT INTO media_features (...)             │
   │     ON CONFLICT (video_id) DO UPDATE …         │
   └────────────────────────────────────────────────┘
            │
            ▼ (transcribe + diarize stages run, fill the remaining cols)
   ┌────────────────────────────────────────────────┐
   │ Transcribe tail:                               │
   │   UPDATE media_features                        │
   │      SET segment_density = N/dur               │
   │    WHERE video_id = $1                         │
   │                                                │
   │ Diarize tail (if on):                          │
   │   UPDATE media_features                        │
   │      SET diarization_turn_density = T/dur      │
   │    WHERE video_id = $1                         │
   └────────────────────────────────────────────────┘
            │
            ▼ (after INDEXED — categorize stage)
   ┌────────────────────────────────────────────────┐
   │ ContentTypeWorker.run(claimed_job)             │
   │                                                │
   │   1. SELECT * FROM videos WHERE id=$1          │
   │      if content_type_source = 'user'           │
   │         and not force: skip (D5)               │
   │                                                │
   │   2. SELECT * FROM media_features WHERE        │
   │      video_id = $1                             │
   │      if NULL or core fields missing:           │
   │        skip; mark unknown (D10)                │
   │                                                │
   │   3. features = build_feature_vector(row,      │
   │                                  duration_sec) │
   │      (model, classes) = load_model()           │
   │      probs = model.predict_proba([features])   │
   │      argmax = classes[probs.argmax()]          │
   │      conf = probs.max()                        │
   │                                                │
   │   4. if conf < threshold:                      │
   │         label = 'unknown'                      │
   │         source = 'auto_low_conf'               │
   │      else:                                     │
   │         label = argmax                         │
   │         source = 'auto'                        │
   │                                                │
   │   5. UPDATE videos                             │
   │      SET content_type = $1,                    │
   │          content_type_confidence = $2,         │
   │          content_type_source = $3              │
   │    WHERE id = $4                               │
   │      AND content_type_source <> 'user'         │
   └────────────────────────────────────────────────┘

   Independent path: PATCH /api/videos/{id}
   ────────────────────────────────────────
   body { content_type: "lecture" }
   → UPDATE videos SET content_type=$1,
       content_type_source='user',
       content_type_confidence=NULL
     WHERE id=$2
   → stats trigger rebalances by_content_type_jsonb
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── categorize/
│   ├── __init__.py
│   ├── classifier.py           // load_model, ClassifierResult, classify
│   ├── features.py             // build_feature_vector, FeatureRow
│   ├── worker.py               // ContentTypeWorker.run
│   ├── probe_hook.py           // populate_media_features_from_probe
│   ├── source.py               // ContentTypeSource enum
│   ├── errors.py
│   ├── model/
│   │   └── v1.pkl              // committed sklearn LogisticRegression (D1, D9)
│   └── tests/
│       ├── conftest.py
│       ├── test_classifier_deterministic.py
│       ├── test_features_build.py
│       ├── test_worker_inference.py
│       ├── test_worker_unknown_fallback.py
│       ├── test_worker_user_override.py
│       └── test_probe_hook_populates_features.py
├── pipeline/
│   └── stages/
│       ├── probe.py            // extended: probe tail call probe_hook
│       └── categorize.py       // new: claim adapter to ContentTypeWorker
└── scripts/
    └── train_content_type.py   // re-train pickle from train.csv
pipeline/tests/fixtures/categorize/
├── train.csv                   // 120 labeled examples (D9)
└── film_45min.json             // sample input fixture
```

### 2.2 Schema migration — `media_features`, `videos.content_type` extension

```sql
-- shared/db/migrations/0029_content_type.sql
BEGIN;

CREATE TABLE media_features (
    video_id                 UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    music_speech_ratio       REAL,                -- 0..1; NULL if not yet computed
    silence_pct              REAL,
    mean_loudness_lufs       REAL,
    diarization_turn_density REAL,                -- turns per minute; NULL if no diar
    segment_density          REAL,                -- segments per minute; NULL until transcribe
    computed_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE videos
    ADD COLUMN content_type TEXT NOT NULL DEFAULT 'unknown'
        CHECK (content_type IN ('lecture', 'sermon', 'interview', 'film',
                                'music_video', 'unknown'));

ALTER TABLE videos
    ADD COLUMN content_type_confidence REAL
        CHECK (content_type_confidence IS NULL
               OR (content_type_confidence >= 0 AND content_type_confidence <= 1));

ALTER TABLE videos
    ADD COLUMN content_type_source TEXT NOT NULL DEFAULT 'auto'
        CHECK (content_type_source IN ('auto', 'auto_low_conf', 'user'));

-- D6 — search filter index.
CREATE INDEX videos_content_type ON videos (content_type);

-- For the categorize stage's claim query.
CREATE INDEX videos_content_type_pending
    ON videos (state)
    WHERE state = 'INDEXED' AND content_type = 'unknown' AND content_type_source = 'auto';

COMMIT;
```

### 2.3 Python — `source.py`

```python
"""ContentTypeSource — provenance for videos.content_type."""
from __future__ import annotations
from enum import StrEnum


class ContentTypeSource(StrEnum):
    AUTO = "auto"
    AUTO_LOW_CONF = "auto_low_conf"
    USER = "user"
```

### 2.4 Python — `features.py` (D7, D8)

```python
"""features.py — turn a media_features row into a numpy feature vector."""
from __future__ import annotations
from dataclasses import dataclass

import numpy as np


@dataclass(frozen=True)
class FeatureRow:
    duration_sec: float
    music_speech_ratio: float | None
    silence_pct: float | None
    mean_loudness_lufs: float | None
    diarization_turn_density: float | None
    segment_density: float | None


_FEATURE_NAMES = (
    "duration_min",
    "music_speech_ratio",
    "silence_pct",
    "mean_loudness_lufs",
    "turn_density",          # diarization OR segment density
)


def build_feature_vector(row: FeatureRow) -> np.ndarray:
    """5-feature vector. NULLs replaced with sentinel column means
    learned at training time (the model's pipeline includes a SimpleImputer)."""
    duration_min = max(row.duration_sec / 60.0, 0.0)
    msr = row.music_speech_ratio if row.music_speech_ratio is not None else np.nan
    sp = row.silence_pct if row.silence_pct is not None else np.nan
    lu = row.mean_loudness_lufs if row.mean_loudness_lufs is not None else np.nan
    # D8: diarization wins when present, else segment density.
    if row.diarization_turn_density is not None:
        td = row.diarization_turn_density
    elif row.segment_density is not None:
        td = row.segment_density
    else:
        td = np.nan
    return np.asarray([duration_min, msr, sp, lu, td], dtype=np.float32)


FEATURE_NAMES = _FEATURE_NAMES
```

### 2.5 Python — `classifier.py` (D1, D2)

```python
"""classifier.py — load the model, run predict_proba, decide label."""
from __future__ import annotations
import logging, pickle
from dataclasses import dataclass
from pathlib import Path

import numpy as np

log = logging.getLogger(__name__)

_MODEL_PATH = Path(__file__).parent / "model" / "v1.pkl"
_DEFAULT_THRESHOLD = 0.55


@dataclass(frozen=True)
class ClassifierResult:
    label: str
    confidence: float
    source: str           # 'auto' or 'auto_low_conf'
    probs: dict[str, float]


def load_model():
    with open(_MODEL_PATH, "rb") as f:
        return pickle.load(f)            # dict {model: ..., classes: [...]}


def classify(features: np.ndarray, *, threshold: float = _DEFAULT_THRESHOLD,
             model_state=None) -> ClassifierResult:
    state = model_state or load_model()
    model, classes = state["model"], state["classes"]
    if features.ndim == 1:
        features = features.reshape(1, -1)
    probs = model.predict_proba(features)[0]
    argmax_idx = int(np.argmax(probs))
    label = classes[argmax_idx]
    conf = float(probs[argmax_idx])
    if conf < threshold:
        return ClassifierResult(
            label="unknown", confidence=conf, source="auto_low_conf",
            probs={c: float(p) for c, p in zip(classes, probs)})
    return ClassifierResult(
        label=label, confidence=conf, source="auto",
        probs={c: float(p) for c, p in zip(classes, probs)})
```

### 2.6 Python — `probe_hook.py` (D3, D7)

```python
"""probe_hook.py — populate media_features at the probe stage tail.

Inputs come from the probe's already-computed ffprobe + silencedetect +
loudnorm outputs (Epic 1). We add no new ffmpeg invocations.
"""
from __future__ import annotations
from dataclasses import dataclass


@dataclass(frozen=True)
class ProbeOutputs:
    duration_sec: float
    silence_intervals: list[tuple[float, float]]    # (start, end) seconds
    loudness_lufs: float | None
    loudness_segments: list[dict]                   # per-window LUFS over time


def music_speech_ratio(loudness_segments: list[dict]) -> float:
    """D7 heuristic: fraction of audio that's "music-likely".

    Music: loudness in [-18, -12] LUFS for runs ≥ 30 s.
    Speech: loudness in [-25, -15] LUFS for shorter runs (< 5 s).
    Returns music / (music + speech). Default 0.5 when ambiguous.
    """
    if not loudness_segments:
        return 0.5
    music_sec = 0.0
    speech_sec = 0.0
    run_start = None
    run_lufs_band = None
    for seg in loudness_segments:
        lufs = seg["lufs"]
        in_music_band = -18.0 <= lufs <= -12.0
        in_speech_band = -25.0 <= lufs <= -15.0
        if in_music_band:
            run_start = run_start or seg["start"]
            run_lufs_band = "music"
            run_end = seg["end"]
            if run_end - run_start >= 30.0:
                music_sec += run_end - run_start
                run_start = None
        elif in_speech_band:
            speech_sec += seg["end"] - seg["start"]
            run_start = None
        else:
            run_start = None
    total = music_sec + speech_sec
    return (music_sec / total) if total > 0 else 0.5


def silence_pct(intervals: list[tuple[float, float]], duration_sec: float) -> float:
    if duration_sec <= 0:
        return 0.0
    return min(1.0, sum(e - s for s, e in intervals) / duration_sec)


_UPSERT_SQL = """
INSERT INTO media_features
    (video_id, music_speech_ratio, silence_pct, mean_loudness_lufs, computed_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (video_id) DO UPDATE
   SET music_speech_ratio = EXCLUDED.music_speech_ratio,
       silence_pct        = EXCLUDED.silence_pct,
       mean_loudness_lufs = EXCLUDED.mean_loudness_lufs,
       computed_at        = now()
"""


async def populate_media_features_from_probe(conn, *, video_id: str,
                                              probe: ProbeOutputs) -> None:
    msr = music_speech_ratio(probe.loudness_segments)
    sp  = silence_pct(probe.silence_intervals, probe.duration_sec)
    await conn.execute(_UPSERT_SQL, video_id, msr, sp, probe.loudness_lufs)
```

### 2.7 Python — `worker.py` (D4, D5, D11)

```python
"""worker.py — ContentTypeWorker.run: dispatched at the categorize stage."""
from __future__ import annotations
import logging, time
from dataclasses import dataclass

from .classifier import classify, load_model
from .features import build_feature_vector, FeatureRow
from .source import ContentTypeSource

log = logging.getLogger(__name__)

_DEFAULT_THRESHOLD = 0.55


@dataclass(frozen=True)
class CategorizeMetric:
    label: str
    confidence: float
    source: str
    skipped: bool


class ContentTypeWorker:
    def __init__(self, *, db_pool):
        self._db = db_pool
        self._model = load_model()                 # cached

    async def run(self, *, claimed_job, force: bool = False) -> dict:
        video_id = claimed_job.video_id
        async with self._db.acquire() as conn:
            v = await conn.fetchrow("""
                SELECT id, library_id, duration_sec, content_type,
                       content_type_source
                  FROM videos WHERE id = $1
                  FOR UPDATE
            """, video_id)
            if v is None:
                return {"skipped": True, "reason": "video_missing"}
            if v["content_type_source"] == ContentTypeSource.USER.value and not force:
                return {"skipped": True, "reason": "user_override",
                        "label": v["content_type"]}

            mf = await conn.fetchrow("""
                SELECT music_speech_ratio, silence_pct, mean_loudness_lufs,
                       diarization_turn_density, segment_density
                  FROM media_features WHERE video_id = $1
            """, video_id)
            if mf is None:
                return {"skipped": True, "reason": "no_features"}

            feature_row = FeatureRow(
                duration_sec=v["duration_sec"] or 0.0,
                music_speech_ratio=mf["music_speech_ratio"],
                silence_pct=mf["silence_pct"],
                mean_loudness_lufs=mf["mean_loudness_lufs"],
                diarization_turn_density=mf["diarization_turn_density"],
                segment_density=mf["segment_density"],
            )
            x = build_feature_vector(feature_row)

            threshold = await self._library_threshold(conn, v["library_id"])
            res = classify(x, threshold=threshold, model_state=self._model)

            await conn.execute("""
                UPDATE videos
                   SET content_type = $1,
                       content_type_confidence = $2,
                       content_type_source = $3,
                       updated_at = now()
                 WHERE id = $4
                   AND content_type_source <> 'user'
            """, res.label, res.confidence, res.source, video_id)

            return CategorizeMetric(label=res.label, confidence=res.confidence,
                                    source=res.source, skipped=False).__dict__

    async def _library_threshold(self, conn, library_id) -> float:
        row = await conn.fetchrow(
            "SELECT settings FROM libraries WHERE id=$1", library_id)
        if row is None:
            return _DEFAULT_THRESHOLD
        s = row["settings"] or {}
        return float(s.get("content_type_threshold", _DEFAULT_THRESHOLD))
```

### 2.8 Python — `train_content_type.py` (D9)

```python
"""train_content_type.py — re-train the content-type classifier from train.csv.

CI runs this and diffs the resulting pickle hash against the committed
file. Drift fails the build.
"""
from __future__ import annotations
import hashlib, pickle, sys
from pathlib import Path

import numpy as np
import pandas as pd
from sklearn.impute import SimpleImputer
from sklearn.linear_model import LogisticRegression
from sklearn.pipeline import Pipeline
from sklearn.preprocessing import StandardScaler

CSV = Path(__file__).parent.parent / "tests" / "fixtures" / "categorize" / "train.csv"
OUT = Path(__file__).parent.parent / "src" / "maktaba_pipeline" / "categorize" / "model" / "v1.pkl"


def main() -> int:
    df = pd.read_csv(CSV)
    X = df[["duration_min", "music_speech_ratio", "silence_pct",
            "mean_loudness_lufs", "turn_density"]].values.astype(np.float32)
    y = df["label"].values
    pipe = Pipeline([
        ("imputer", SimpleImputer(strategy="mean")),
        ("scaler", StandardScaler()),
        ("clf", LogisticRegression(
            max_iter=2000, multi_class="multinomial", random_state=42)),
    ])
    pipe.fit(X, y)
    state = {"model": pipe, "classes": list(pipe.named_steps["clf"].classes_)}
    bytes_ = pickle.dumps(state, protocol=4)
    OUT.write_bytes(bytes_)
    digest = hashlib.sha256(bytes_).hexdigest()
    print(f"trained → {OUT}\nsha256 = {digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

### 2.9 PATCH override (Go)

The PATCH handler from Plan 9.8 already exists; this plan adds one more
field path.

```go
// apps/api/internal/http/videos/patch.go (excerpt — only the new path)
type PatchBody struct {
    // ... existing fields ...
    ContentType *string `json:"content_type,omitempty"`
}

var allowedContentTypes = map[string]struct{}{
    "lecture": {}, "sermon": {}, "interview": {}, "film": {}, "music_video": {}, "unknown": {},
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
    // ... existing parse + auth ...

    if body.ContentType != nil {
        if _, ok := allowedContentTypes[*body.ContentType]; !ok {
            http.Error(w, "invalid content_type", http.StatusBadRequest)
            return
        }
        _, err := h.pool.Exec(r.Context(), `
            UPDATE videos
               SET content_type             = $1,
                   content_type_source      = 'user',
                   content_type_confidence  = NULL,
                   updated_at               = now()
             WHERE id = $2
        `, *body.ContentType, videoID)
        if err != nil {
            http.Error(w, "update failed", http.StatusInternalServerError); return
        }
    }
}
```

---

## 3. File-by-file scaffolding checklist

| Order | File | Symbols | Tests gating |
|-------|------|---------|--------------|
| 1 | `shared/db/migrations/0029_content_type.sql` | `media_features`, `videos.content_type/_confidence/_source`, `videos_content_type` index | `TestMigration0029` |
| 2 | `pipeline/.../categorize/source.py` | `ContentTypeSource` | `test_source_enum` |
| 3 | `pipeline/.../categorize/features.py` | `FeatureRow`, `build_feature_vector`, `FEATURE_NAMES` | `test_features_build` |
| 4 | `pipeline/.../categorize/classifier.py` | `ClassifierResult`, `load_model`, `classify` | `test_classifier_*` |
| 5 | `pipeline/.../categorize/probe_hook.py` | `ProbeOutputs`, `populate_media_features_from_probe`, `music_speech_ratio`, `silence_pct` | `test_probe_hook_*` |
| 6 | `pipeline/.../categorize/worker.py` | `ContentTypeWorker.run`, `CategorizeMetric` | `test_worker_*` |
| 7 | `pipeline/.../categorize/model/v1.pkl` | committed pickle (D1) | `test_model_pickle_hash_pinned` |
| 8 | `pipeline/scripts/train_content_type.py` | `main()` trainer | (CI drift check) |
| 9 | `pipeline/tests/fixtures/categorize/train.csv` | 120 labeled examples (D9) | (used by trainer) |
| 10 | `pipeline/.../pipeline/stages/probe.py` (extend) | call `populate_media_features_from_probe` | `test_probe_writes_features` |
| 11 | `pipeline/.../pipeline/stages/categorize.py` | claim adapter to `ContentTypeWorker` | `test_categorize_stage_*` |
| 12 | `apps/api/internal/http/videos/patch.go` (extend) | `content_type` PATCH path | `TestPatchVideo_ContentType_*` |

---

## 4. Test cases

### 4.1 `test_classifier_deterministic_5_class_fixture` (story-named)

```python
def test_classifier_returns_expected_labels_for_fixture_set():
    """5 fixtures, one per class, each returns the right argmax."""
    fixtures = [
        ("lecture",     FeatureRow(duration_sec=3600, music_speech_ratio=0.05,
                                    silence_pct=0.10, mean_loudness_lufs=-22,
                                    diarization_turn_density=None,
                                    segment_density=18.0)),
        ("sermon",      FeatureRow(duration_sec=2700, music_speech_ratio=0.10,
                                    silence_pct=0.18, mean_loudness_lufs=-23,
                                    diarization_turn_density=None,
                                    segment_density=15.0)),
        ("interview",   FeatureRow(duration_sec=1800, music_speech_ratio=0.05,
                                    silence_pct=0.05, mean_loudness_lufs=-21,
                                    diarization_turn_density=10.0,
                                    segment_density=None)),
        ("film",        FeatureRow(duration_sec=5400, music_speech_ratio=0.40,
                                    silence_pct=0.20, mean_loudness_lufs=-18,
                                    diarization_turn_density=None,
                                    segment_density=8.0)),
        ("music_video", FeatureRow(duration_sec=240,  music_speech_ratio=0.85,
                                    silence_pct=0.02, mean_loudness_lufs=-12,
                                    diarization_turn_density=None,
                                    segment_density=2.0)),
    ]
    state = load_model()
    for expected, row in fixtures:
        x = build_feature_vector(row)
        res = classify(x, threshold=0.55, model_state=state)
        assert res.label == expected, \
            f"expected {expected}, got {res.label} (probs={res.probs})"
```

### 4.2 `test_short_clip_under_60s_yields_unknown` (edge)

```python
def test_under_60s_clip_falls_below_threshold():
    """Ultra-short clip → confidence < 0.55 → unknown (story edge case)."""
    row = FeatureRow(
        duration_sec=30, music_speech_ratio=0.5, silence_pct=0.3,
        mean_loudness_lufs=-20, diarization_turn_density=None,
        segment_density=2.0)
    x = build_feature_vector(row)
    res = classify(x, threshold=0.55)
    assert res.label == "unknown"
    assert res.source == "auto_low_conf"
```

### 4.3 `test_film_45min_classified_film` (AC-2 integration)

```python
async def test_45min_film_fixture_classified_as_film(
    db, video_factory, content_type_worker, fixtures,
):
    v = await video_factory.fresh(state="INDEXED", duration_sec=2700)
    await db.execute("""
        INSERT INTO media_features
            (video_id, music_speech_ratio, silence_pct, mean_loudness_lufs,
             segment_density)
        VALUES ($1, $2, $3, $4, $5)
    """, v.id, 0.42, 0.18, -19.0, 7.5)

    job = await db.queue_categorize_job(video_id=v.id)
    metric = await content_type_worker.run(claimed_job=job)
    assert metric["label"] == "film"
    row = await db.fetchrow(
        "SELECT content_type, content_type_source FROM videos WHERE id=$1", v.id)
    assert row["content_type"] == "film"
    assert row["content_type_source"] == "auto"
```

### 4.4 `test_45min_sermon_fixture_classified_sermon` (AC-2 integration)

```python
async def test_45min_sermon_fixture_classified_as_sermon(
    db, video_factory, content_type_worker,
):
    v = await video_factory.fresh(state="INDEXED", duration_sec=2700)
    await db.execute("""
        INSERT INTO media_features
            (video_id, music_speech_ratio, silence_pct, mean_loudness_lufs,
             segment_density)
        VALUES ($1, $2, $3, $4, $5)
    """, v.id, 0.10, 0.18, -23.0, 15.0)

    job = await db.queue_categorize_job(video_id=v.id)
    metric = await content_type_worker.run(claimed_job=job)
    assert metric["label"] == "sermon"
```

### 4.5 `test_user_override_sticks_across_reprocess` (AC-3, story-named)

```python
async def test_user_pinned_content_type_preserved_on_recategorize(
    db, video_factory, content_type_worker,
):
    v = await video_factory.fresh(state="INDEXED", duration_sec=3600)
    await db.execute("""
        INSERT INTO media_features (video_id, music_speech_ratio, silence_pct,
                                    mean_loudness_lufs, segment_density)
        VALUES ($1, 0.05, 0.10, -22, 18)
    """, v.id)
    # User patches type=film.
    await db.execute("""
        UPDATE videos SET content_type='film', content_type_source='user'
         WHERE id=$1
    """, v.id)

    # Re-categorize default (no force).
    metric = await content_type_worker.run(claimed_job=await db.queue_categorize_job(v.id))
    assert metric["skipped"] is True
    assert metric["reason"] == "user_override"
    assert metric["label"] == "film"

    # With force=True, the auto value overrides.
    metric_forced = await content_type_worker.run(
        claimed_job=await db.queue_categorize_job(v.id), force=True)
    assert metric_forced["skipped"] is False
    row = await db.fetchrow(
        "SELECT content_type, content_type_source FROM videos WHERE id=$1", v.id)
    assert row["content_type"] == "lecture"
    assert row["content_type_source"] == "auto"
```

### 4.6 `test_probe_hook_writes_features` (AC-1)

```python
async def test_probe_writes_media_features_row(db, video_factory):
    v = await video_factory.fresh(duration_sec=3600)
    probe = ProbeOutputs(
        duration_sec=3600,
        silence_intervals=[(0, 5), (3500, 3550)],
        loudness_lufs=-22.0,
        loudness_segments=[
            {"start": 0,    "end": 60,    "lufs": -22.0},
            {"start": 60,   "end": 3540,  "lufs": -22.5},
            {"start": 3540, "end": 3600,  "lufs": -22.3},
        ])
    async with db.acquire() as conn:
        await populate_media_features_from_probe(
            conn, video_id=v.id, probe=probe)
    row = await db.fetchrow(
        "SELECT music_speech_ratio, silence_pct, mean_loudness_lufs "
        "FROM media_features WHERE video_id=$1", v.id)
    assert row is not None
    assert 0.0 <= row["music_speech_ratio"] <= 1.0
    assert abs(row["silence_pct"] - (55.0 / 3600.0)) < 1e-3
    assert row["mean_loudness_lufs"] == -22.0
```

### 4.7 `test_model_pickle_hash_pinned` (D9 CI guard)

```python
def test_committed_pickle_hash_matches_csv_retrain(tmp_path):
    """Re-train from train.csv and assert SHA256 matches the committed pickle."""
    import subprocess, hashlib
    from pathlib import Path

    repo_root = Path(__file__).resolve().parents[3]
    committed = repo_root / "pipeline/src/maktaba_pipeline/categorize/model/v1.pkl"
    expected = hashlib.sha256(committed.read_bytes()).hexdigest()

    # Train into a temp file and compare.
    out = tmp_path / "v1.pkl"
    subprocess.run(["python", "pipeline/scripts/train_content_type.py",
                    "--out", str(out)], check=True)
    actual = hashlib.sha256(out.read_bytes()).hexdigest()
    assert actual == expected, \
        "model drift: committed pickle differs from re-trained pickle"
```

### 4.8 `TestPatchVideo_ContentType` (Go, AC-3)

```go
func TestPatchVideo_ContentType_SetsSourceUser(t *testing.T) {
    db := testdb.Fresh(t)
    libID := testdb.SeedLibrary(t, db, "videos")
    vid := testdb.SeedVideo(t, db, libID, "INDEXED")

    body := `{"content_type":"lecture"}`
    req := httptest.NewRequest("PATCH", "/api/videos/"+vid.String(),
        strings.NewReader(body))
    req = withChiCtx(req, "id", vid.String())
    rr := httptest.NewRecorder()
    h := videos.NewHandler(db.Pool, slog.Default())
    h.Patch(rr, req)
    require.Equal(t, http.StatusOK, rr.Code)

    var ct, src string
    require.NoError(t, db.Pool.QueryRow(t.Context(),
        `SELECT content_type, content_type_source FROM videos WHERE id=$1`,
        vid).Scan(&ct, &src))
    require.Equal(t, "lecture", ct)
    require.Equal(t, "user", src)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Music-heavy video classified as `music_video`** even with speech intros (story-named). The dominant feature is `music_speech_ratio ≥ 0.7`; the model learns to weight that strongly. | `test_classifier_returns_expected_labels_for_fixture_set` (music_video case) |
| E2  | **Ultra-short clip < 60 s** (story-named). Confidence falls below 0.55 due to short-duration training examples → `unknown`. | `test_under_60s_clip_falls_below_threshold` |
| E3  | **`media_features` row missing** (e.g., probe stage skipped or crashed). Worker returns `skipped: true, reason: 'no_features'`; `videos.content_type` stays `unknown`. The probe stage owns populating the row; this is a recovery path. | `test_worker_skips_when_features_missing` |
| E4  | **Diarization off → `diarization_turn_density` NULL.** `build_feature_vector` falls back to `segment_density` (D8); the model handles either via the imputer. | `test_features_build_with_segment_density_only` |
| E5  | **Model file missing or unreadable.** `load_model()` raises; the worker bubbles up; the categorize stage marks the job failed (Epic 7 retry policy). The CI drift check (D9) prevents this in deployed builds. | `test_load_model_raises_on_missing_pickle` |
| E6  | **PATCH content_type while categorize stage is running.** `UPDATE … WHERE content_type_source <> 'user'` clause in the worker prevents accidental overwrite even if the PATCH commits between SELECT and UPDATE. | `test_concurrent_patch_during_categorize_no_overwrite` |
| E7  | **Library threshold override** (D2). `library.settings.content_type_threshold = 0.7` raises the bar; videos that would have been `lecture` at 0.6 now become `unknown`. | `test_library_threshold_override` |
| E8  | **Re-probe causes `media_features` UPSERT.** `populate_media_features_from_probe` uses ON CONFLICT DO UPDATE; the categorize stage runs again on next INDEXED transition; user override (D5) is preserved. | `test_reprobe_does_not_overwrite_user_pin` |
| E9  | **Backfill of `content_type` for existing videos.** Migration 0029 sets default `'unknown'`; no auto-classification on migration. Operators run `maktaba-api content-type-rebuild` (mirrors `lang-rebuild`) to backfill — admin-only opt-in. | Operational note; no test in scope here. |
| E10 | **Filter `?type=lecture` performance** (AC-4). With `videos_content_type` index + the existing `(library_id, state)` index, the query plan uses a bitmap-AND of the two; ≤ 5 ms on the 50k fixture. | `test_filter_type_uses_index` |
| E11 | **Model drift in CI** (D9). Re-training from `train.csv` produces a different pickle than committed; `test_model_pickle_hash_pinned` fails; the developer must commit the new pickle (and document why the model changed). | `test_model_pickle_hash_pinned` |
| E12 | **All confidences nearly equal** (model genuinely uncertain across all 6 classes). argmax wins by 0.001 over the next class with conf 0.18. Threshold check writes `'unknown'`. | covered by D2 threshold logic. |

---

## 6. Acceptance checklist

- [ ] **A1** Probe stage tail populates `media_features (music_speech_ratio, silence_pct, mean_loudness_lufs)`; `segment_density` and `diarization_turn_density` are filled by transcribe + diarize tails. (`test_probe_writes_media_features_row`)
- [ ] **A2** Categorize stage reads `media_features`, runs the model, writes `videos.content_type` to argmax class with `content_type_confidence`; if confidence < threshold (default 0.55, per-library overridable), label is `'unknown'` with source `'auto_low_conf'`. (`test_classifier_returns_expected_labels_for_fixture_set`, `test_under_60s_clip_falls_below_threshold`, `test_45min_film_fixture_classified_as_film`, `test_45min_sermon_fixture_classified_as_sermon`)
- [ ] **A3** Manual override: PATCH `content_type` sets `content_type_source = 'user'`; auto-classifier respects the user value unless `?force=true` is passed to the re-categorize trigger. (`TestPatchVideo_ContentType_SetsSourceUser`, `test_user_pinned_content_type_preserved_on_recategorize`)
- [ ] **A4** Index `videos_content_type` on `videos (content_type)` exists after migration 0029. (`TestMigration0029`)
- [ ] **A5** Migration `0029_content_type.sql` creates `media_features` and the `videos.content_type/_confidence/_source` columns + CHECKs. (`TestMigration0029`)
- [ ] **A6** Deterministic classifier: same model + same features → same output for the 5-fixture set covering each class. (`test_classifier_returns_expected_labels_for_fixture_set`)
- [ ] **A7** `unknown` is both a model class and a low-confidence fallback; the classifier never writes a non-`unknown` class with confidence < threshold. (`test_under_60s_clip_falls_below_threshold`)
- [ ] **A8** `media_features` is upserted; re-probe doesn't lose data. (`test_probe_hook_upsert_keeps_data`)
- [ ] **A9** Model pickle is reproducible: `pipeline/scripts/train_content_type.py` re-creates the committed pickle byte-for-byte; CI guard `test_model_pickle_hash_pinned` fails on drift. (`test_model_pickle_hash_pinned`)
- [ ] **A10** Diarization-turn-density wins over segment-density when both are present (D8). (`test_features_build_diarization_takes_precedence`)

---

## 7. Performance budget

(Story 9.7 owns the explicit 50 ms target; this story has no fixed
budget, but the categorize stage is per-video and lightweight.)

| Phase | Cost (per video) | Notes |
|-------|------------------|-------|
| `SELECT video, media_features` | ~200 µs | two PK lookups, joinable. |
| `build_feature_vector` | < 1 µs | dataclass + numpy array. |
| `model.predict_proba` (LogisticRegression, 5 features) | ~10 µs | sklearn vectorized. |
| `UPDATE videos SET content_type, ...` | ~100 µs | indexed PK update. |
| **Total** | **< 1 ms per video** | A 50k-video backfill is ~50 s of pure CPU work. |

The probe-tail population path is dominated by the existing probe
stage's I/O; the added work is one INSERT (~200 µs).

---

## 8. Operational notes

- **Re-categorize CLI:** `maktaba-api content-type-rebuild --library-id=<uuid> [--force] [--dry-run]` re-runs the worker over every video in the library; honours `--force` to override user pins. Mirrors the language rebuild from Plan 9.8.
- **Metrics:**
  - `categorize_inference_total{label, source}` — counter.
  - `categorize_inference_duration_seconds` — histogram; expected p99 < 5 ms.
  - `categorize_low_confidence_total{library_id}` — counter; high values flag a library where the threshold may need tuning.
  - `categorize_skipped_total{reason}` — counter.
- **Model lifecycle:** any change to feature columns or class set requires (a) updating `train.csv`, (b) re-running the trainer, (c) committing the new pickle, (d) bumping the file name to `v2.pkl` and adding a `model_version` column (deferred until v1.1; v1 ships only `v1.pkl`).
- **Search filter index:** `videos_content_type` complements the existing `(library_id, state)` index from architecture §8.1; the planner combines them via bitmap-AND.
