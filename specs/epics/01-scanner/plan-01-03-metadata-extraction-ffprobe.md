# Plan 01-03 — Metadata Extraction via FFprobe

> **Note on scope.** This plan covers FFprobe-based metadata extraction. The
> canonical user-facing spec is
> [story-02-01-audio-probe.md](../02-audio-extraction/story-02-01-audio-probe.md)
> (acceptance criteria, edge cases, test cases). The architecture
> ([§3.2 Probe](../../architecture.md)) places the probe **stage** inside the
> Python Pipeline Service, but the FFprobe binding itself is implemented in
> Go as a shared package — `internal/ffmpeg/probe` — used by:
>
> - the Streaming Service for the live probe-cache (architecture §2.1, "Probe cache"),
> - the API Service when it needs synchronous metadata (e.g. on manual scan),
> - and called from Python via gRPC `MediaService.Probe` for the bulk pipeline
>   probe stage.
>
> One canonical implementation; three callers. This plan describes that Go
> package and the schema it writes.

---

## 1. Architecture diagram — FFprobe integration flow

```
                ┌─────────────────────────────────────────────────────┐
                │  Caller (Streaming Service / API Service / gRPC)    │
                │   probe.Probe(ctx, path) → VideoMetadata, error     │
                └────────────────────────┬────────────────────────────┘
                                         │
                                         ▼
              ┌──────────────────────────────────────────────────┐
              │  internal/ffmpeg/probe.Prober                    │
              │   - resolves ffprobe binary (PATH or config)     │
              │   - applies timeout (default 30 s)               │
              │   - exec.CommandContext + JSON stdout pipe       │
              └────────────────────────┬─────────────────────────┘
                                       │
                  exec ffprobe -v quiet -print_format json
                  -show_streams -show_format -show_chapters
                  -analyzeduration 100M -probesize 50M  <path>
                                       │
                                       ▼
               ┌──────────────────────────────────────────┐
               │  ffprobe subprocess                       │
               │   reads ≤ ~50 MB of <path>                │
               │   writes JSON (streams[] + format{} + …) │
               └────────────────────────┬─────────────────┘
                                        │ stdout
                                        ▼
              ┌──────────────────────────────────────────────────┐
              │  json.Unmarshal → ffprobeOutput (raw)            │
              │  Normalizer → VideoMetadata (typed):              │
              │    container, duration, bitrate, video stream,    │
              │    audio tracks[], subtitle tracks[],             │
              │    chapters[], raw JSON (pass-through)            │
              └────────────────────────┬─────────────────────────┘
                                       │
                                       ▼
              ┌──────────────────────────────────────────────────┐
              │  Persistence (sqlc-generated queries):           │
              │    UPSERT videos.duration_sec                     │
              │    UPSERT media_info (one row per video_id)       │
              │    UPSERT audio_tracks (UNIQUE video_id, index)   │
              │    UPSERT subtitle_streams (new in this plan)     │
              │    UPDATE videos.state = 'probed' (or            │
              │           'ready_no_audio' if no audio streams)   │
              │    INSERT processing_jobs(stage='extract', …)     │
              │  All inside one tx; idempotent on replay.         │
              └────────────────────────┬─────────────────────────┘
                                       │
                                       ▼
              ┌──────────────────────────────────────────────────┐
              │  Postgres LISTEN/NOTIFY 'video_state_changed'    │
              │  → API broadcasts WS, downstream stages claim job │
              └──────────────────────────────────────────────────┘
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
internal/ffmpeg/
├── probe/
│   ├── prober.go         # Prober interface + Cmd Prober (real ffprobe)
│   ├── parse.go          # ffprobeOutput → VideoMetadata normalizer
│   ├── types.go          # VideoMetadata, AudioTrack, SubtitleStream, Chapter
│   ├── errors.go         # ErrFFprobeNotFound, ErrTimeout, ErrCorrupt, ErrUnsupported
│   ├── prober_test.go    # table-driven unit tests over JSON fixtures
│   └── testdata/
│       ├── lecture_1080p_h264_aac_ar.json
│       ├── multiaudio_3tracks.json
│       ├── silent_no_audio.json
│       ├── embedded_subs_srt.json
│       ├── corrupt_truncated.json
│       └── undefined_lang.json
└── store/
    ├── persist.go        # PersistProbe(tx, videoID, meta) — calls sqlc queries
    └── persist_test.go   # against sqlite test DB
```

### 2.2 The exact FFprobe command

```
ffprobe \
  -v quiet \
  -print_format json \
  -show_format \
  -show_streams \
  -show_chapters \
  -analyzeduration 100M \
  -probesize 50M \
  --                     # end-of-options sentinel; the path follows
  /absolute/path/to/file.mkv
```

Flag rationale:

