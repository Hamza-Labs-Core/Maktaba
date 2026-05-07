---
name: Plan 02-01 — Audio probe stage
description: Implementation plan for Epic 2 Story 1 (audio probe). Covers the gRPC MediaService.Probe wrapper in Go and the Python pipeline stage that claims probe jobs, calls gRPC, persists media_info / audio_tracks / subtitle_streams, and drives the FSM (DISCOVERED → PROBED | READY_NO_AUDIO).
type: plan
---

# Plan 02-01 — Audio Probe Stage

> **Canonical story:** [story-02-01-audio-probe.md](story-02-01-audio-probe.md)
> (acceptance criteria + edge cases + test cases).
>
> **Relationship to Plan 01-03.**
> [plan-01-03-metadata-extraction-ffprobe.md](../01-scanner/plan-01-03-metadata-extraction-ffprobe.md)
> already specified the Go FFprobe binding (`internal/ffmpeg/probe`)
> together with its parser, error taxonomy, persistence helpers, and 15
> unit tests. **This plan does not re-do that work.** It builds on top of
> it: the new pieces are
>
> 1. a thin gRPC service (`MediaService.Probe`) that exposes the Go
>    prober to the Python Pipeline Service, and
> 2. the Python `probe` pipeline stage — the worker-side orchestration
>    that claims a `probe` job, calls the gRPC, persists results in one
>    Postgres tx, advances the FSM, and enqueues the `extract` job.
>
> Plan 01-03 owns the *binding*; this plan owns the *stage*.

> **Scope-clarification re: epic title.** Epic 02 is "Audio Extraction".
> Story 2.1 is **probe only** — it inspects audio tracks; it does not
> extract them. The actual `ffmpeg -map 0:a:N -ac 1 -ar 16000 -f wav -`
> extraction pipeline, temp-file lifecycle, and stream-vs-disk policy
> live in [story-02-03-stream-extraction.md](story-02-03-stream-extraction.md)
> and will be planned separately. Probe writes nothing to disk and uses
> no temp files.

---

## 1. End-to-end flow

```
                           ┌──────────────────────────┐
            (1) LISTEN     │ Postgres                 │
            video_state_   │  videos.state=DISCOVERED │
            changed        │  processing_jobs(probe)  │
                  ┌────────┤  state=pending           │
                  │        └──────────────────────────┘
                  ▼
   ┌──────────────────────────────────┐  (2) CLAIM job
   │ Python pipeline runner           │──────────────────┐
   │ (pipeline/runner.py)             │                  │
   │  - SELECT FOR UPDATE SKIP LOCKED │◄─────────────────┘
   │  - heartbeat tick every 5 s      │
   │  - dispatches to stage handler   │
   └────────────────┬─────────────────┘
                    │
                    ▼
   ┌──────────────────────────────────┐  (3) gRPC call
   │ stages/probe.py                  │   MediaService.Probe(path)
   │  - reads videos.path           │──────────────────────────┐
   │  - calls media_grpc.Probe()       │                          │
   │  - on result: persist + advance   │                          │
   └────────────────┬─────────────────┘                          ▼
                    │                            ┌─────────────────────────────┐
                    │                            │ Go API/Streaming process    │
                    │                            │  pkg/grpcserver/media.go    │
                    │                            │   ├ probe.CmdProber         │
                    │                            │   │  (plan 01-03)            │
                    │                            │   └ ffprobe subprocess       │
                    │                            └─────────────────────────────┘
                    │ (4) tx: in one BEGIN/COMMIT
                    ▼
   ┌──────────────────────────────────────────────┐
   │ asyncpg transaction                           │
   │   UPSERT media_info                           │
   │   UPSERT audio_tracks (one per stream)        │
   │   UPSERT subtitle_streams                     │
   │   UPSERT chapters                             │
   │   UPDATE videos.duration_sec                  │
   │   UPDATE videos.state DISCOVERED → PROBED     │
   │     (or → READY_NO_AUDIO if Audio == 0)       │
   │   INSERT processing_jobs(stage='extract')     │
   │     (only when PROBED, not READY_NO_AUDIO)    │
   │   UPDATE processing_jobs(probe).state='done'  │
   │ COMMIT                                        │
   └──────────────────────────────────────────────┘
                    │ (5) NOTIFY 'video_state_changed'
                    ▼
        API broadcasts WS; extract-worker wakes
```

Five things to notice:

1. **Python is the stage owner.** Per architecture §1.3 / §3, every ML/AI
   pipeline stage runs in Python. Probe is the first non-trivial stage
   and sets the pattern for `extract`, `transcribe`, `index`,
   `thumbnail`.
2. **Go is the FFprobe touch-point.** Python never shells out to
   ffprobe. There is one parser, one error taxonomy, one set of
   fixtures — the Go code from plan 01-03.
3. **Persistence is in the Python tx**, not in the Go gRPC response.
   The Go service is a *pure function*: path → metadata. Side effects
   (DB writes, FSM advance, job enqueue) are the stage's responsibility
   and atomic with claiming the job.
