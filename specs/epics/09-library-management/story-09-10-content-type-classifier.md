# Story 9.10 — Auto-categorization: content type classifier

A small classifier (§5.2) predicts `content_type ∈ {lecture, sermon,
interview, film, music_video, unknown}` from features computed during
probe and audio extract: duration, speaker turn density (from
diarization if on, segment density otherwise), and music-vs-speech ratio
from `silencedetect` + `loudnorm` stats.

**AC-1 — Feature extraction during probe.**
- **Given** the probe stage,
- **When** it completes,
- **Then** `media_features` is populated (schema in [README.md](README.md)).

**AC-2 — Classifier inference.**
- **Given** a row in `media_features` and the trained model,
- **When** the categorize stage runs,
- **Then** `videos.content_type` is set to the argmax class with
  confidence; if confidence < 0.55 → `unknown`.

**AC-3 — Manual override.**
- **Given** a user sets `content_type` via PATCH,
- **When** auto-classifier runs again (e.g., after re-probe),
- **Then** the user value is preserved unless `?force=true` is set.

**AC-4 — Required index for filtering.**
- **Given** Epic 7 Story 7.4's filter `?type=lecture`,
- **When** the schema is migrated,
- **Then** an index `CREATE INDEX videos_content_type ON videos
  (content_type)` exists.

**Test cases:**
- Unit: deterministic classifier output for a 5-fixture set covering each
  class.
- Integration: a 90-min film fixture → `film`; a 45-min sermon fixture
  → `sermon`.
- Integration: per-user override sticks across reprocess.

**Edge cases:**
- Music-heavy video (concert) classified as `music_video` even when it
  has speech intros — score is from the dominant class.
- An ultra-short clip (< 60 s) — confidence floor isn't met → `unknown`.