| Flag | Purpose |
|---|---|
| `-v quiet` | Suppress the banner and progress lines on stderr; we only want the JSON on stdout. We still capture stderr and log it on non-zero exit. |
| `-print_format json` | Machine-readable, stable schema. |
| `-show_format` | Container, duration, bitrate, format-level tags. |
| `-show_streams` | Per-stream codec, resolution, fps, language, disposition, channel layout. |
| `-show_chapters` | Embedded chapter markers (Epic 8 reuses this). |
| `-analyzeduration 100M -probesize 50M` | Required for fragmented/MPEG-TS sources to report full duration; harmless on well-formed files. Architecture §3.2 mandates these. |
| `--` | Defensive: prevents a maliciously named file (e.g. starting with `-`) from being parsed as a flag. |

**Working directory:** the binary's CWD. We pass an absolute path so CWD is irrelevant.

**Network filesystems:** the same flags work on NFS/SMB; we already require absolute paths so the open is unambiguous.

**Hardening:** the call goes through `exec.CommandContext`, never `sh -c`; the path argument is positional and never interpolated into a shell string. This eliminates command-injection by construction.

### 2.3 Parsed fields (full list)

| Field | Source in ffprobe JSON | Maktaba column / model field |
|---|---|---|
| Container | `format.format_name` (we keep the canonical first token, e.g. `matroska` from `matroska,webm`) | `media_info.container` |
| Duration (seconds) | `format.duration` (string → float) | `videos.duration_sec`, `media_info` (derived) |
| Overall bitrate (kbps) | `format.bit_rate` / 1000 | `media_info.bitrate_kbps` |
| Video codec | first stream where `codec_type=video` and `disposition.attached_pic != 1` → `codec_name` | `media_info.video_codec` |
| Width / height | same stream → `width`, `height` | `media_info.width`, `media_info.height` |
| Frame rate | same stream → `avg_frame_rate` parsed as `num/den`; fallback `r_frame_rate` | `media_info.fps` |
| Audio tracks (one row each) | every stream where `codec_type=audio` | `audio_tracks` rows |
| &nbsp;&nbsp;index | `index` from ffprobe (matches `ffmpeg -map 0:N`) | `audio_tracks.index` |
| &nbsp;&nbsp;codec | `codec_name` | `audio_tracks.codec` |
| &nbsp;&nbsp;channels | `channels` | `audio_tracks.channels` |
| &nbsp;&nbsp;sample rate | `sample_rate` (string → int) | `audio_tracks.sample_rate` |
| &nbsp;&nbsp;language | `tags.language` (ISO 639-3); else `und` (never NULL) | `audio_tracks.language` |
| &nbsp;&nbsp;title | `tags.title` | `audio_tracks.title` |
| &nbsp;&nbsp;is_default | `disposition.default == 1` | `audio_tracks.is_default` |
| Embedded subtitle streams | every stream where `codec_type=subtitle` | `subtitle_streams` rows (new) |
| &nbsp;&nbsp;index, codec, language, title, is_default, is_forced, is_hearing_impaired | matching stream fields and disposition flags | columns of `subtitle_streams` |
| `has_subtitles` | derived: `len(subtitle_streams) > 0` | `media_info.has_subtitles` |
| Chapters | `chapters[]` → seq, start_sec, end_sec, title | `chapters` table (Epic 8) |
| Raw JSON | full ffprobe stdout, kept verbatim | `media_info.raw_ffprobe` (jsonb) |

---

## 3. Go code scaffolding

### 3.1 `types.go`

```go
package probe

import (
	"encoding/json"
	"time"
)

// VideoMetadata is the normalized, caller-facing result of a probe.
// All fields are populated even when ffprobe omits them; missing values
// are encoded as zero values (channels=0, language="und", etc.) rather
// than nil so downstream callers don't need a triple-state.
type VideoMetadata struct {
	Container    string          `json:"container"`
	DurationSec  float64         `json:"duration_sec"`
	BitrateKbps  int             `json:"bitrate_kbps"`
	Video        VideoStream     `json:"video"`
	Audio        []AudioTrack    `json:"audio"`
	Subtitles    []SubtitleStream `json:"subtitles"`
	Chapters     []Chapter       `json:"chapters"`
	HasSubtitles bool            `json:"has_subtitles"`
	ProbedAt     time.Time       `json:"probed_at"`
	Raw          json.RawMessage `json:"-"` // persisted to media_info.raw_ffprobe
}

type VideoStream struct {
	Index    int     `json:"index"`
	Codec    string  `json:"codec"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Bitrate  int     `json:"bitrate_kbps"` // per-stream, 0 if unknown
}