4. **Idempotent on replay.** If the worker dies after step (3) and
   another worker re-claims, step (4) on conflict does nothing (state
   already PROBED, audio_tracks rows already there, extract job already
   pending). Plan 01-03's `ON CONFLICT DO NOTHING` SQL still applies.
5. **No disk I/O.** Probe is a metadata-only stage. No temp files, no
   output stream. Resource accounting (Story 2.4) only kicks in once
   extraction starts.

---

## 2. New artifacts (delta over plan 01-03)

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Proto | `shared/proto/pipeline.proto` | **new message** | `ProbeRequest`, `ProbeResponse`, `MediaService.Probe` RPC. |
| Go | `apps/api/internal/grpcserver/media.go` | **new** | gRPC server impl that wraps `internal/ffmpeg/probe.Prober`. |
| Go | `apps/api/internal/grpcserver/media_test.go` | **new** | RPC-level tests using a fake `Prober`. |
| Go | `apps/api/cmd/api/main.go` | **edit** | Register `MediaServer` on the existing gRPC listener. |
| Python | `pipeline/src/maktaba_pipeline/pipeline/stages/probe.py` | **new** | The stage handler. |
| Python | `pipeline/src/maktaba_pipeline/db/probe_queries.py` | **new** | asyncpg queries mirroring sqlc names. |
| Python | `pipeline/tests/stages/test_probe_stage.py` | **new** | Stage-level tests with a fake gRPC client + sqlite. |
| Python | `pipeline/src/maktaba_pipeline/grpc_clients.py` | **edit** | Add `MediaServiceStub` factory. |
| SQL | `shared/db/queries/probe.sql` | **edit** | Add `MarkJobDone`, `MarkJobFailed` (if not already in the queue plan). |
| Schema | _no new migrations_ | — | All tables already added in plan 01-03 + Epic 1. |

---

## 3. gRPC contract

### 3.1 Proto definition (additions to `pipeline.proto`)

```proto
syntax = "proto3";
package maktaba.pipeline.v1;

option go_package = "github.com/maktaba/maktaba/shared/proto/gen/go/pipeline/v1;pipelinev1";

import "google/protobuf/timestamp.proto";

service MediaService {
  // Probe runs ffprobe against an absolute path and returns the
  // normalized metadata. Pure function — no DB writes. Caller owns
  // persistence.
  rpc Probe(ProbeRequest) returns (ProbeResponse);
}

message ProbeRequest {
  // Absolute path on the host filesystem the API/Streaming service can
  // see. Pipeline has the same mount, so the path is portable.
  string path = 1;
  // Optional per-call timeout override; 0 = use server default (30 s).
  uint32 timeout_ms = 2;
}

message ProbeResponse {
  string container = 1;
  double duration_sec = 2;
  uint32 bitrate_kbps = 3;
  VideoStream video = 4;
  repeated AudioTrack audio = 5;
  repeated SubtitleStream subtitles = 6;
  repeated Chapter chapters = 7;
  bool has_subtitles = 8;
  google.protobuf.Timestamp probed_at = 9;
  // Verbatim ffprobe stdout. Stored in media_info.raw_ffprobe.
  bytes raw_ffprobe = 10;
}

message VideoStream {
  uint32 index = 1;
  string codec = 2;
  uint32 width = 3;
  uint32 height = 4;
  double fps = 5;
  uint32 bitrate_kbps = 6;
}

message AudioTrack {
  uint32 index = 1;        // ffmpeg -map 0:a:N maps to this
  string codec = 2;
  uint32 channels = 3;
  uint32 sample_rate = 4;
  string language = 5;     // ISO 639-3, "und" if absent — never empty
  string title = 6;
  bool is_default = 7;
}

message SubtitleStream {
  uint32 index = 1;
  string codec = 2;
  string language = 3;
  string title = 4;
  bool is_default = 5;
  bool is_forced = 6;
  bool is_hearing_impaired = 7;
}

message Chapter {
  uint32 seq = 1;
  double start_sec = 2;
  double end_sec = 3;
  string title = 4;
}
```

**Status codes — Probe.**

| ffprobe outcome | gRPC status | Detail message | Stage action |
|---|---|---|---|
| Success | `OK` | — | Persist + advance FSM. |
| Binary not found | `FAILED_PRECONDITION` | `"ffprobe binary not on PATH"` | Stage marks job `failed`, **no retry** (operator must fix). Surfaces in `/healthz`. |
| Timeout | `DEADLINE_EXCEEDED` | `"ffprobe timed out after 30s"` | Retry with backoff (Epic 6 policy). |
| Corrupt / non-zero exit | `INVALID_ARGUMENT` | `"ffprobe rejected the file: <stderr>"` | Mark job + video `failed`, store stderr in `last_error`. User can re-trigger via Story 1.4. |
| Unsupported (zero streams) | `INVALID_ARGUMENT` | `"no recognizable streams"` | Same as corrupt. |
| Path missing | `NOT_FOUND` | `"path not accessible: <path>"` | Mark video `missing` (Epic 1.6 FSM allows this), don't retry until next scan. |

The mapping from `internal/ffmpeg/probe`'s sentinel errors to gRPC
codes happens in the server impl (§3.2). This is the **only** place
the codes are decided; the Python side reacts purely to gRPC status.

