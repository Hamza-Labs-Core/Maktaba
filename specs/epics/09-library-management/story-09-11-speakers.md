# Story 9.11 — Speakers, voiceprints, naming, merge

§5.2 + §9.6 endpoints. Diarization is opt-in per library; when on,
voiceprints are matched against per-library `speakers`.

**AC-1 — New voice → unknown speaker.**
- **Given** diarization detects a voice not matching any existing
  `speakers.voiceprint` (cosine distance > `speaker_match_threshold`,
  default 0.35),
- **When** the segment is committed,
- **Then** a new `speakers (library_id, name=NULL, voiceprint)` row is
  created with `name = "unknown-{n}"` rendered in the UI; n is the
  count of unknowns + 1.

**AC-2 — Match assignment.**
- **Given** a new segment whose voiceprint matches an existing speaker
  within threshold,
- **When** committed,
- **Then** `segment_speakers (segment_id, speaker_id, confidence)`
  is inserted; the speaker's voiceprint is *not* updated (avoid drift).

**AC-3 — User naming.**
- **Given** an unknown speaker,
- **When** the user PATCHes a name via Epic 7 Story 7.14 endpoint,
- **Then** the name is set; UI relabels every prior segment by reference.

**AC-4 — Merge.**
- **Given** two speakers found to be the same person,
- **When** `POST /api/speakers/merge {keep, drop}` is called,
- **Then** as in Epic 7 Story 7.14 AC-4: `segment_speakers` rows are
  rewritten in one transaction. The voiceprint of the merged speaker is
  *not* recomputed; it remains the kept speaker's original.

**AC-5 — Cross-library isolation.**
- **Given** speakers in two libraries,
- **When** queried,
- **Then** they never collide; the same person watched across libraries
  is two separate `speakers` rows. No cross-library merge in v1.

**Test cases:**
- Integration: insert 100 segments from 3 voices → 3 speaker rows; merge
  two → 2 rows; rename → 2 rows with names.
- Integration: a voice present in 50 segments named via PATCH → all 50
  segments now display the new name in the next API read.

**Edge cases:**
- Diarization disabled mid-library — existing speakers and `segment_speakers`
  are preserved; new segments simply have no speaker. No data loss.
- Voiceprint storage size — a `d-vector` of 512 floats = 2 KiB per
  speaker; even 10k speakers per library is 20 MiB. Stored as `BYTEA`.
- Two unknown speakers later turn out to be the same — the merge handles
  it; the count of unknowns can decrease (next new unknown takes the
  lowest free index).