type AudioTrack struct {
	Index      int    `json:"index"`
	Codec      string `json:"codec"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sample_rate"`
	Language   string `json:"language"` // ISO 639-3, "und" if absent
	Title      string `json:"title"`
	IsDefault  bool   `json:"is_default"`
}

type SubtitleStream struct {
	Index             int    `json:"index"`
	Codec             string `json:"codec"`
	Language          string `json:"language"`
	Title             string `json:"title"`
	IsDefault         bool   `json:"is_default"`
	IsForced          bool   `json:"is_forced"`
	IsHearingImpaired bool   `json:"is_hearing_impaired"`
}

type Chapter struct {
	Seq      int     `json:"seq"`
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
	Title    string  `json:"title"`
}
```

### 3.2 `prober.go`

```go
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Prober is the probe interface; tests inject a fake.
type Prober interface {
	Probe(ctx context.Context, path string) (VideoMetadata, error)
}

// CmdProber shells out to a real ffprobe binary.
type CmdProber struct {
	Binary  string        // path to ffprobe; "ffprobe" by default (resolved via PATH)
	Timeout time.Duration // per-call timeout; 0 → 30s default
	Logger  *slog.Logger
}

func NewCmdProber(binary string, timeout time.Duration, logger *slog.Logger) *CmdProber {
	if binary == "" {
		binary = "ffprobe"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CmdProber{Binary: binary, Timeout: timeout, Logger: logger}
}

func (p *CmdProber) Probe(ctx context.Context, path string) (VideoMetadata, error) {
	if _, err := exec.LookPath(p.Binary); err != nil {
		return VideoMetadata{}, fmt.Errorf("%w: %s", ErrFFprobeNotFound, p.Binary)
	}

	cctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_chapters",
		"-analyzeduration", "100M",
		"-probesize", "50M",
		"--", path,
	}
	cmd := exec.CommandContext(cctx, p.Binary, args...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if cctx.Err() == context.DeadlineExceeded {
		return VideoMetadata{}, fmt.Errorf("%w after %s", ErrTimeout, p.Timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Non-zero exit usually means the file is corrupt or unsupported.
			p.Logger.Warn("ffprobe exited non-zero",
				"path", path, "code", ee.ExitCode(),
				"stderr", stderr.String(), "elapsed", elapsed)
			return VideoMetadata{}, fmt.Errorf("%w: exit %d: %s",
				ErrCorrupt, ee.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return VideoMetadata{}, fmt.Errorf("ffprobe exec: %w", err)
	}

	raw := json.RawMessage(stdout.String())
	meta, err := parseOutput(raw)
	if err != nil {
		return VideoMetadata{}, fmt.Errorf("parse ffprobe json: %w", err)
	}
	meta.Raw = raw
	meta.ProbedAt = time.Now().UTC()
	return meta, nil
}
```

### 3.3 `parse.go`

```go
package probe

import (
	"encoding/json"
	"strconv"
	"strings"
)

type rawOutput struct {
	Format   rawFormat   `json:"format"`
	Streams  []rawStream `json:"streams"`
	Chapters []rawChapter `json:"chapters"`
}

type rawFormat struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	BitRate    string            `json:"bit_rate"`
	Tags       map[string]string `json:"tags"`
}

type rawStream struct {
	Index         int               `json:"index"`
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	AvgFrameRate  string            `json:"avg_frame_rate"`
	RFrameRate    string            `json:"r_frame_rate"`
	Channels      int               `json:"channels"`
	SampleRate    string            `json:"sample_rate"`
	BitRate       string            `json:"bit_rate"`
	Tags          map[string]string `json:"tags"`
	Disposition   map[string]int    `json:"disposition"`
}

type rawChapter struct {
	ID        int64             `json:"id"`
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

func parseOutput(raw json.RawMessage) (VideoMetadata, error) {
	var o rawOutput
	if err := json.Unmarshal(raw, &o); err != nil {
		return VideoMetadata{}, err
	}

	meta := VideoMetadata{
		Container:   firstToken(o.Format.FormatName),
		DurationSec: parseFloat(o.Format.Duration),
		BitrateKbps: parseInt(o.Format.BitRate) / 1000,
	}

	for _, s := range o.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue // album art / cover image, not a real video stream
			}
			if meta.Video.Codec == "" { // first non-cover video stream wins
				meta.Video = VideoStream{
					Index:   s.Index,
					Codec:   s.CodecName,
					Width:   s.Width,
					Height:  s.Height,
					FPS:     parseFraction(s.AvgFrameRate, s.RFrameRate),
					Bitrate: parseInt(s.BitRate) / 1000,
				}
			}
		case "audio":
			meta.Audio = append(meta.Audio, AudioTrack{
				Index:      s.Index,
				Codec:      s.CodecName,
				Channels:   s.Channels,
				SampleRate: parseInt(s.SampleRate),
				Language:   languageOrUnd(s.Tags),
				Title:      s.Tags["title"],
				IsDefault:  s.Disposition["default"] == 1,
			})
		case "subtitle":
			meta.Subtitles = append(meta.Subtitles, SubtitleStream{
				Index:             s.Index,
				Codec:             s.CodecName,
				Language:          languageOrUnd(s.Tags),
				Title:             s.Tags["title"],
				IsDefault:         s.Disposition["default"] == 1,
				IsForced:          s.Disposition["forced"] == 1,
				IsHearingImpaired: s.Disposition["hearing_impaired"] == 1,
			})
		}
	}
	meta.HasSubtitles = len(meta.Subtitles) > 0

	for i, c := range o.Chapters {
		meta.Chapters = append(meta.Chapters, Chapter{
			Seq:      i,
			StartSec: parseFloat(c.StartTime),
			EndSec:   parseFloat(c.EndTime),
			Title:    c.Tags["title"],
		})
	}
	return meta, nil
}