### 3.2 Go server (`apps/api/internal/grpcserver/media.go`)

```go
package grpcserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pipelinev1 "github.com/maktaba/maktaba/shared/proto/gen/go/pipeline/v1"
	"github.com/maktaba/maktaba/internal/ffmpeg/probe"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MediaServer implements pipelinev1.MediaServiceServer.
//
// It is a thin façade over internal/ffmpeg/probe — no DB writes, no
// caching. The Pipeline Service owns persistence; the API/Streaming
// services have their own probe-cache for ad-hoc lookups but call the
// same Prober here.
type MediaServer struct {
	pipelinev1.UnimplementedMediaServiceServer
	Prober probe.Prober
	Logger *slog.Logger
}

func NewMediaServer(p probe.Prober, l *slog.Logger) *MediaServer {
	if l == nil {
		l = slog.Default()
	}
	return &MediaServer{Prober: p, Logger: l}
}

func (s *MediaServer) Probe(
	ctx context.Context, req *pipelinev1.ProbeRequest,
) (*pipelinev1.ProbeResponse, error) {
	if req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	meta, err := s.Prober.Probe(ctx, req.GetPath())
	if err != nil {
		return nil, mapProbeErr(err)
	}
	return toProto(meta), nil
}

func mapProbeErr(err error) error {
	switch {
	case errors.Is(err, probe.ErrFFprobeNotFound):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, probe.ErrTimeout):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, probe.ErrCorrupt), errors.Is(err, probe.ErrUnsupported):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		// Wrap unknowns as Internal — the Pipeline runner retries those.
		return status.Error(codes.Internal, err.Error())
	}
}

func toProto(m probe.VideoMetadata) *pipelinev1.ProbeResponse {
	resp := &pipelinev1.ProbeResponse{
		Container:    m.Container,
		DurationSec:  m.DurationSec,
		BitrateKbps:  uint32(m.BitrateKbps),
		HasSubtitles: m.HasSubtitles,
		ProbedAt:     timestamppb.New(m.ProbedAt),
		RawFfprobe:   []byte(m.Raw),
		Video: &pipelinev1.VideoStream{
			Index:       uint32(m.Video.Index),
			Codec:       m.Video.Codec,
			Width:       uint32(m.Video.Width),
			Height:      uint32(m.Video.Height),
			Fps:         m.Video.FPS,
			BitrateKbps: uint32(m.Video.Bitrate),
		},
	}
	for _, a := range m.Audio {
		resp.Audio = append(resp.Audio, &pipelinev1.AudioTrack{
			Index:      uint32(a.Index),
			Codec:      a.Codec,
			Channels:   uint32(a.Channels),
			SampleRate: uint32(a.SampleRate),
			Language:   a.Language,
			Title:      a.Title,
			IsDefault:  a.IsDefault,
		})
	}
	for _, s := range m.Subtitles {
		resp.Subtitles = append(resp.Subtitles, &pipelinev1.SubtitleStream{
			Index:             uint32(s.Index),
			Codec:             s.Codec,
			Language:          s.Language,
			Title:             s.Title,
			IsDefault:         s.IsDefault,
			IsForced:          s.IsForced,
			IsHearingImpaired: s.IsHearingImpaired,
		})
	}
	for _, c := range m.Chapters {
		resp.Chapters = append(resp.Chapters, &pipelinev1.Chapter{
			Seq:      uint32(c.Seq),
			StartSec: c.StartSec,
			EndSec:   c.EndSec,
			Title:    c.Title,
		})
	}
	return resp
}
```

### 3.3 Wiring in `apps/api/cmd/api/main.go`

```go
prober := probe.NewCmdProber(cfg.FFprobeBinary, cfg.ProbeTimeout, logger)
mediaSrv := grpcserver.NewMediaServer(prober, logger.With("rpc", "media"))
pipelinev1.RegisterMediaServiceServer(grpcServer, mediaSrv)
```

No new transport, no new auth — same gRPC server (mTLS between
internal services per architecture §2.1).

---

## 4. Python pipeline stage

### 4.1 Stage interface (architecture §1.4)

Every stage implements:

```python
class StageHandler(Protocol):
    name: ClassVar[str]                         # "probe"
    async def handle(
        self, ctx: StageContext, job: Job,
    ) -> StageOutcome: ...
```

`StageContext` carries the asyncpg pool, gRPC clients, settings, and
the heartbeat callback. `StageOutcome` is one of `Done`, `RetryAfter(s)`,
`FailedTerminal(msg)`. The runner translates these into the right
`processing_jobs` row updates.

### 4.2 `pipeline/stages/probe.py`

