# Implementation Plan — Story 9.10 Content Type Classifier

> Companion to [story-09-10-content-type-classifier.md](story-09-10-content-type-classifier.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on the probe (Epic 1) and audio-extract (Epic 2) stages, the
> diarization stage (Pipeline §5.2 and Story 9.11), and Story 9.7's
> `by_content_type_jsonb` cache bucket.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Stage | New `categorize` stage at `priority=200`. Runs after `INDEXED` (or at minimum after probe + audio extract — diarization is optional). One job per video; idempotent. |
| Feature extraction | Inline in the probe stage (Epic 1 Story 1.7's plan adds the FFmpeg call to compute silence and loudness). For this story we *consume* `media_features`, we don't compute it. |
| Model artifact | A single sklearn pipeline saved to `models/content_type/v1.joblib` checked into the repo (small ~50 KB). Loaded once per worker; thread-safe. Versioned filename so a v2 retrain ships in a separate file. |
| Decision threshold | `confidence < 0.55` → `unknown`. Hard-coded; tested. |
| User override | A new `videos.content_type_source` column: `'classifier'` | `'user'`. PATCH from Epic 7 Story 7.4 sets `'user'`; `?force=true` lets the classifier overwrite. |
| Filter index | `videos_content_type` index per AC-4. |
| Out of scope | Training the model (separate spec under `specs/research/content-type-training.md`); the probe-stage feature extraction (Epic 1 Story 1.7); the multi-class boundaries between similar classes (model owns those). |

## 1. Architecture diagram

```
   probe stage (Epic 1) → media_features row written
   audio_extract (Epic 2) → silence_pct, mean_loudness_lufs in media_features
   diarization (Story 9.11, optional) → diarization_turn_density
                                        OR fallback: segment_density from
                                        transcript_segments
        ↓
   indexer.commit(video_id) → enqueue(stage=categorize, priority=200)
        ↓
   categorize_worker.run(video_id)
      ├─ load media_features, video, library
      ├─ if videos.content_type_source = 'user' AND not payload.force: skip
      ├─ assemble feature vector:
      │     [duration_sec / 3600,             # hours, normalized
      │      silence_pct,
      │      music_speech_ratio,
      │      diarization_turn_density OR segment_density,
      │      mean_loudness_lufs / -23.0]      # LUFS / EBU R128 ref
      ├─ probabilities = model.predict_proba(features)
      ├─ argmax_class, argmax_p
      ├─ result = argmax_class if argmax_p >= 0.55 else 'unknown'
      └─ UPDATE videos
              SET content_type        = $result,
                  content_type_source = 'classifier',
                  updated_at          = now()
            WHERE id = $video_id
              AND (content_type_source IS DISTINCT FROM 'user' OR $force)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/content_type/__init__.py` | Re-exports. |
| `pipeline/src/maktaba_pipeline/content_type/classifier.py` | The runtime classifier — loads `v1.joblib`, exposes `predict(features) -> (label, confidence)`. |
| `pipeline/src/maktaba_pipeline/content_type/features.py` | Feature assembly from `media_features` + `videos` + segment-density fallback. |
| `pipeline/src/maktaba_pipeline/content_type/worker.py` | `run_categorize_job(video_id)`. |
| `pipeline/tests/content_type/test_classifier.py` | Deterministic test set per §6.1. |
| `pipeline/tests/content_type/test_worker.py` | Worker integration per §6.2. |
| `models/content_type/v1.joblib` | The trained model artifact (binary). |
| `models/content_type/v1.metadata.json` | Class names, feature schema, training-data summary, sklearn version. |
| `shared/db/migrations/0038_videos_content_type.sql` | Adds `content_type` (if not present), `content_type_source`, the index. |
| `shared/db/migrations/0038b_media_features.sql` | **Owns** the canonical `media_features` migration (architecture). Schema: `(video_id UUID PK, features JSONB NOT NULL, model TEXT NOT NULL, updated_at TIMESTAMPTZ DEFAULT now())`. The probe stage populates it; this stage consumes it. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/jobs/dispatcher.py` | Register `categorize` stage handler. |
| `pipeline/src/maktaba_pipeline/index/commit.py` | After commit, enqueue `categorize` (Story 9.18 also enqueues from here; one combined `enqueue_post_index` helper). |
| `shared/db/migrations/0038a_processing_jobs_stage_categorize.sql` | New migration that ALTERs the `processing_jobs.stage` CHECK to include `'categorize'`. Does **not** edit Epic 6's `0010_processing_jobs.sql` (immutable once shipped). Renumber sequentially after the last Epic 6 migration. |
| `api/internal/handlers/videos/patch.go` | When PATCH sets `content_type`, also stamp `content_type_source = 'user'`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.10. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/content_type/classifier.py
from __future__ import annotations
from dataclasses import dataclass
from enum import StrEnum

CONFIDENCE_THRESHOLD = 0.55


class ContentType(StrEnum):
    LECTURE     = "lecture"
    SERMON      = "sermon"
    INTERVIEW   = "interview"
    FILM        = "film"
    MUSIC_VIDEO = "music_video"
    UNKNOWN     = "unknown"


class ContentTypeSource(StrEnum):
    CLASSIFIER = "classifier"
    USER       = "user"


@dataclass(slots=True, frozen=True)
class Prediction:
    label: ContentType
    confidence: float
    raw_probabilities: dict[ContentType, float]
```

## 3. Database migration

`shared/db/migrations/0038_videos_content_type.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- The architecture mentioned content_type but the canonical column
-- exists from arch §8.1; this migration is defensive.
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS content_type TEXT,
    ADD COLUMN IF NOT EXISTS content_type_source TEXT
        CHECK (content_type_source IS NULL
               OR content_type_source IN ('classifier','user'));

ALTER TABLE videos
    ADD CONSTRAINT IF NOT EXISTS videos_content_type_chk
    CHECK (content_type IS NULL OR content_type IN
           ('lecture','sermon','interview','film','music_video','unknown'));

-- AC-4: filter on ?type= must be O(log n).
CREATE INDEX IF NOT EXISTS videos_content_type
    ON videos (content_type);

-- For "untyped videos" UI list:
CREATE INDEX IF NOT EXISTS videos_content_type_unknown_partial
    ON videos (library_id) WHERE content_type IS NULL OR content_type = 'unknown';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_content_type_unknown_partial;
DROP INDEX IF EXISTS videos_content_type;
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_content_type_chk;
ALTER TABLE videos DROP COLUMN IF EXISTS content_type_source;
-- (content_type column intentionally not dropped; data preservation)
-- +goose StatementEnd
```

### 3.1 `media_features` migration (canonical, owned here)

`shared/db/migrations/0038b_media_features.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Architecture canonicalizes media_features as
--   (video_id UUID PK, features JSONB NOT NULL, model TEXT NOT NULL,
--    updated_at TIMESTAMPTZ DEFAULT now())
-- The probe stage (Epic 1 Story 1.7) populates `features` JSONB; this
-- stage reads documented keys (silence_pct, music_speech_ratio,
-- mean_loudness_lufs, diarization_turn_density, segment_density). The
-- README's older sketch with one-column-per-feature is superseded by
-- this JSONB shape.
CREATE TABLE media_features (
    video_id    UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    features    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    model       TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT media_features_features_object_chk
        CHECK (jsonb_typeof(features) = 'object')
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS media_features;
-- +goose StatementEnd
```

When this stage reads media_features it does so via JSONB key
extraction:

```sql
SELECT v.duration_sec,
       (mf.features ->> 'silence_pct')::float              AS silence_pct,
       (mf.features ->> 'music_speech_ratio')::float       AS music_speech_ratio,
       (mf.features ->> 'diarization_turn_density')::float AS diarization_turn_density,
       (mf.features ->> 'segment_density')::float          AS segment_density,
       (mf.features ->> 'mean_loudness_lufs')::float       AS mean_loudness_lufs
  FROM videos v
  LEFT JOIN media_features mf ON mf.video_id = v.id
 WHERE v.id = $1
```

(The §4.1 feature-assembly Python uses this query shape.)

## 4. Code scaffolding

### 4.1 Feature assembly

```python
# pipeline/src/maktaba_pipeline/content_type/features.py
import numpy as np
from uuid import UUID

FEATURE_NAMES = (
    "duration_hours",
    "silence_pct",
    "music_speech_ratio",
    "turn_density",
    "mean_loudness_norm",
)


async def assemble_features(db, video_id: UUID) -> tuple[np.ndarray, dict] | None:
    # Canonical media_features shape: (video_id, features JSONB, model, updated_at).
    row = await db.fetchrow(
        """
        SELECT v.duration_sec,
               (mf.features ->> 'silence_pct')::float              AS silence_pct,
               (mf.features ->> 'music_speech_ratio')::float       AS music_speech_ratio,
               (mf.features ->> 'diarization_turn_density')::float AS diarization_turn_density,
               (mf.features ->> 'segment_density')::float          AS segment_density,
               (mf.features ->> 'mean_loudness_lufs')::float       AS mean_loudness_lufs
          FROM videos v
          LEFT JOIN media_features mf ON mf.video_id = v.id
         WHERE v.id = $1
        """,
        video_id,
    )
    if row is None or row["duration_sec"] is None:
        return None
    if row["silence_pct"] is None or row["mean_loudness_lufs"] is None:
        return None  # probe stage hasn't completed for this video yet

    turn_density = row["diarization_turn_density"]
    if turn_density is None:
        turn_density = row["segment_density"] or 0.0  # fallback

    feat = np.array([
        (row["duration_sec"] or 0.0) / 3600.0,
        row["silence_pct"] or 0.0,
        row["music_speech_ratio"] or 0.0,
        float(turn_density),
        (row["mean_loudness_lufs"] or -23.0) / -23.0,
    ], dtype=np.float32)

    raw = {n: float(v) for n, v in zip(FEATURE_NAMES, feat)}
    return feat, raw
```

### 4.2 Classifier wrapper

```python
# pipeline/src/maktaba_pipeline/content_type/classifier.py
import json
from importlib.resources import files

import joblib
import numpy as np


class ContentTypeClassifier:
    _instance = None

    @classmethod
    def get(cls) -> "ContentTypeClassifier":
        if cls._instance is None:
            cls._instance = cls._load_v1()
        return cls._instance

    @classmethod
    def _load_v1(cls):
        path = files("maktaba_pipeline.content_type").joinpath(
            "../../../models/content_type/v1.joblib")
        meta_path = files("maktaba_pipeline.content_type").joinpath(
            "../../../models/content_type/v1.metadata.json")
        model = joblib.load(str(path))
        meta = json.loads(meta_path.read_text())
        return cls(model=model, classes=meta["classes"],
                   feature_names=tuple(meta["feature_names"]))

    def __init__(self, *, model, classes, feature_names):
        self._model = model
        self._classes = classes  # ordered like model.classes_
        self._features = feature_names

    def predict(self, features: np.ndarray) -> Prediction:
        if features.shape != (len(self._features),):
            raise ValueError(f"feature shape mismatch: got {features.shape}")
        probs = self._model.predict_proba(features.reshape(1, -1))[0]
        idx = int(np.argmax(probs))
        conf = float(probs[idx])
        label = self._classes[idx]
        if conf < CONFIDENCE_THRESHOLD:
            label = ContentType.UNKNOWN.value
        return Prediction(
            label=ContentType(label),
            confidence=conf,
            raw_probabilities={ContentType(self._classes[i]): float(p)
                               for i, p in enumerate(probs)},
        )
```

### 4.3 Worker

```python
# pipeline/src/maktaba_pipeline/content_type/worker.py
async def run_categorize_job(db, job) -> None:
    video_id = job.video_id
    payload = job.payload or {}
    force = bool(payload.get("force", False))

    # Skip if user-set and not forced.
    src = await db.fetchval(
        "SELECT content_type_source FROM videos WHERE id=$1", video_id)
    if src == "user" and not force:
        content_type_skipped_total.labels(reason="user_set").inc()
        return

    fa = await assemble_features(db, video_id)
    if fa is None:
        content_type_skipped_total.labels(reason="features_missing").inc()
        return

    feat, raw = fa
    pred = ContentTypeClassifier.get().predict(feat)

    await db.execute(
        "UPDATE videos "
        "   SET content_type = $1, "
        "       content_type_source = 'classifier', "
        "       updated_at = now() "
        " WHERE id = $2 "
        "   AND ($3 OR content_type_source IS DISTINCT FROM 'user')",
        pred.label.value, video_id, force,
    )
    content_type_predict_total.labels(label=pred.label.value).inc()
    content_type_confidence.observe(pred.confidence)
```

### 4.4 PATCH handler stamps `'user'`

```sql
-- name: PatchVideoContentTypeByUser :exec
UPDATE videos
   SET content_type = $2,
       content_type_source = 'user',
       updated_at = now()
 WHERE id = $1;
```

## 5. Test plan

### 5.1 Classifier deterministic tests (`test_classifier.py`)

A fixture set of 5 hand-crafted feature vectors, one per class. Each
vector is what you'd expect from a typical video of that class, derived
from the training data's median row.

| Test | What it pins |
|---|---|
| `test_lecture_fixture_predicts_lecture` | Vector → `lecture`; confidence ≥ 0.7. |
| `test_sermon_fixture_predicts_sermon` | Vector → `sermon`. |
| `test_interview_fixture_predicts_interview` | Vector → `interview`. |
| `test_film_fixture_predicts_film` | Vector → `film`. |
| `test_music_video_fixture_predicts_music_video` | Vector → `music_video`. |
| `test_borderline_below_threshold_predicts_unknown` | Synthetic vector that pushes max-prob to 0.50 → `unknown`. AC-2. |
| `test_unknown_when_short_clip` | `duration_hours = 0.01` (36 s) feature → max-prob falls below 0.55 → `unknown`. Edge case from story. |
| `test_predict_proba_sums_to_one` | Defensive — every prediction's `raw_probabilities` sums to 1.0 ± 1e-6. |
| `test_feature_shape_mismatch_raises` | Passing a 4-length vector → `ValueError`. |

### 5.2 Worker integration (`test_worker.py`)

| Test | What it pins |
|---|---|
| `test_writes_classifier_label` | Insert features → run worker → `videos.content_type='lecture'`, `content_type_source='classifier'`. |
| `test_user_set_not_overwritten` | Pre-state `content_type_source='user'` → worker no-ops; counter increments. AC-3. |
| `test_force_overwrites_user_set` | Same pre-state with `payload.force=True` → worker writes `classifier`. AC-3. |
| `test_features_missing_skipped` | `media_features` row absent → worker skips with reason `features_missing`. |
| `test_per_user_override_sticks_across_reprocess` | Run worker → user PATCH → run worker again (no force) → user value preserved. Story integration. |
| `test_segment_density_fallback_when_no_diarization` | `diarization_turn_density IS NULL` → uses `segment_density`. |

### 5.3 Cross-language label parity

Class names must be a single source of truth. Add a small Go test
`api/internal/db/content_type_test.go` that loads the
`v1.metadata.json` and asserts the Go-side `enum ContentType` lists the
same six values.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Music-heavy concert with brief speech intros | The model's training set classifies these by dominant class; `music_video` wins. The story documents this as expected. | Documented; manual smoke test. |
| Ultra-short clip (< 60 s) | Confidence floor isn't met → `unknown`. | `test_unknown_when_short_clip` |
| `media_features` partial (probe done, audio_extract not) | Some required features (`mean_loudness_lufs`) NULL → worker skips with `features_missing`. The categorize stage retries on the next probe job. | `test_features_missing_skipped` |
| `diarization_turn_density` NULL because library has `diarize: false` | Use `segment_density` from transcript segments as a proxy. | `test_segment_density_fallback_when_no_diarization` |
| User sets `content_type='news'` (not in enum) | API validator rejects with 422; the CHECK constraint at DB level is the second line of defense. | Validator test in Epic 7 Story 7.4. |
| Model artifact missing on disk | `ContentTypeClassifier.get()` raises at first call; the worker logs and marks the job FAILED with `error.code='model-missing'`. The reaper retries — but it won't help. Operator runbook covers manual recovery (restore the .joblib). | `test_classifier_get_raises_when_model_missing` |
| `predict_proba` returns NaN (corrupted input) | `np.argmax` returns 0; we still write `unknown` because conf is NaN < threshold. Defensive but documented. | `test_predict_handles_nan_features` |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `categorize_confidence_threshold` (constant) | 0.55 | Hard-coded per AC-2. |
| Model artifact path | `models/content_type/v1.joblib` | Loaded at first call. |

## 8. Dependencies

| Dep | Version | Why |
|---|---|---|
| `scikit-learn` | ≥ 1.4 | Same version as Story 9.9; pickle compat. |
| `joblib` | already pinned | Model load. |
| `numpy` | ≥ 1.26 | Feature vector. |

## 9. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/content_type/` package created.
- [ ] Indexer commit enqueues `categorize` stage.
- [ ] Worker honors `force` and skips `'user'`-set rows.

**Migration**
- [ ] `videos.content_type` and `content_type_source` exist with CHECKs.
- [ ] `videos_content_type` index exists. AC-4.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: `media_features` populated by probe (verified via cross-story probe contract; this story tolerates absence).
- [ ] AC-2: confidence < 0.55 → `unknown`.
- [ ] AC-3: user PATCH stamps `'user'`; classifier no-ops without `force=true`.
- [ ] AC-4: `?type=lecture` API filter is index-backed.

**Model**
- [ ] `models/content_type/v1.joblib` checked into the repo.
- [ ] `models/content_type/v1.metadata.json` documents the class list, feature names, sklearn version, and training-data summary.

**Observability**
- [ ] Counter `content_type_predict_total{label}`.
- [ ] Counter `content_type_skipped_total{reason}`.
- [ ] Histogram `content_type_confidence`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.10.
- [ ] `specs/research/content-type-training.md` (separate doc) covers retraining.