func firstToken(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return s[:i]
	}
	return s
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// parseFraction handles ffprobe's "30000/1001" form. Falls back to alt if num/den invalid.
func parseFraction(primary, fallback string) float64 {
	if v, ok := tryFraction(primary); ok {
		return v
	}
	if v, ok := tryFraction(fallback); ok {
		return v
	}
	return 0
}

func tryFraction(s string) (float64, bool) {
	if s == "" || s == "0/0" {
		return 0, false
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, false
	}
	num, err1 := strconv.ParseFloat(parts[0], 64)
	den, err2 := strconv.ParseFloat(parts[1], 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0, false
	}
	return num / den, true
}

func languageOrUnd(tags map[string]string) string {
	if l, ok := tags["language"]; ok && l != "" {
		return l
	}
	return "und"
}
```

### 3.4 `errors.go`

```go
package probe

import "errors"

var (
	ErrFFprobeNotFound = errors.New("ffprobe binary not found")
	ErrTimeout         = errors.New("ffprobe timed out")
	ErrCorrupt         = errors.New("ffprobe rejected the file (corrupt or unsupported)")
	ErrUnsupported    = errors.New("ffprobe parsed the file but it has no recognizable streams")
)
```

### 3.5 Persistence (`store/persist.go` sketch)

```go
// PersistProbe writes the probe result and advances the FSM in one tx.
// Idempotent: replaying a probe leaves the same end state.
func PersistProbe(ctx context.Context, q *db.Queries, videoID uuid.UUID, m probe.VideoMetadata) error {
	if err := q.UpsertMediaInfo(ctx, db.UpsertMediaInfoParams{
		VideoID:      videoID,
		Container:    sql.NullString{String: m.Container, Valid: m.Container != ""},
		VideoCodec:   sql.NullString{String: m.Video.Codec, Valid: m.Video.Codec != ""},
		Width:        sql.NullInt32{Int32: int32(m.Video.Width), Valid: m.Video.Width > 0},
		Height:       sql.NullInt32{Int32: int32(m.Video.Height), Valid: m.Video.Height > 0},
		Fps:          sql.NullFloat64{Float64: m.Video.FPS, Valid: m.Video.FPS > 0},
		BitrateKbps:  sql.NullInt32{Int32: int32(m.BitrateKbps), Valid: m.BitrateKbps > 0},
		HasSubtitles: m.HasSubtitles,
		RawFfprobe:   m.Raw,
	}); err != nil {
		return err
	}

	if err := q.UpdateVideoDuration(ctx, db.UpdateVideoDurationParams{
		ID:          videoID,
		DurationSec: sql.NullFloat64{Float64: m.DurationSec, Valid: m.DurationSec > 0},
	}); err != nil {
		return err
	}

	for _, a := range m.Audio {
		if err := q.UpsertAudioTrack(ctx, db.UpsertAudioTrackParams{
			VideoID:    videoID,
			Index:      int32(a.Index),
			Codec:      sql.NullString{String: a.Codec, Valid: true},
			Channels:   sql.NullInt32{Int32: int32(a.Channels), Valid: a.Channels > 0},
			SampleRate: sql.NullInt32{Int32: int32(a.SampleRate), Valid: a.SampleRate > 0},
			Language:   a.Language,
			Title:      sql.NullString{String: a.Title, Valid: a.Title != ""},
			IsDefault:  a.IsDefault,
		}); err != nil {
			return err
		}
	}

	for _, s := range m.Subtitles {
		if err := q.UpsertSubtitleStream(ctx, db.UpsertSubtitleStreamParams{
			VideoID:           videoID,
			Index:             int32(s.Index),
			Codec:             sql.NullString{String: s.Codec, Valid: true},
			Language:          s.Language,
			Title:             sql.NullString{String: s.Title, Valid: s.Title != ""},
			IsDefault:         s.IsDefault,
			IsForced:          s.IsForced,
			IsHearingImpaired: s.IsHearingImpaired,
		}); err != nil {
			return err
		}
	}

	nextState := "probed"
	if len(m.Audio) == 0 {
		nextState = "ready_no_audio"
	}
	if err := q.AdvanceVideoState(ctx, db.AdvanceVideoStateParams{
		ID: videoID, From: "discovered", To: nextState,
	}); err != nil {
		return err
	}
	if nextState == "probed" {
		if err := q.EnqueueExtractJob(ctx, videoID); err != nil {
			return err
		}
	}
	return nil
}
```

---

## 4. Database migrations

The `videos`, `media_info`, and `audio_tracks` tables already exist
(architecture §8.1). This story:

- **Adds** the `subtitle_streams` table — embedded subtitle streams need
  per-stream rows so Epic 4 can pick them later without re-probing.
- **Adds** indexes that the probe stage's idempotent upserts rely on.
- **Adds** the `READY_NO_AUDIO` state to the existing FSM check (Story 1.6
  owns the canonical FSM, but we make sure the value is allowed).

```sql
-- migrations/000NN_subtitle_streams.up.sql
BEGIN;