```python
"""Audio probe stage.

Claims a 'probe' job, calls MediaService.Probe over gRPC, persists the
result in one transaction, and advances the video FSM.

See specs/epics/02-audio-extraction/story-02-01-audio-probe.md.
"""
from __future__ import annotations

import asyncio
import logging
from typing import ClassVar

import grpc
from google.protobuf.json_format import MessageToDict

from maktaba_pipeline.db import probe_queries as q
from maktaba_pipeline.pipeline.types import (
    Done, FailedTerminal, Job, RetryAfter, StageContext, StageOutcome,
)
from maktaba_pipeline.proto.pipeline.v1 import pipeline_pb2 as pb

log = logging.getLogger(__name__)


class ProbeStage:
    name: ClassVar[str] = "probe"

    async def handle(self, ctx: StageContext, job: Job) -> StageOutcome:
        # 1. Look up the file path. The video row was created by the scanner.
        async with ctx.pool.acquire() as conn:
            row = await conn.fetchrow(
                "SELECT id, path, state FROM videos WHERE id=$1", job.video_id,
            )
        if row is None:
            return FailedTerminal(f"video {job.video_id} vanished")
        if row["state"] != "discovered":
            # Idempotent replay — somebody already ran probe. Mark job done.
            log.info("probe replay no-op", extra={"video_id": str(job.video_id)})
            await self._mark_job_done(ctx, job)
            return Done()

        # 2. Call gRPC.
        try:
            resp = await ctx.media.Probe(
                pb.ProbeRequest(path=row["path"]),
                timeout=ctx.settings.probe_timeout_s,
            )
        except grpc.aio.AioRpcError as e:
            return _outcome_for_grpc_error(e, row["path"])

        # 3. Persist + advance, all in one tx.
        try:
            await self._persist(ctx, job, row, resp)
        except Exception as e:
            log.exception("probe persist failed", extra={"video_id": str(job.video_id)})
            return RetryAfter(seconds=15, reason=f"persist error: {e}")

        return Done()

    async def _persist(
        self, ctx: StageContext, job: Job, video_row, resp: pb.ProbeResponse,
    ) -> None:
        next_state = "probed" if resp.audio else "ready_no_audio"
        async with ctx.pool.acquire() as conn:
            async with conn.transaction():
                await q.upsert_media_info(conn, video_row["id"], resp)
                await q.update_video_duration(
                    conn, video_row["id"], resp.duration_sec,
                )
                for at in resp.audio:
                    await q.upsert_audio_track(conn, video_row["id"], at)
                for s in resp.subtitles:
                    await q.upsert_subtitle_stream(conn, video_row["id"], s)
                for ch in resp.chapters:
                    await q.upsert_chapter(conn, video_row["id"], ch)
                advanced = await q.advance_video_state(
                    conn, video_row["id"], "discovered", next_state,
                )
                # advanced=False means the FSM already moved past discovered;
                # treat as no-op (idempotent) but still mark the job done.
                if advanced and next_state == "probed":
                    await q.enqueue_extract_job(conn, video_row["id"])
                await q.mark_job_done(conn, job.id)

    async def _mark_job_done(self, ctx: StageContext, job: Job) -> None:
        async with ctx.pool.acquire() as conn:
            async with conn.transaction():
                await q.mark_job_done(conn, job.id)


def _outcome_for_grpc_error(
    e: grpc.aio.AioRpcError, path: str,
) -> StageOutcome:
    code = e.code()
    detail = e.details() or ""
    if code is grpc.StatusCode.FAILED_PRECONDITION:
        # Operator-fixable; do not retry.
        return FailedTerminal(f"ffprobe binary missing: {detail}")
    if code is grpc.StatusCode.DEADLINE_EXCEEDED:
        return RetryAfter(seconds=60, reason="probe timeout")
    if code is grpc.StatusCode.INVALID_ARGUMENT:
        # Corrupt or unsupported file. Move video to failed; don't retry.
        return FailedTerminal(f"ffprobe rejected file: {detail}")
    if code is grpc.StatusCode.NOT_FOUND:
        return FailedTerminal(f"path missing: {path}")
    # Internal / Unavailable / Unknown — transient.
    return RetryAfter(seconds=30, reason=f"grpc {code.name}: {detail}")
```

### 4.3 asyncpg queries (`pipeline/db/probe_queries.py`)