CREATE TABLE subtitle_streams (
    id                   BIGSERIAL PRIMARY KEY,
    video_id             UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    index                INT  NOT NULL,                  -- ffmpeg stream index
    codec                TEXT,                            -- subrip, ass, mov_text, hdmv_pgs_subtitle, …
    language             TEXT NOT NULL DEFAULT 'und',
    title                TEXT,
    is_default           BOOLEAN NOT NULL DEFAULT false,
    is_forced            BOOLEAN NOT NULL DEFAULT false,
    is_hearing_impaired  BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (video_id, index)
);
CREATE INDEX ON subtitle_streams (video_id);
CREATE INDEX ON subtitle_streams (language);

-- audio_tracks already has UNIQUE (video_id, index); we add a partial index
-- on the default track so 'pick the default audio' is O(1).
CREATE INDEX IF NOT EXISTS audio_tracks_default_idx
    ON audio_tracks (video_id) WHERE is_default;

-- videos.state allowed values (Story 1.6 owns the canonical list; this is a
-- defensive check that can be widened by later migrations).
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_check;
ALTER TABLE videos
    ADD  CONSTRAINT videos_state_check
    CHECK (state IN (
        'discovered', 'probed', 'audio_extracted', 'transcribed',
        'indexed', 'thumbnailed', 'ready', 'ready_no_audio',
        'failed', 'missing'
    ));