```python
"""asyncpg mirrors of the sqlc queries from plan 01-03 §4."""
from __future__ import annotations
import asyncpg

from maktaba_pipeline.proto.pipeline.v1 import pipeline_pb2 as pb


async def upsert_media_info(
    conn: asyncpg.Connection, video_id, resp: pb.ProbeResponse,
) -> None:
    await conn.execute(
        """
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
        """,
        video_id,
        resp.container or None,
        resp.video.codec or None,
        resp.video.width or None,
        resp.video.height or None,
        resp.video.fps or None,
        resp.bitrate_kbps or None,
        resp.has_subtitles,
        bytes(resp.raw_ffprobe),
    )


async def upsert_audio_track(
    conn: asyncpg.Connection, video_id, at: pb.AudioTrack,
) -> None:
    await conn.execute(
        """
        INSERT INTO audio_tracks (
            video_id, index, codec, channels, sample_rate,
            language, title, is_default
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (video_id, index) DO NOTHING;
        """,
        video_id, at.index, at.codec or None,
        at.channels or None, at.sample_rate or None,
        at.language or "und", at.title or None, at.is_default,
    )


async def upsert_subtitle_stream(
    conn: asyncpg.Connection, video_id, s: pb.SubtitleStream,
) -> None:
    await conn.execute(
        """
        INSERT INTO subtitle_streams (
            video_id, index, codec, language, title,
            is_default, is_forced, is_hearing_impaired
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (video_id, index) DO NOTHING;
        """,
        video_id, s.index, s.codec or None,
        s.language or "und", s.title or None,
        s.is_default, s.is_forced, s.is_hearing_impaired,
    )


async def upsert_chapter(
    conn: asyncpg.Connection, video_id, ch: pb.Chapter,
) -> None:
    await conn.execute(
        """
        INSERT INTO chapters (video_id, seq, start_sec, end_sec, title)
        VALUES ($1,$2,$3,$4,$5)
        ON CONFLICT (video_id, seq) DO NOTHING;
        """,
        video_id, ch.seq, ch.start_sec, ch.end_sec, ch.title or None,
    )


async def update_video_duration(
    conn: asyncpg.Connection, video_id, duration_sec: float,
) -> None:
    if duration_sec <= 0:
        return
    await conn.execute(
        "UPDATE videos SET duration_sec=$2, updated_at=now() WHERE id=$1",
        video_id, duration_sec,
    )


async def advance_video_state(
    conn: asyncpg.Connection, video_id, frm: str, to: str,
) -> bool:
    row = await conn.fetchrow(
        """
        UPDATE videos SET state=$3, updated_at=now()
        WHERE id=$1 AND state=$2
        RETURNING id;
        """,
        video_id, frm, to,
    )
    return row is not None  # False on idempotent replay


async def enqueue_extract_job(conn: asyncpg.Connection, video_id) -> None:
    # The partial unique index (slot 0002, plan-06-01) is keyed on
    # (video_id, stage) WHERE state IN
    # ('pending','claimed','running','paused','resuming'). Postgres only
    # binds ON CONFLICT to a partial unique index when the WHERE
    # predicate matches **exactly**, so we repeat the full state list
    # here.
    await conn.execute(
        """
        INSERT INTO processing_jobs (video_id, stage, state)
        VALUES ($1, 'extract', 'pending')
        ON CONFLICT (video_id, stage)
            WHERE state IN ('pending','claimed','running','paused','resuming')
        DO NOTHING;
        """,
        video_id,
    )


async def mark_job_done(conn: asyncpg.Connection, job_id) -> None:
    await conn.execute(
        """
        UPDATE processing_jobs
           SET state='done', finished_at=now(), updated_at=now()
         WHERE id=$1;
        """,
        job_id,
    )
```

These queries are intentionally a 1:1 mirror of the sqlc set from plan
01-03 §4. The cross-language consistency is held by `shared/db/queries`
being the single source of truth and CI running both code paths against
the same fixtures.

---

## 5. Test plan

Tests come in three groups: **gRPC server** (Go), **stage handler**
(Python, integration with sqlite + fake gRPC client), **end-to-end**
(real ffprobe + real Postgres, smoke only). Plan 01-03's parser tests
already guard JSON normalization; we don't re-test them here.

### 5.1 Go gRPC server tests (`media_test.go`)

| # | Name | What it checks |
|---|---|---|
| G1 | `Probe_OK` | Fake `Prober` returns canned `VideoMetadata` → response fields populated correctly; `RawFfprobe` round-trips bytes. |
| G2 | `Probe_ErrFFprobeNotFound_mapsFailedPrecondition` | Returns `ErrFFprobeNotFound` → gRPC `FAILED_PRECONDITION`. |
| G3 | `Probe_ErrTimeout_mapsDeadlineExceeded` | `ErrTimeout` → `DEADLINE_EXCEEDED`. |
| G4 | `Probe_ErrCorrupt_mapsInvalidArgument` | `ErrCorrupt` (with stderr in detail) → `INVALID_ARGUMENT`. |
| G5 | `Probe_ErrUnsupported_mapsInvalidArgument` | `ErrUnsupported` → `INVALID_ARGUMENT`. |
| G6 | `Probe_emptyPath_returnsInvalidArgument` | Empty `path` → `INVALID_ARGUMENT` with no Prober call. |
| G7 | `Probe_perCallTimeoutOverride` | `req.TimeoutMs=50`, fake Prober blocks 200 ms → context deadline observed by Prober. |
| G8 | `Probe_unknownErr_mapsInternal` | Wrapped non-sentinel error → `INTERNAL`. |

```go
func TestMediaServer_Probe(t *testing.T) {
	cases := []struct {
		name     string
		setup    func() *grpcserver.MediaServer
		req      *pipelinev1.ProbeRequest
		wantCode codes.Code
		check    func(t *testing.T, resp *pipelinev1.ProbeResponse)
	}{
		{
			name: "ok",
			setup: func() *grpcserver.MediaServer {
				return grpcserver.NewMediaServer(&fakeProber{
					meta: probe.VideoMetadata{
						Container:   "matroska",
						DurationSec: 60.0,
						Audio: []probe.AudioTrack{
							{Index: 1, Codec: "aac", Language: "ara", IsDefault: true},
						},
						Raw: json.RawMessage(`{}`),
					},
				}, nil)
			},
			req:      &pipelinev1.ProbeRequest{Path: "/x.mkv"},
			wantCode: codes.OK,
			check: func(t *testing.T, r *pipelinev1.ProbeResponse) {
				if len(r.Audio) != 1 || r.Audio[0].Language != "ara" {
					t.Fatalf("audio = %+v", r.Audio)
				}
			},
		},
		{
			name:     "ffprobe_missing",
			setup:    func() *grpcserver.MediaServer {
				return grpcserver.NewMediaServer(
					&fakeProber{err: probe.ErrFFprobeNotFound}, nil)
			},
			req:      &pipelinev1.ProbeRequest{Path: "/x.mkv"},
			wantCode: codes.FailedPrecondition,
		},
		// … G3–G8 follow the same pattern …
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.setup()
			resp, err := s.Probe(context.Background(), tc.req)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("code = %s, want %s; err=%v", got, tc.wantCode, err)
			}
			if tc.check != nil {
				tc.check(t, resp)
			}
		})
	}
}

type fakeProber struct {
	meta  probe.VideoMetadata
	err   error
	delay time.Duration
}

func (f *fakeProber) Probe(ctx context.Context, _ string) (probe.VideoMetadata, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return probe.VideoMetadata{}, probe.ErrTimeout
		}
	}
	return f.meta, f.err
}
```

### 5.2 Python stage tests (`tests/stages/test_probe_stage.py`)

A real asyncpg-backed sqlite (via `aiosqlite`) test DB seeded with one
DISCOVERED video, plus a fake `MediaServiceStub` whose `Probe` coroutine
returns canned `ProbeResponse` (or raises `AioRpcError`). These map
directly to the story's stated test cases.

| # | Story ref | Name | What it checks |
|---|---|---|---|
| P1 | `test_probe_writes_media_info` | `test_lecture_writes_media_info` | One audio row with `language='ara'`, `is_default=true`; `media_info` populated; state = `probed`; one `extract` job pending. |
| P2 | `test_probe_writes_one_audio_row_per_track` | `test_multiaudio_three_tracks` | 3 `audio_tracks` rows; `is_default` set on the disposition-default one only; one `extract` job (not three). |
| P3 | `test_probe_handles_undefined_language` | `test_undefined_language_becomes_und` | Audio row has `language='und'`. |
| P4 | `test_probe_audioless_video` | `test_silent_video_goes_ready_no_audio` | `audio_tracks` empty; state = `ready_no_audio`; **no** `extract` job. |
| P5 | `test_probe_idempotent_on_replay` | `test_replay_is_noop` | Run handler twice → row counts unchanged; state still `probed`; still exactly one `extract` job; both job rows end `done`. |
| P6 | (edge) | `test_corrupt_file_marks_failed` | gRPC `INVALID_ARGUMENT` → outcome `FailedTerminal`; video state → `failed`; `last_error` populated; **no** `extract` job. |
| P7 | (edge) | `test_timeout_returns_retry_after` | gRPC `DEADLINE_EXCEEDED` → outcome `RetryAfter(60)`; job row stays `pending` (or `running` with `attempts++`); no FSM change. |
| P8 | (edge) | `test_ffprobe_missing_marks_failed_no_retry` | gRPC `FAILED_PRECONDITION` → `FailedTerminal`; job moves to `failed` *without* incrementing retries (operator-fixable). |
| P9 | (edge) | `test_video_already_probed_is_noop` | Pre-seed video with state=`probed` → handler observes mismatch, marks job `done`, no DB writes beyond that. |
| P10 | (edge) | `test_subtitles_populated` | `ProbeResponse.subtitles` length 2 → 2 `subtitle_streams` rows; `media_info.has_subtitles=true`. |
| P11 | (edge) | `test_chapters_populated` | `ProbeResponse.chapters` length 5 → 5 `chapters` rows in seq order. |
| P12 | (edge) | `test_mid_file_codec_change_passthrough` | First-packet codec is what we record; the test asserts we don't try to detect the change here (acknowledged in story §Edge cases — the *extractor* handles it). |
| P13 | (edge) | `test_mislabeled_track_recorded_as_declared` | Track tagged `language=eng` but actually Arabic → row says `eng`; we never override (story §Edge cases). |
| P14 | (edge) | `test_video_missing_marks_terminal` | gRPC `NOT_FOUND` → `FailedTerminal`. |