COMMIT;
```

```sql
-- migrations/000NN_subtitle_streams.down.sql
BEGIN;
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_state_check;
DROP INDEX IF EXISTS audio_tracks_default_idx;
DROP TABLE IF EXISTS subtitle_streams;
COMMIT;
```

### sqlc queries (selected)

```sql
-- name: UpsertMediaInfo :exec
INSERT INTO media_info (
    video_id, container, video_codec, width, height, fps,
    bitrate_kbps, has_subtitles, raw_ffprobe, probed_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
ON CONFLICT (video_id) DO UPDATE SET
    container = EXCLUDED.container,
    video_codec = EXCLUDED.video_codec,
    width = EXCLUDED.width,
    height = EXCLUDED.height,
    fps = EXCLUDED.fps,
    bitrate_kbps = EXCLUDED.bitrate_kbps,
    has_subtitles = EXCLUDED.has_subtitles,
    raw_ffprobe = EXCLUDED.raw_ffprobe,
    probed_at = now();

-- name: UpsertAudioTrack :exec
INSERT INTO audio_tracks (video_id, index, codec, channels, sample_rate,
                          language, title, is_default)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (video_id, index) DO NOTHING;
-- Probe is idempotent; the file's audio layout doesn't change between probes.
-- If it does (rare; mid-file codec change), we deliberately keep the first
-- result rather than racing.

-- name: UpsertSubtitleStream :exec
INSERT INTO subtitle_streams (video_id, index, codec, language, title,
                              is_default, is_forced, is_hearing_impaired)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (video_id, index) DO NOTHING;

-- name: UpdateVideoDuration :exec
UPDATE videos SET duration_sec = $2, updated_at = now() WHERE id = $1;

-- name: AdvanceVideoState :exec
UPDATE videos SET state = $3, updated_at = now()
WHERE id = $1 AND state = $2;
-- Conditional update so a replay (state already moved past) is a silent no-op.

-- name: EnqueueExtractJob :exec
INSERT INTO processing_jobs (video_id, stage, state)
VALUES ($1, 'extract', 'pending')
ON CONFLICT (video_id, stage) WHERE state IN ('pending','running') DO NOTHING;
```

---

## 5. Test plan

| # | Name | What it checks |
|---|---|---|
| T1 | `parse_lecture_h264_aac_arabic` | Golden: `media_info` row matches expected; `audio_tracks` has 1 row with `language='ara'`, `is_default=true`. |
| T2 | `parse_multiaudio_three_tracks` | 3 `audio_tracks` rows; `is_default` set on the disposition-default stream only. |
| T3 | `parse_subtitle_streams` | 2 `subtitle_streams` rows; `media_info.has_subtitles=true`. |
| T4 | `parse_no_audio` | `audio_tracks` empty; downstream caller advances state to `READY_NO_AUDIO`; no extract job. |
| T5 | `parse_undefined_language` | Audio row has `language='und'`, never NULL. |
| T6 | `parse_attached_picture_skipped` | A `video` stream with `disposition.attached_pic=1` is **not** treated as the main video; codec falls through to the next video stream. |
| T7 | `parse_avg_frame_rate_zero_falls_back` | `avg_frame_rate="0/0"` falls back to `r_frame_rate`. |
| T8 | `parse_chapters` | Chapters parsed from `chapters[]` with stable seq order. |
| T9 | `idempotent_replay` | Run probe twice → same row counts; `audio_tracks.id` unchanged for existing rows. |
| T10 | `error_ffprobe_not_found` | Binary path missing → `ErrFFprobeNotFound`; nothing written to DB. |
| T11 | `error_corrupt_file` | ffprobe exits 1 on truncated MKV → `ErrCorrupt`; stderr captured in log. |
| T12 | `error_timeout` | Slow stub binary > timeout → `ErrTimeout`; child process killed (not orphaned). |
| T13 | `error_unsupported_format` | ffprobe succeeds but reports zero streams → `ErrUnsupported`; no DB writes. |
| T14 | `command_injection_safe` | Path containing `; rm -rf /` is passed positional; the `--` sentinel and exec mode prevent shell interpretation. |
| T15 | `network_path_with_analyzeduration` | Fixture mimicking a fragmented MPEG-TS stream — without `-analyzeduration 100M` duration is 0; with the flag it's correct. |

Every test is an isolated unit test against JSON fixtures. The CmdProber
itself is exercised in T10–T12, T14 by injecting a fake-`ffprobe` shell
script (test-only) into `PATH`; everything else parses canned JSON
directly.

---

## 6. Test code scaffolding

### 6.1 Table-driven parser tests

```go
package probe_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maktaba/maktaba/internal/ffmpeg/probe"
)

type expectedAudio struct {
	Index     int
	Codec     string
	Language  string
	IsDefault bool
}