```python
import pytest

from maktaba_pipeline.pipeline.stages.probe import ProbeStage
from maktaba_pipeline.pipeline.types import Done, FailedTerminal, RetryAfter
from tests.fakes import FakeMediaStub, make_probe_response


@pytest.mark.asyncio
async def test_lecture_writes_media_info(stage_ctx, seed_video):
    video_id = await seed_video(state="discovered", path="/x/lecture.mkv")
    resp = make_probe_response(
        container="matroska",
        duration_sec=3612.45,
        audio=[("ara", "aac", True), ],
    )
    stage_ctx.media = FakeMediaStub({"/x/lecture.mkv": resp})

    out = await ProbeStage().handle(stage_ctx, _job(video_id))

    assert isinstance(out, Done)
    row = await stage_ctx.pool.fetchrow(
        "SELECT state FROM videos WHERE id=$1", video_id)
    assert row["state"] == "probed"
    tracks = await stage_ctx.pool.fetch(
        "SELECT * FROM audio_tracks WHERE video_id=$1", video_id)
    assert len(tracks) == 1 and tracks[0]["language"] == "ara"
    jobs = await stage_ctx.pool.fetch(
        "SELECT * FROM processing_jobs "
        "WHERE video_id=$1 AND stage='extract' AND state='pending'", video_id)
    assert len(jobs) == 1


@pytest.mark.asyncio
async def test_silent_video_goes_ready_no_audio(stage_ctx, seed_video):
    video_id = await seed_video(state="discovered", path="/x/silent.mp4")
    stage_ctx.media = FakeMediaStub({
        "/x/silent.mp4": make_probe_response(audio=[]),
    })

    out = await ProbeStage().handle(stage_ctx, _job(video_id))
    assert isinstance(out, Done)

    row = await stage_ctx.pool.fetchrow(
        "SELECT state FROM videos WHERE id=$1", video_id)
    assert row["state"] == "ready_no_audio"
    jobs = await stage_ctx.pool.fetch(
        "SELECT 1 FROM processing_jobs "
        "WHERE video_id=$1 AND stage='extract'", video_id)
    assert jobs == []


@pytest.mark.asyncio
async def test_replay_is_noop(stage_ctx, seed_video):
    video_id = await seed_video(state="discovered", path="/x/lecture.mkv")
    resp = make_probe_response(audio=[("ara", "aac", True)])
    stage_ctx.media = FakeMediaStub({"/x/lecture.mkv": resp})

    await ProbeStage().handle(stage_ctx, _job(video_id, job_id=1))
    await ProbeStage().handle(stage_ctx, _job(video_id, job_id=2))

    tracks = await stage_ctx.pool.fetch(
        "SELECT * FROM audio_tracks WHERE video_id=$1", video_id)
    assert len(tracks) == 1  # not 2
    extract = await stage_ctx.pool.fetch(
        "SELECT * FROM processing_jobs "
        "WHERE video_id=$1 AND stage='extract'", video_id)
    assert len(extract) == 1
```

### 5.3 End-to-end smoke (`tests/e2e/test_probe_real_ffprobe.py`)

Skipped on CI machines without ffprobe (`pytest.importorskip` + binary
detection). Uses the four fixture clips from `shared/fixtures/samples/`:

| Fixture | Expected state | Expected audio rows | Notes |
|---|---|---|---|
| `lecture_1080p_h264_aac_ar.mkv` | `probed` | 1, ara, default | Golden case. |
| `multiaudio_3tracks.mkv` | `probed` | 3 (ara, eng, fra) | Default disposition tested. |
| `silent.mp4` | `ready_no_audio` | 0 | No `extract` job. |
| `corrupt_truncated.mkv` | `failed` | 0 | `last_error` matches `ffprobe rejected the file`. |

The smoke test runs the **whole** stack: real Go gRPC server (with real
`internal/ffmpeg/probe`), real ffprobe, real Postgres in Docker, real
Python runner. One pass per fixture; takes < 10 s on a developer
laptop.

---

## 6. Edge cases (from story §Edge cases, mapped to where they're handled)

| Edge case | Handled where | Test |
|---|---|---|
| **Mislabeled tracks** (file claims wrong language) | We record what the file claims, exactly. The transcriber's auto-detect (Epic 3) handles the divergence. The probe stage does **not** override `tags.language`. | P13 |
| **Mid-file codec change** | Probe records first-packet codec (ffprobe limitation). The extractor (Story 2.3) is the layer that catches the runtime mismatch and retries with `transcoded_extract=true`. | P12 |
| **CDN-style fragmented MPEG-TS** | Already addressed by the `-analyzeduration 100M -probesize 50M` flags hardcoded in plan 01-03 §2.2; covered by plan 01-03 test T15. No new test here. | (plan 01-03 T15) |
| **Corrupt / truncated file** | Maps to gRPC `INVALID_ARGUMENT` → stage `FailedTerminal` → video `failed` with stderr in `last_error`. User can re-trigger via Story 1.4. | P6, smoke |
| **No audio track** | Stage routes to `READY_NO_AUDIO` (terminal-but-searchable state — title/description still indexable; no transcript). **Critically: no `extract` job is enqueued.** | P4, smoke |
| **Multi-track audio** | Plan 01-03's parser already handles multiple `codec_type=audio` streams. Stage iterates `resp.audio` and upserts each row. Only **one** `extract` job is enqueued for the video; track *selection* is Story 2.2's job, run inside that stage. | P2 |
| **ffprobe binary missing** | gRPC `FAILED_PRECONDITION` → `FailedTerminal` with no retry (operator-fixable). Surfaces in `/healthz`. | P8 |
| **Path becomes inaccessible between scan and probe** | gRPC `NOT_FOUND` → `FailedTerminal`. Video state stays `discovered` → wait for next scan to either re-discover or mark `missing` (Epic 1 owns that transition). | P14 |
| **Worker dies mid-tx** | The whole persist + state-advance + extract-enqueue is one Postgres tx, so a crash either commits everything or nothing. The job's heartbeat lock expires, another worker picks it up. The replay test (P5) guards this. | P5 |
| **Probe replayed after extract already ran** | `advance_video_state` is conditional on `state='discovered'`; idempotent persist queries skip on conflict. Handler returns `Done` without further effect. | P9 |

### 6.1 Explicitly out of scope (deferred to other stories)

- **Track selection logic** (default vs Arabic vs first available) →
  Story 2.2.
- **Audio extraction command** (`ffmpeg -i {file} -map 0:a:N -ac 1 -ar
  16000 -f wav -`) → Story 2.3. The probe stage writes nothing to disk;
  there is no temp-file lifecycle here.
- **Resource accounting** (concurrency caps, disk-watermark eviction) →
  Story 2.4. Probe is cheap (≤30 s, ~50 MB read budget) and not
  rate-limited.

---

## 7. Operational concerns

| Concern | Decision |
|---|---|
| **Telemetry** | OpenTelemetry span `pipeline.stage.probe` per job; attributes: `video_id`, `path`, `duration_ms`, `audio_count`, `subtitle_count`, `outcome` (`done`/`retry`/`failed`). Span links to the `ffmpeg.probe` child span emitted by the Go server. |
| **Logging** | One INFO log per outcome (`probe.done`, `probe.retry`, `probe.failed`); one WARN log per gRPC error with code, detail, path. Same JSON shape as the Go side. |
| **Metrics** | Counters: `pipeline_probe_total{outcome}`, `pipeline_probe_duration_seconds` (histogram), `pipeline_probe_audio_tracks_total` (histogram, bucketed 0/1/2/3+). |
| **Healthcheck** | `/healthz` calls `MediaService.Probe` once at startup against a tiny known-good fixture. Failure → unhealthy until ffprobe is fixed. |
| **Retry policy** | `RetryAfter` outcomes go back to `pending` with `next_attempt_at = now() + delay` and `attempts++`. Max 3 attempts before terminal failure. (Epic 6 owns the runner; this stage just returns the outcome.) |
| **Concurrency** | Probe is fast (median 200–400 ms); the runner can fan out to N workers. Cap via existing global semaphore. Story 2.4 adds the disk-aware caps for `extract`; probe isn't disk-bound. |

---

## 8. Acceptance checklist

Sourced from [story-02-01-audio-probe.md](story-02-01-audio-probe.md).
Items already discharged by plan 01-03 are marked **(↑01-03)** and not
re-asserted here.

**Behavioral**

- [ ] A `DISCOVERED` video successfully probed populates `media_info`
      (container, video codec, resolution, fps, bitrate, has_subtitles).
      **(↑01-03 parsing; new: stage writes the row.)**
- [ ] One `audio_tracks` row per audio stream is written with `index`,
      `codec`, `channels`, `sample_rate`, `language`, `title`,
      `is_default`. **(↑01-03 parsing; new: stage UPSERT loop.)**
- [ ] `language` is ISO 639-3 from `tags.language`; absent → `'und'`,
      never NULL. **(↑01-03; reasserted by P3.)**
- [ ] `is_default` is set when `disposition.default == 1`. **(↑01-03.)**
- [ ] State advances `discovered → probed` exactly once on success. (P1, P5)
- [ ] A `processing_jobs(stage='extract', state='pending')` row is
      enqueued exactly once on success. (P1, P5)
- [ ] Audioless video → state advances `discovered → ready_no_audio`,
      **and no `extract` job is enqueued**. (P4)
- [ ] Replaying probe is idempotent: same row counts, same job count,
      same end state. (P5, P9)
- [ ] Embedded subtitle streams populate `subtitle_streams`;
      `media_info.has_subtitles` reflects presence. (P10)
- [ ] Embedded chapters populate `chapters`. (P11)

**Implementation invariants**

- [ ] FFprobe is invoked exclusively via `MediaService.Probe`; the
      Python side never shells out directly. (Reviewed in CR; grep
      guard in CI: `pipeline/` must not contain `subprocess.*ffprobe`.)
- [ ] All persistence + FSM advance + job enqueue + job-row update
      happen in **one** asyncpg transaction. (P5 implicitly guards.)
- [ ] gRPC error codes map to outcomes per §3.1; no `INVALID_ARGUMENT`
      or `FAILED_PRECONDITION` ever causes a retry. (P6, P8)
- [ ] `raw_ffprobe` JSONB stored verbatim. **(↑01-03; preserved
      end-to-end by `bytes(resp.raw_ffprobe)`.)**

**Operational**

- [ ] `/healthz` includes a `probe.binary_present` sub-check (calls a
      synthetic `MediaService.Probe`).
- [ ] Span `pipeline.stage.probe` emitted per call with the attribute
      set in §7.
- [ ] Counter `pipeline_probe_total{outcome="done"|"retry"|"failed"}`
      and histogram `pipeline_probe_duration_seconds` exposed on
      `/metrics`.
- [ ] All Python tests in §5.2 pass (P1–P14).
- [ ] All Go tests in §5.1 pass (G1–G8).
- [ ] E2E smoke (§5.3) passes against the four fixture clips on Linux
      and macOS CI runners with ffprobe 6.x or 7.x pinned.