type expected struct {
	Container    string
	DurationSec  float64
	VideoCodec   string
	Width        int
	Height       int
	FPS          float64
	HasSubtitles bool
	Audio        []expectedAudio
	SubtitleN    int
}

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		file string
		want expected
	}{
		{
			name: "lecture_h264_aac_arabic",
			file: "lecture_1080p_h264_aac_ar.json",
			want: expected{
				Container:    "matroska",
				DurationSec:  3612.45,
				VideoCodec:   "h264",
				Width:        1920,
				Height:       1080,
				FPS:          25.0,
				HasSubtitles: false,
				Audio: []expectedAudio{
					{Index: 1, Codec: "aac", Language: "ara", IsDefault: true},
				},
			},
		},
		{
			name: "multiaudio_three_tracks",
			file: "multiaudio_3tracks.json",
			want: expected{
				Container: "matroska", VideoCodec: "h264",
				Audio: []expectedAudio{
					{Index: 1, Codec: "aac", Language: "ara", IsDefault: true},
					{Index: 2, Codec: "aac", Language: "eng", IsDefault: false},
					{Index: 3, Codec: "aac", Language: "fra", IsDefault: false},
				},
			},
		},
		{
			name: "embedded_subtitles",
			file: "embedded_subs_srt.json",
			want: expected{HasSubtitles: true, SubtitleN: 2},
		},
		{
			name: "silent_no_audio",
			file: "silent_no_audio.json",
			want: expected{Container: "mp4", VideoCodec: "h264", Audio: nil},
		},
		{
			name: "undefined_language_becomes_und",
			file: "undefined_lang.json",
			want: expected{
				Audio: []expectedAudio{{Index: 1, Codec: "aac", Language: "und"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustReadFixture(t, tc.file)
			got, err := probe.ParseForTest(raw) // test-only export of parseOutput
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.Container != tc.want.Container && tc.want.Container != "" {
				t.Errorf("container = %q, want %q", got.Container, tc.want.Container)
			}
			if tc.want.VideoCodec != "" && got.Video.Codec != tc.want.VideoCodec {
				t.Errorf("video codec = %q, want %q", got.Video.Codec, tc.want.VideoCodec)
			}
			if got.HasSubtitles != tc.want.HasSubtitles {
				t.Errorf("has_subtitles = %v, want %v", got.HasSubtitles, tc.want.HasSubtitles)
			}
			if tc.want.SubtitleN > 0 && len(got.Subtitles) != tc.want.SubtitleN {
				t.Errorf("subtitle count = %d, want %d", len(got.Subtitles), tc.want.SubtitleN)
			}
			if len(tc.want.Audio) != len(got.Audio) {
				t.Fatalf("audio count = %d, want %d", len(got.Audio), len(tc.want.Audio))
			}
			for i, w := range tc.want.Audio {
				a := got.Audio[i]
				if a.Index != w.Index || a.Codec != w.Codec ||
					a.Language != w.Language || a.IsDefault != w.IsDefault {
					t.Errorf("audio[%d] = %+v, want %+v", i, a, w)
				}
			}
		})
	}
}

func mustReadFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}
```

### 6.2 Error-path tests (CmdProber)

```go
func TestCmdProber_Errors(t *testing.T) {
	t.Run("binary_missing", func(t *testing.T) {
		p := probe.NewCmdProber("/nonexistent/ffprobe", time.Second, nil)
		_, err := p.Probe(context.Background(), "any.mkv")
		if !errors.Is(err, probe.ErrFFprobeNotFound) {
			t.Fatalf("err = %v, want ErrFFprobeNotFound", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		dir := t.TempDir()
		stub := filepath.Join(dir, "ffprobe")
		// shell stub that sleeps longer than the timeout
		os.WriteFile(stub, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
		p := probe.NewCmdProber(stub, 200*time.Millisecond, nil)
		start := time.Now()
		_, err := p.Probe(context.Background(), "any.mkv")
		if !errors.Is(err, probe.ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("did not abort promptly")
		}
	})

	t.Run("corrupt_file", func(t *testing.T) {
		dir := t.TempDir()
		stub := filepath.Join(dir, "ffprobe")
		os.WriteFile(stub, []byte("#!/bin/sh\necho boom 1>&2\nexit 1\n"), 0o755)
		p := probe.NewCmdProber(stub, time.Second, nil)
		_, err := p.Probe(context.Background(), "any.mkv")
		if !errors.Is(err, probe.ErrCorrupt) {
			t.Fatalf("err = %v, want ErrCorrupt", err)
		}
	})

	t.Run("path_with_shell_metacharacters_is_safe", func(t *testing.T) {
		dir := t.TempDir()
		stub := filepath.Join(dir, "ffprobe")
		// Echo the last argv to stdout as JSON-ish so we can verify it.
		os.WriteFile(stub, []byte(
			"#!/bin/sh\nprintf '{\"format\":{\"format_name\":\"received:%s\"}}' \"$1\" "+
				"|| true\nexit 0\n"), 0o755)
		p := probe.NewCmdProber(stub, time.Second, nil)
		// Won't actually be parsed correctly, but the test asserts no shell expansion.
		const evil = "/tmp/file; rm -rf /.mkv"
		m, _ := p.Probe(context.Background(), evil)
		if !strings.Contains(m.Container, evil) {
			t.Fatalf("argv was modified: container=%q", m.Container)
		}
	})
}
```

### 6.3 Persistence test (idempotency)

```go
func TestPersistProbe_Idempotent(t *testing.T) {
	q, cleanup := newTestDB(t) // sqlite test DB seeded with one DISCOVERED video
	defer cleanup()
	id := seedVideo(t, q, "discovered")

	meta := goldenMetadata(t, "lecture_1080p_h264_aac_ar.json")

	if err := store.PersistProbe(ctx, q, id, meta); err != nil {
		t.Fatalf("first persist: %v", err)
	}
	if err := store.PersistProbe(ctx, q, id, meta); err != nil {
		t.Fatalf("replay persist: %v", err)
	}

	got := q.GetVideoForTest(t, id)
	if got.State != "probed" {
		t.Fatalf("state = %s, want probed", got.State)
	}
	tracks := q.ListAudioTracksForTest(t, id)
	if len(tracks) != len(meta.Audio) {
		t.Fatalf("audio_tracks rows = %d, want %d", len(tracks), len(meta.Audio))
	}
	jobs := q.ListPendingJobsForTest(t, id, "extract")
	if len(jobs) != 1 {
		t.Fatalf("extract jobs = %d, want exactly 1 even after replay", len(jobs))
	}
}
```

---

## 7. Error handling

| Failure | Detection | Response |
|---|---|---|
| **ffprobe not on PATH / wrong binary** | `exec.LookPath` returns error before exec. | Return `ErrFFprobeNotFound`. The probe stage marks the job `failed` with a clear message and does **not** retry — operator must fix the binary. Surfaces in health check `/healthz` (probe sub-check). |
| **Timeout (file too big to analyze, NFS stall)** | `context.DeadlineExceeded` after 30 s default. | Return `ErrTimeout`. Job is retried with exponential backoff (Epic 6 owns retry policy); after `max_retries=3` it's `failed`. Child process killed via `CommandContext`. |
| **Corrupt file (truncated download, bad MOV atom)** | Non-zero exit code; stderr captured. | Return `ErrCorrupt`. Job advances to `failed`; the video state moves to `failed` with `last_error=stderr`. The user can re-trigger via Story 1.4 manual control. |
| **Unsupported format** | ffprobe exits 0 but `len(streams)==0` or no video stream. | Return `ErrUnsupported`. State → `failed`. |
| **JSON parse error** (ffprobe version mismatch) | `json.Unmarshal` error. | Wrapped error logs the raw stdout (truncated to 1 KiB) for forensics; state → `failed`. Tests guard the schema for the supported ffprobe version. |
| **Mid-file codec change** | Cannot detect at probe time (architecture §3.2 acknowledges this). | We record the first-packet codec. The downstream extractor is the one that fails and retries with `transcoded_extract=true` (Story 2.3). |
| **Probe replayed after extract already ran** | `AdvanceVideoState` is conditional on `from='discovered'`. | The state-update is a silent no-op; audio_tracks upserts use `ON CONFLICT DO NOTHING`. End result: zero new rows, no duplicate jobs. |
| **Path traversal / shell injection** | Eliminated by construction: `exec.CommandContext` with positional args, `--` sentinel. | Test T14 guards the invariant. |
| **ffprobe stderr contains a panic** | We always capture stderr; on non-zero exit it's wrapped into the error. | Logger emits `ffprobe.exit` at WARN with `path`, `code`, `stderr` (truncated). |

The `Prober` interface is what callers depend on — tests substitute a fake
that returns canned `VideoMetadata` or canned errors, so error-path tests
for downstream code don't need real ffprobe.

---

## 8. Acceptance checklist

Sourced from
[story-02-01-audio-probe.md](../02-audio-extraction/story-02-01-audio-probe.md),
plus the implementation invariants this plan adds.

**Behavioral**

- [ ] Given a `DISCOVERED` video, a probe populates `media_info` (container, video codec, resolution, fps, bitrate, has_subtitles).
- [ ] One `audio_tracks` row per audio stream with `index`, `codec`, `channels`, `sample_rate`, `language`, `title`, `is_default`.
- [ ] `language` is ISO 639-3 from `tags.language`, falling back to `'und'` (never NULL).
- [ ] `is_default` is set when `disposition.default == 1`.
- [ ] On completion, state advances `discovered → probed` exactly once.
- [ ] A `processing_jobs(stage='extract', state='pending')` row is enqueued exactly once.
- [ ] An audioless video moves `discovered → ready_no_audio` and **does not** enqueue extract.
- [ ] Replaying probe is idempotent: same row counts, same job counts, same state.
- [ ] Embedded subtitle streams populate `subtitle_streams`; `media_info.has_subtitles` reflects the count.
- [ ] Embedded chapters populate `chapters` (Epic 8 consumer).

**Implementation invariants**

- [ ] FFprobe is invoked with the exact flag list in §2.2; no shell.
- [ ] All persistence happens in a single transaction (probe + audio tracks + subtitles + state advance + job enqueue).
- [ ] `raw_ffprobe` JSONB is stored verbatim for forensic re-parsing.
- [ ] The `Prober` interface is the only ffprobe touch point; downstream code never shells out independently.
- [ ] Default timeout 30 s, configurable per call via `context.WithTimeout`.
- [ ] `attached_pic` video streams are skipped, not treated as the main video.
- [ ] `avg_frame_rate=0/0` falls back to `r_frame_rate`.

**Operational**

- [ ] `/healthz` includes a `probe.binary_present` sub-check.
- [ ] Structured log entries include `path`, `duration_ms`, `streams.audio`, `streams.video`, `streams.subtitle`, and `error.kind` on failure.
- [ ] An OpenTelemetry span `ffmpeg.probe` wraps each call; attributes mirror the log fields.
- [ ] Migration `subtitle_streams` is reversible (`down.sql` drops cleanly).
- [ ] All 15 tests in §5 pass on CI (Linux + macOS) against the pinned ffprobe version (`6.x` or `7.x`).
- [ ] Property-equivalent Python wrapper exists in `pipeline/.../probe.py` and shells out to the same Go binary via gRPC `MediaService.Probe` — Pipeline does not maintain a parallel parser.
