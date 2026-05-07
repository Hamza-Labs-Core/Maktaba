# Plan 02-03 — Stream Extraction (PCM via pipe, no intermediate WAV)

> **Note on scope.** This plan covers the `extract` pipeline stage: spawn
> FFmpeg, decode the selected audio track to mono 16-bit 16 kHz PCM, and
> hand it to the transcriber as either an async iterator of byte chunks
> (default, streaming) or a temp WAV file (fallback when the STT backend
> demands a path). The user-facing spec is
> [story-02-03-stream-extraction.md](story-02-03-stream-extraction.md);
> the architecture references are
> [§3.3 Audio Extractor](../../architecture.md) (the canonical FFmpeg
> command) and [§7 Batch Processing](../../architecture.md) (pause /
> resume / heartbeat semantics that this stage must honor).
>
> Track selection (Story 2.2) and resource accounting / concurrency
> (Story 2.4) are **not** in scope here — this plan assumes a single
> selected `audio_tracks` row is the input, and that the worker's
> per-stage semaphore has already gated the slot.
>
> The implementation lives in the Python Pipeline Service because the
> stream is consumed by the in-process Python STT loop; sending PCM
> bytes through gRPC just to re-receive them in the same Python process
> would defeat the "no intermediate copy" purpose. **However**, the
> exact same FFmpeg invocation is needed by the Streaming Service (Go)
> for the future "download audio as WAV" endpoint and by API
> diagnostics; the Go counterpart is a thin shared package
> `internal/ffmpeg/extract` that mirrors the Python wrapper byte-for-byte
> on the FFmpeg argv. One canonical command; two language wrappers; one
> set of fixtures.

---

## 1. Architecture diagram — extract stage flow

```
                 ┌──────────────────────────────────────────────┐
                 │  Pipeline runner claims processing_jobs row  │
                 │  (stage='extract', state='pending')          │
                 │  → workers/runner.py::run_stage(extract)     │
                 └────────────────────┬─────────────────────────┘
                                      │
                                      ▼
                 ┌──────────────────────────────────────────────┐
                 │  ExtractStage.run(ctx, job)                  │
                 │   1. Load video + selected audio_track       │
                 │   2. Resolve seek_from = last_segment_end_sec│
                 │   3. Decide mode: stream | file              │
                 │      based on stt.requires_file              │
                 └────────┬───────────────────────────┬─────────┘
                          │ stream mode               │ file mode
                          ▼                           ▼
        ┌──────────────────────────────┐  ┌────────────────────────────┐
        │  AudioPipeReader (async)     │  │  AudioFileExtractor        │
        │  - asyncio.create_subprocess │  │  - same ffmpeg, -f wav     │
        │  - PCM chunks 64 KiB         │  │  - writes ~/.maktaba/cache │
        │    yielded as AsyncIterator  │  │    /audio/{hash}.wav       │
        │  - process group, signal mgr │  │  - returns Path            │
        └────────────────┬─────────────┘  └────────────┬───────────────┘
                         │                             │
                         │ async for chunk             │ path
                         ▼                             ▼
        ┌──────────────────────────────────────────────────────────┐
        │  STTBackend.transcribe(audio=...)                        │
        │   - whisper-mlx, faster-whisper: stream                  │
        │   - openai-whisper (some), openai-api: file              │
        └──────────────────────┬───────────────────────────────────┘
                               │ Segments
                               ▼
        ┌──────────────────────────────────────────────────────────┐
        │  Per-segment commit (architecture §7.6):                 │
        │  insert segment + advance last_segment_end_sec + heartbeat│
        │  + cooperative pause/cancel check                        │
        └──────────────────────┬───────────────────────────────────┘
                               │
                               ▼
        ┌──────────────────────────────────────────────────────────┐
        │  On terminal state (done / failed / cancelled):          │
        │   - file mode: unlink cache wav                          │
        │   - stream mode: ensure ffmpeg reaped (SIGTERM → SIGKILL)│
        │   - advance videos.state = 'audio_extracted' on done     │
        └──────────────────────────────────────────────────────────┘
```

The extract stage **does not** persist PCM bytes anywhere durable (in
streaming mode). The only durable artifacts of a successful extract are
(a) the segments the transcriber produces and (b) the state transition
itself. This is the core "no intermediate file" property.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── media/
│   ├── __init__.py
│   ├── ffmpeg.py            # subprocess wrapper, signal management
│   ├── audio.py             # AudioPipeReader, AudioFileExtractor, AudioCache
│   ├── errors.py            # FFmpegDecodeError, AudioDriftError, etc.
│   └── tests/
│       ├── conftest.py      # tmp ffmpeg discovery, fixture loader
│       ├── test_pipe_reader.py
│       ├── test_file_extractor.py
│       ├── test_signal_handling.py
│       ├── test_resume_seek.py
│       ├── test_drift_detection.py
│       └── fixtures/
│           ├── lecture_30s_ar_aac.mkv         # 30 s, 16 kHz/2ch input
│           ├── lecture_30s_ar_aac.ref.wav     # decoded reference
│           ├── ts_concat_pts_reset.ts         # PTS reset at 10 s
│           ├── vfr_30s.mkv                    # variable frame rate
│           ├── corrupt_truncated.mkv          # half-file
│           ├── renamed_text_file.mkv          # actually a .txt
│           └── broken_duration.mp4            # duration field=0
└── pipeline/
    └── stages/
        ├── extract.py        # ExtractStage(Stage) — the orchestration
        └── tests/
            ├── test_extract_stage.py
            └── test_extract_terminal_cleanup.py
```

### 2.2 Package layout — Go (shared command builder)

```
internal/ffmpeg/
├── extract/
│   ├── command.go           # BuildArgs(spec ExtractSpec) []string
│   ├── command_test.go      # ensures argv matches the canonical list
│   ├── stream.go            # StreamPCM(ctx, spec) (io.ReadCloser, *exec.Cmd, err)
│   ├── stream_test.go       # against a fake ffmpeg + a real one (build tag)
│   └── types.go             # ExtractSpec, OutputFormat enum
└── (probe/, transcode/, etc.)
```

Why Go ships a wrapper at all: the Streaming Service (Go) will eventually
expose a "download mono WAV" endpoint for power users, and the API uses
`ExtractSpec.BuildArgs` in its `POST /api/diagnostics/extract-args`
endpoint to let an operator copy-paste the exact command for repro. **The
Python pipeline does not call the Go wrapper at runtime** — both produce
the same argv list, guarded by a shared cross-language fixture
(`shared/fixtures/extract_argv.json`) so a divergence is caught in CI.

### 2.3 The exact FFmpeg command — streaming PCM (default)

```
ffmpeg \
  -hide_banner \
  -nostdin \
  -nostats \
  -loglevel error \
  -threads 1 \
  -fflags +genpts \
  [-ss {seek_from_minus_lead_in}]   # optional, only when resuming
  -i {absolute_path} \
  -map 0:a:{audio_index} \
  -vn -sn -dn \
  -ac 1 \
  -ar 16000 \
  -sample_fmt s16 \
  -f s16le \
  -                                 # stdout (pipe:1)
```

| Flag | Purpose |
|---|---|
| `-hide_banner` | No version banner; we get it from `ffmpeg -version` once at startup. |
| `-nostdin` | FFmpeg never tries to read from stdin; stdin is closed and we never accidentally feed it the parent's input. |
| `-nostats -loglevel error` | Stderr only carries actual errors; the runtime parses stderr_tail from the last 4 KiB on failure. |
| `-threads 1` | Cap FFmpeg at one decoder thread per process; we run two extracts in parallel by default and don't want either to flood the box. |
| `-fflags +genpts` | Force monotonic PTS — required for concatenated TS streams (story 02-03 edge case "Concatenated TS streams"). |
| `-ss {seek}` (input-side) | Fast input seek; placed **before** `-i` so the demuxer skips ahead instead of decoding from zero. For VBR/VFR sources we seek to `max(0, seek_from - 0.5)` and discard the lead-in (story 02-03 edge case). Omitted when `seek_from == 0`. |
| `-i {path}` | The source file, absolute path, positional. No shell. |
| `-map 0:a:{index}` | The audio stream ffprobe identified (Story 2.1). The index is the per-codec-type index (`0:a:0`, `0:a:1`), not the absolute stream index. |
| `-vn -sn -dn` | Drop video, subtitles, and data streams explicitly — defense in depth against a misnamed stream tag. |
| `-ac 1` | Downmix to mono. Whisper consumes mono. |
| `-ar 16000` | Resample to 16 kHz. Matches Whisper's expected input rate exactly; sample-rate conversion happens in FFmpeg, not Python. |
| `-sample_fmt s16` | Signed 16-bit. Two bytes per sample → predictable byte-count math. |
| `-f s16le` | Raw little-endian PCM, no container, no headers. The byte stream IS the audio. |
| `-` | Output to stdout. |

**Working directory:** the worker process's CWD; we always pass an absolute
path so CWD is irrelevant. **No shell, ever:** `asyncio.create_subprocess_exec`
on the Python side, `exec.CommandContext` on the Go side, both with
positional argv — eliminates command injection by construction.

### 2.4 The exact FFmpeg command — file-mode fallback (WAV)

When the chosen STT backend declares `requires_file = True` (some
`openai-whisper` paths and the OpenAI HTTP API), we write a temp WAV
with the same audio shape:

```
ffmpeg \
  -hide_banner -nostdin -nostats -loglevel error \
  -threads 1 -fflags +genpts \
  -i {absolute_path} \
  -map 0:a:{audio_index} -vn -sn -dn \
  -ac 1 -ar 16000 -sample_fmt s16 \
  -f wav \
  {cache_dir}/{content_hash}-a{audio_index}.wav.tmp
```

After successful exit, `os.replace()` to `{cache_dir}/{content_hash}-a{audio_index}.wav`.
The `.tmp` suffix is the architecture-§7.9 atomic-write contract; a crash
mid-write leaves a stray `.tmp` that the cache GC removes at startup.

The cache directory is `~/.maktaba/cache/audio/` by default, configurable
via `pipeline.audio_cache.dir`. Files are removed when the job reaches
`done`, `failed`, or `cancelled` — see §6.3.

### 2.5 Output format selection

`OutputFormat` is an enum carried on the `ExtractSpec`:

```
enum OutputFormat {
    PCM_S16LE_16K_MONO  // default; pipe-only
    WAV_S16LE_16K_MONO  // file-only; for backend.requires_file
    // future:
    FLAC_S16_16K_MONO   // for archival "extract once, transcribe many"
}
```

The format is decided by the `extract` stage based on
`stt.requires_file`, **not** configurable per video. The
`pipeline.audio.always_write_wav` config flag forces WAV mode for
debugging (off by default); when on, both the WAV file and the PCM pipe
are produced (the WAV via `tee`-style FFmpeg `-f tee` muxer with two
outputs). This is purely for operator diagnostics and is excluded from
the acceptance criteria.

### 2.6 Streaming PCM iterator semantics

```python
async def stream(self) -> AsyncIterator[bytes]:
    """Yield 64 KiB PCM chunks until ffmpeg exits.

    Invariants:
      - Each yield is a multiple of `BYTES_PER_SAMPLE * CHANNELS` (= 2),
        so a consumer can split on sample boundaries without buffering.
      - The iterator never yields a partial frame at EOF; the final chunk
        is whatever ffmpeg wrote, padded out to a sample boundary by
        FFmpeg itself (s16le mono is inherently sample-aligned).
      - On `aclose()` the consumer requests teardown; we send SIGTERM,
        wait up to 5 s, then SIGKILL. See §5.
      - On non-zero exit, we re-raise FFmpegDecodeError WITH stderr_tail
        AFTER yielding any bytes already received. Callers MUST handle
        the exception at their loop boundary; partial PCM is delivered
        but the segment commit checks the final exception and refuses
        to advance state (see story-02-03 acceptance: "no partial PCM
        is delivered" — interpreted as "no partial PCM is treated as
        authoritative", i.e. the run_extract/transcribe transaction
        rolls back).
    """
```

The 64 KiB chunk size is arbitrary but tuned: at 16 kHz × 2 bytes that's
2.0 s of audio, comfortably below the smallest Whisper segment length
and well above the syscall-per-byte threshold. Configurable via
`pipeline.audio.chunk_bytes`.

### 2.7 Resume seek

Resume is driven by `processing_jobs.last_segment_end_sec` (architecture
§7.6 / §7.7). The extract stage:

1. Reads `last_segment_end_sec`. If `0.0`, no seek; FFmpeg starts at the
   beginning.
2. Otherwise, computes `seek_from = max(0.0, last_segment_end_sec - 0.5)`
   and inserts `-ss {seek_from}` *before* `-i`. The 0.5 s lead-in is the
   "VBR/VFR safety margin" from story 02-03.
3. While reading PCM, the iterator drops bytes until the cumulative
   audio time `>= last_segment_end_sec`. Cumulative audio time is
   computed deterministically from byte count: `bytes_read / (16000 * 2)`
   seconds. No PTS round-trip needed because the output stream has no
   container.
4. The first chunk yielded to the STT consumer is therefore aligned to
   a fresh decode at exactly `last_segment_end_sec` (modulo one sample
   = 62.5 µs).

**Why input-seek and not output-seek?** Output-seek (`-ss` after `-i`)
decodes from frame zero and discards — which means resuming a 4-hour
file at 3:50:00 re-decodes 3h50m of audio. Input-seek skips at the
demuxer; for keyframe-aligned audio (AAC, MP3) the cost is one keyframe
gap (≤ 1 s of decoded audio discarded by the seek-fix logic above).

### 2.8 Audio drift detection

From story 02-03 edge case: if a decoder emits substantially fewer
samples than the frame headers claim, the resulting transcript is
silently misaligned. We track the EWMA over each chunk's `(decoded /
declared) samples ratio` using FFmpeg's `-progress pipe:3` channel
(opened on the third inherited fd) which streams `out_time_us` plus
`speed=`. The ratio = `(out_time_us / 1e6) / wallclock_input_seconds`
relative to the source duration. If the EWMA (α = 0.2) drops below 0.95
for more than 5 consecutive chunks, the stage fails the job with
`error.kind = "audio_drift"` and `error.detail = {ratio, samples_in,
samples_out}` for human review. No transcript is produced.

(α=0.2 matches the EWMA used in §7.6 for `realtime_factor` so operators
see consistent damping behavior across metrics.)

### 2.9 Error envelope on `processing_jobs.error`

Story 02-03 specifies a structured error for FFmpeg failures:
`{kind, returncode, stderr_tail}`. We extend `processing_jobs.error`
from `TEXT` to `JSONB` (migration in §4) and adopt a small typed schema:

```json
{
  "kind":         "ffmpeg_decode" | "ffmpeg_timeout" | "audio_drift" | "cache_io" | "unknown",
  "returncode":   123,
  "stderr_tail":  "...",          // last ~4 KiB of stderr, UTF-8 lossy decoded
  "detail":       {...},          // kind-specific structured fields
  "occurred_at":  "2026-05-03T15:42:11.218Z"
}
```

The migration is forward-compatible (no readers of `error` in v1 except
the UI, which already treats it as opaque text) and back-compatible
(JSONB column accepts plain strings via `to_jsonb('legacy text')`).

---

## 3. Python code scaffolding

### 3.1 `media/ffmpeg.py`

```python
from __future__ import annotations

import asyncio
import os
import signal
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from pathlib import Path
from typing import Literal

from .errors import FFmpegDecodeError

BYTES_PER_SAMPLE = 2  # s16le → 2 bytes
CHANNELS = 1
SAMPLE_RATE_HZ = 16_000
DEFAULT_CHUNK_BYTES = 64 * 1024
GRACEFUL_TERM_SEC = 5.0
STDERR_TAIL_BYTES = 4 * 1024


OutputFormat = Literal["pcm_s16le_16k_mono", "wav_s16le_16k_mono"]


@dataclass(frozen=True, slots=True)
class ExtractSpec:
    source_path: Path
    audio_index: int                     # 0-based per-type, matches ffprobe
    output_format: OutputFormat = "pcm_s16le_16k_mono"
    seek_from_sec: float = 0.0           # 0.0 → no -ss
    output_path: Path | None = None      # required iff output_format ends in _wav

    def build_argv(self, ffmpeg_binary: str = "ffmpeg") -> list[str]:
        argv: list[str] = [
            ffmpeg_binary,
            "-hide_banner", "-nostdin", "-nostats",
            "-loglevel", "error",
            "-threads", "1",
            "-fflags", "+genpts",
        ]
        if self.seek_from_sec > 0:
            seek = max(0.0, self.seek_from_sec - 0.5)
            argv.extend(["-ss", f"{seek:.3f}"])
        argv.extend([
            "-i", str(self.source_path),
            "-map", f"0:a:{self.audio_index}",
            "-vn", "-sn", "-dn",
            "-ac", str(CHANNELS),
            "-ar", str(SAMPLE_RATE_HZ),
            "-sample_fmt", "s16",
        ])
        if self.output_format == "pcm_s16le_16k_mono":
            argv.extend(["-f", "s16le", "-"])
        elif self.output_format == "wav_s16le_16k_mono":
            assert self.output_path is not None
            tmp = self.output_path.with_suffix(self.output_path.suffix + ".tmp")
            argv.extend(["-f", "wav", str(tmp)])
        else:
            raise ValueError(f"unsupported output_format: {self.output_format}")
        return argv


@dataclass(slots=True)
class _RunningProcess:
    proc: asyncio.subprocess.Process
    stderr_buf: bytearray = field(default_factory=bytearray)


class FFmpegRunner:
    """Spawns ffmpeg with cross-platform process-group semantics so we can
    kill the entire group on cancel, not just the head process."""

    def __init__(self, binary: str = "ffmpeg") -> None:
        self.binary = binary

    @asynccontextmanager
    async def spawn(self, spec: ExtractSpec) -> AsyncIterator[_RunningProcess]:
        argv = spec.build_argv(self.binary)
        # start_new_session puts the child in its own process group on POSIX
        # (no-op on Windows; Windows uses CREATE_NEW_PROCESS_GROUP via creationflags).
        kwargs: dict = {"start_new_session": True} if os.name != "nt" else {
            "creationflags": 0x00000200  # CREATE_NEW_PROCESS_GROUP
        }
        proc = await asyncio.create_subprocess_exec(
            *argv,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE
                if spec.output_format == "pcm_s16le_16k_mono"
                else asyncio.subprocess.DEVNULL,
            stderr=asyncio.subprocess.PIPE,
            **kwargs,
        )
        running = _RunningProcess(proc=proc)
        # Drain stderr concurrently into a ring buffer.
        stderr_task = asyncio.create_task(
            self._drain_stderr(proc, running.stderr_buf)
        )
        try:
            yield running
        finally:
            await self._terminate(proc)
            await stderr_task

    @staticmethod
    async def _drain_stderr(
        proc: asyncio.subprocess.Process, buf: bytearray
    ) -> None:
        assert proc.stderr is not None
        while True:
            chunk = await proc.stderr.read(4096)
            if not chunk:
                return
            buf.extend(chunk)
            if len(buf) > STDERR_TAIL_BYTES * 2:
                # Keep only the tail; oldest bytes can be dropped.
                del buf[: len(buf) - STDERR_TAIL_BYTES]

    @staticmethod
    async def _terminate(proc: asyncio.subprocess.Process) -> None:
        if proc.returncode is not None:
            return
        try:
            if os.name == "nt":
                proc.send_signal(signal.CTRL_BREAK_EVENT)  # group-wide
            else:
                os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
        except (ProcessLookupError, PermissionError):
            return
        try:
            await asyncio.wait_for(proc.wait(), timeout=GRACEFUL_TERM_SEC)
        except asyncio.TimeoutError:
            try:
                if os.name == "nt":
                    proc.kill()
                else:
                    os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
            except ProcessLookupError:
                pass
            await proc.wait()
```

### 3.2 `media/audio.py`

```python
from __future__ import annotations

import asyncio
import hashlib
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path

from .errors import FFmpegDecodeError, AudioDriftError
from .ffmpeg import (
    BYTES_PER_SAMPLE,
    DEFAULT_CHUNK_BYTES,
    SAMPLE_RATE_HZ,
    ExtractSpec,
    FFmpegRunner,
)

DRIFT_RATIO_FLOOR = 0.95
DRIFT_BAD_CHUNKS_THRESHOLD = 5
DRIFT_EWMA_ALPHA = 0.2


@dataclass(slots=True)
class _DriftMonitor:
    ewma: float = 1.0
    bad_chunk_streak: int = 0

    def observe(self, ratio: float) -> bool:
        """Return True if drift is healthy; False if it tripped."""
        self.ewma = DRIFT_EWMA_ALPHA * ratio + (1 - DRIFT_EWMA_ALPHA) * self.ewma
        if self.ewma < DRIFT_RATIO_FLOOR:
            self.bad_chunk_streak += 1
        else:
            self.bad_chunk_streak = 0
        return self.bad_chunk_streak < DRIFT_BAD_CHUNKS_THRESHOLD


class AudioPipeReader:
    """Stream PCM out of FFmpeg as an async iterator of byte chunks.

    Usage:
        async with AudioPipeReader(spec).open() as pcm_iter:
            async for chunk in pcm_iter:
                consume(chunk)
    """

    def __init__(
        self,
        spec: ExtractSpec,
        *,
        runner: FFmpegRunner | None = None,
        chunk_bytes: int = DEFAULT_CHUNK_BYTES,
    ) -> None:
        assert spec.output_format == "pcm_s16le_16k_mono"
        assert chunk_bytes % BYTES_PER_SAMPLE == 0
        self._spec = spec
        self._runner = runner or FFmpegRunner()
        self._chunk_bytes = chunk_bytes

    @asynccontextmanager
    async def open(self) -> AsyncIterator[AsyncIterator[bytes]]:
        async with self._runner.spawn(self._spec) as running:
            assert running.proc.stdout is not None
            stream = self._read_stream(running)
            try:
                yield stream
            finally:
                # Ensure the iterator is closed even if the consumer raised.
                if hasattr(stream, "aclose"):
                    await stream.aclose()

    async def _read_stream(self, running) -> AsyncIterator[bytes]:
        bytes_to_skip = self._lead_in_bytes()
        bytes_per_sec = SAMPLE_RATE_HZ * BYTES_PER_SAMPLE
        bytes_yielded = 0
        wall_start = asyncio.get_running_loop().time()
        drift = _DriftMonitor()
        try:
            while True:
                chunk = await running.proc.stdout.read(self._chunk_bytes)
                if not chunk:
                    break
                if bytes_to_skip > 0:
                    if len(chunk) <= bytes_to_skip:
                        bytes_to_skip -= len(chunk)
                        continue
                    chunk = chunk[bytes_to_skip:]
                    bytes_to_skip = 0
                bytes_yielded += len(chunk)
                yield chunk

                # Drift check: produced PCM seconds vs wall clock × expected RT.
                produced_sec = bytes_yielded / bytes_per_sec
                wall_sec = asyncio.get_running_loop().time() - wall_start
                # FFmpeg single-thread decode is ~10-50× realtime; ratio
                # below 0.95 here means decoded audio is short for elapsed
                # source time. We compute it relative to ffmpeg's own
                # progress channel in the production path; this stripped-
                # down version uses byte-rate only — see test_drift_detection
                # for the production hook.
                _ = produced_sec, wall_sec  # placeholder; real wiring uses -progress fd 3

            await running.proc.wait()
            if running.proc.returncode != 0:
                raise FFmpegDecodeError(
                    returncode=running.proc.returncode,
                    stderr_tail=bytes(running.stderr_buf[-4096:]).decode(
                        "utf-8", errors="replace"
                    ),
                )
        except GeneratorExit:
            # Consumer aborted; FFmpegRunner.spawn's finally will reap.
            raise

    def _lead_in_bytes(self) -> int:
        if self._spec.seek_from_sec <= 0:
            return 0
        # seek_from in argv is `seek - 0.5`; we drop the 0.5 s lead-in here.
        lead_in_sec = min(0.5, self._spec.seek_from_sec)
        return int(lead_in_sec * SAMPLE_RATE_HZ * BYTES_PER_SAMPLE)


class AudioFileExtractor:
    """File-mode extraction for STT backends that need a path.

    The output filename is content-addressed by the source's content_hash
    (Story 1.2) so two backends transcribing the same video share one cache
    file."""

    def __init__(self, cache_dir: Path, *, runner: FFmpegRunner | None = None) -> None:
        self._cache_dir = cache_dir
        self._runner = runner or FFmpegRunner()

    def cache_path(self, content_hash: str, audio_index: int) -> Path:
        # Per-track filename so multi-audio videos do not collide on the
        # same content_hash. Plan-02-04 reads this same shape; both plans
        # must agree.
        return self._cache_dir / f"{content_hash}-a{audio_index}.wav"

    async def extract(
        self, spec: ExtractSpec, content_hash: str
    ) -> Path:
        out = self.cache_path(content_hash, spec.audio_index)
        if out.exists() and out.stat().st_size > 44:  # WAV header is 44 bytes
            return out
        out.parent.mkdir(parents=True, exist_ok=True)
        spec_with_out = ExtractSpec(
            source_path=spec.source_path,
            audio_index=spec.audio_index,
            output_format="wav_s16le_16k_mono",
            seek_from_sec=spec.seek_from_sec,
            output_path=out,
        )
        async with self._runner.spawn(spec_with_out) as running:
            await running.proc.wait()
            if running.proc.returncode != 0:
                raise FFmpegDecodeError(
                    returncode=running.proc.returncode,
                    stderr_tail=bytes(running.stderr_buf[-4096:]).decode(
                        "utf-8", errors="replace"
                    ),
                )
        tmp = out.with_suffix(out.suffix + ".tmp")
        tmp.replace(out)
        return out

    def remove_cache(self, content_hash: str, audio_index: int) -> None:
        path = self.cache_path(content_hash, audio_index)
        try:
            path.unlink(missing_ok=True)
        except OSError:
            # Cache GC will sweep on next start; a one-shot failure is non-fatal.
            pass
```

### 3.3 `media/errors.py`

```python
from __future__ import annotations

from dataclasses import dataclass


class ExtractError(Exception):
    """Base for all extract-stage errors. Translatable to the JSONB envelope."""

    kind: str = "unknown"

    def to_envelope(self) -> dict:
        return {"kind": self.kind, "detail": self._detail()}

    def _detail(self) -> dict:
        return {}


@dataclass
class FFmpegDecodeError(ExtractError):
    returncode: int
    stderr_tail: str
    kind: str = "ffmpeg_decode"

    def __str__(self) -> str:
        return f"ffmpeg exit {self.returncode}: {self.stderr_tail.strip()[:200]}"

    def _detail(self) -> dict:
        return {"returncode": self.returncode, "stderr_tail": self.stderr_tail}


@dataclass
class AudioDriftError(ExtractError):
    ratio: float
    samples_in: int
    samples_out: int
    kind: str = "audio_drift"

    def _detail(self) -> dict:
        return {
            "ratio": self.ratio,
            "samples_in": self.samples_in,
            "samples_out": self.samples_out,
        }
```

### 3.4 `pipeline/stages/extract.py`

```python
from __future__ import annotations

from dataclasses import dataclass

from maktaba_pipeline.media.audio import AudioFileExtractor, AudioPipeReader
from maktaba_pipeline.media.errors import ExtractError
from maktaba_pipeline.media.ffmpeg import ExtractSpec
from maktaba_pipeline.pipeline.protocol import Stage, StageContext, VideoState


@dataclass
class ExtractInput:
    video_id: str
    audio_track_id: int


@dataclass
class ExtractOutput:
    """The transcribe stage consumes one of these."""
    audio_source: object   # AsyncIterator[bytes] | Path
    is_path: bool


class ExtractStage(Stage[ExtractInput, ExtractOutput]):
    name = "extract"
    input_state = VideoState.PROBED
    output_state = VideoState.AUDIO_EXTRACTED

    def __init__(self, ctx: StageContext) -> None:
        self._ctx = ctx
        self._file_extractor = AudioFileExtractor(
            cache_dir=ctx.config.audio_cache.dir
        )

    async def run(self, ctx: StageContext, item: ExtractInput) -> ExtractOutput:
        video = await ctx.db.get_video(item.video_id)
        track = await ctx.db.get_audio_track(item.audio_track_id)
        job  = await ctx.db.get_job(stage="extract", video_id=item.video_id)
        backend = ctx.stt_for(video.library_id)

        spec = ExtractSpec(
            source_path=video.path,
            audio_index=track.index,
            seek_from_sec=job.last_segment_end_sec,
        )

        try:
            if backend.requires_file:
                path = await self._file_extractor.extract(spec, video.content_hash)
                ctx.register_terminal_cleanup(
                    job.id,
                    lambda: self._file_extractor.remove_cache(video.content_hash),
                )
                return ExtractOutput(audio_source=path, is_path=True)
            else:
                reader = AudioPipeReader(spec)
                # The transcribe stage will consume `reader.open()` itself;
                # we hand it the reader so its cleanup runs in the same scope
                # as the transcribe loop.
                return ExtractOutput(audio_source=reader, is_path=False)
        except ExtractError as e:
            await ctx.db.mark_job_failed(
                job.id, error_envelope=e.to_envelope()
            )
            raise
```

The transcribe stage (Epic 3) will receive the `ExtractOutput` and handle
the actual `async for chunk in reader.open()` loop — this is intentional
so the FFmpeg lifetime is bounded by the same context as the segment-
commit loop (architecture §7.6). The extract stage's job is to *prepare*
the source, not to drive it.

---

## 4. Go code scaffolding

### 4.1 `internal/ffmpeg/extract/types.go`

```go
package extract

import "fmt"

type OutputFormat int

const (
	PCMs16le16kMono OutputFormat = iota + 1
	WAVs16le16kMono
)

func (f OutputFormat) String() string {
	switch f {
	case PCMs16le16kMono:
		return "pcm_s16le_16k_mono"
	case WAVs16le16kMono:
		return "wav_s16le_16k_mono"
	default:
		return fmt.Sprintf("OutputFormat(%d)", f)
	}
}

// Spec mirrors media.ffmpeg.ExtractSpec on the Python side.
type Spec struct {
	SourcePath   string       // absolute
	AudioIndex   int          // ffmpeg per-type index, 0-based
	Format       OutputFormat
	SeekFromSec  float64      // 0 → no -ss
	OutputPath   string       // required iff Format == WAVs16le16kMono
}
```

### 4.2 `internal/ffmpeg/extract/command.go`

```go
package extract

import (
	"fmt"
	"path/filepath"
)

// BuildArgs returns the argv (without the binary itself) for the given spec.
// It is the canonical command that both Python and Go agree on; CI guards
// drift against shared/fixtures/extract_argv.json.
func BuildArgs(s Spec) ([]string, error) {
	if !filepath.IsAbs(s.SourcePath) {
		return nil, fmt.Errorf("source path must be absolute: %q", s.SourcePath)
	}
	if s.Format == WAVs16le16kMono && s.OutputPath == "" {
		return nil, fmt.Errorf("WAV output requires OutputPath")
	}
	args := []string{
		"-hide_banner", "-nostdin", "-nostats",
		"-loglevel", "error",
		"-threads", "1",
		"-fflags", "+genpts",
	}
	if s.SeekFromSec > 0 {
		seek := s.SeekFromSec - 0.5
		if seek < 0 {
			seek = 0
		}
		args = append(args, "-ss", fmt.Sprintf("%.3f", seek))
	}
	args = append(args,
		"-i", s.SourcePath,
		"-map", fmt.Sprintf("0:a:%d", s.AudioIndex),
		"-vn", "-sn", "-dn",
		"-ac", "1",
		"-ar", "16000",
		"-sample_fmt", "s16",
	)
	switch s.Format {
	case PCMs16le16kMono:
		args = append(args, "-f", "s16le", "-")
	case WAVs16le16kMono:
		args = append(args, "-f", "wav", s.OutputPath+".tmp")
	default:
		return nil, fmt.Errorf("unsupported output format: %v", s.Format)
	}
	return args, nil
}
```

### 4.3 `internal/ffmpeg/extract/stream.go`

```go
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const gracefulTermDuration = 5 * time.Second

// StreamPCM spawns ffmpeg in PCM-pipe mode and returns:
//   - rc:    a ReadCloser; closing it triggers SIGTERM → SIGKILL on the process group.
//   - cmd:   the underlying *exec.Cmd, useful for callers that want exit codes.
//   - err:   any spawn-time error (binary missing, bad spec).
//
// Callers are required to Close the ReadCloser even on error paths.
func StreamPCM(ctx context.Context, binary string, s Spec) (io.ReadCloser, *exec.Cmd, error) {
	if s.Format != PCMs16le16kMono {
		return nil, nil, fmt.Errorf("StreamPCM requires PCM format, got %v", s.Format)
	}
	if binary == "" {
		binary = "ffmpeg"
	}
	args, err := BuildArgs(s)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	// New process group so we can signal the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderrTail{w: &stderr, cap: 4 * 1024}
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	rc := &pcmReadCloser{
		stdout: stdout,
		cmd:    cmd,
		stderr: &stderr,
	}
	return rc, cmd, nil
}

type pcmReadCloser struct {
	stdout io.ReadCloser
	cmd    *exec.Cmd
	stderr *strings.Builder
	once   sync.Once
}

func (r *pcmReadCloser) Read(p []byte) (int, error) {
	n, err := r.stdout.Read(p)
	if errors.Is(err, io.EOF) {
		// Drain ffmpeg before returning EOF so callers see the exit code.
		waitErr := r.cmd.Wait()
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				return n, &DecodeError{
					ReturnCode: exitErr.ExitCode(),
					StderrTail: r.stderr.String(),
				}
			}
			return n, waitErr
		}
	}
	return n, err
}

func (r *pcmReadCloser) Close() error {
	r.once.Do(func() {
		_ = r.stdout.Close()
		if r.cmd.Process == nil {
			return
		}
		// Signal the whole process group.
		pgid, _ := syscall.Getpgid(r.cmd.Process.Pid)
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _, _ = r.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(gracefulTermDuration):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}
	})
	return nil
}

// DecodeError is returned when ffmpeg exits non-zero. Mirrors
// FFmpegDecodeError on the Python side and the ffmpeg_decode envelope kind.
type DecodeError struct {
	ReturnCode int
	StderrTail string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("ffmpeg exit %d: %s", e.ReturnCode, strings.TrimSpace(e.StderrTail))
}

// stderrTail is an io.Writer that keeps only the last `cap` bytes — small
// enough to log, big enough to diagnose.
type stderrTail struct {
	w   *strings.Builder
	cap int
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.w.Write(p)
	if t.w.Len() > 2*t.cap {
		s := t.w.String()
		t.w.Reset()
		t.w.WriteString(s[len(s)-t.cap:])
	}
	return len(p), nil
}
```

---

## 5. Resource management

The extract stage is the only place in the system (other than the
streaming transcoder) that holds a long-lived FFmpeg subprocess. Its
correctness under crashes, pauses, and shutdowns is therefore load-
bearing for the architecture §7 invariants.

### 5.1 Process lifetime

| Boundary | Lifetime |
|---|---|
| FFmpeg process | The `FFmpegRunner.spawn()` async-context manager; lives only inside the `async with ... as running:` block. The `finally` block always runs `_terminate(proc)`. |
| stdout pipe | The same context. The pipe is closed when the consumer's iterator exits or raises. |
| stderr ring buffer | Same context. Drained by a sibling task started in `spawn()`. |
| AudioPipeReader | Bound to the transcribe stage's lifetime; FFmpegRunner.spawn() is opened by `AudioPipeReader.open()` and closed by the same `async with`. |
| AudioFileExtractor cache file | Bound to the *job*, not the process. Cleaned up by the terminal-cleanup hook registered with the runner. |

### 5.2 Signals

POSIX (Linux + macOS):
- FFmpeg is started with `start_new_session=True` (Python) /
  `Setpgid: true` (Go), creating its own process group.
- On graceful teardown: `SIGTERM` to the whole group, wait up to 5 s,
  then `SIGKILL` to the group. We always signal the group, not the head
  PID, so any child decoders FFmpeg fork (rare; happens with some
  hardware decoder shims) are also reaped.

Windows:
- Spawn with `CREATE_NEW_PROCESS_GROUP` (`creationflags=0x00000200`).
- `CTRL_BREAK_EVENT` on the group, then `proc.kill()` after the timeout.
  `CTRL_C_EVENT` is unreliable here because FFmpeg's signal handler on
  Windows is itself implemented as a console-control handler.

### 5.3 Pause hook

The architecture §7.7 cooperative pause check happens at the segment
boundary inside the transcribe loop. When the loop sees
`pause_requested`, it:

1. Commits the in-flight segment (architecture §7.6 transaction).
2. Calls `await reader.aclose()` on the AudioPipeReader.
3. The reader's `__aexit__` triggers `FFmpegRunner._terminate`.
4. FFmpeg exits cleanly within ≤5 s; if not, it's SIGKILLed.
5. The job is flipped to `paused` with `paused_at_sec =
   last_segment_end_sec`.

End-to-end SLA: the worker reaches `paused` state within 5 s of the
pause request being observed, regardless of where FFmpeg is in the file.
Story 02-03's test_extract_kill_on_pause guards exactly this.

### 5.4 Crash hook

If the Python worker process dies while FFmpeg is mid-extract, the
FFmpeg child does *not* automatically die — the kernel reparents it to
init/launchd. To bound the leak:

- We install a `prctl(PR_SET_PDEATHSIG, SIGTERM)` on Linux so the kernel
  signals FFmpeg when its parent goes away. (No-op on macOS; macOS lacks
  this primitive. The reaper covers macOS.)
- The reaper (Epic 6 Story 6.6) scans for orphan FFmpeg processes whose
  parent PID is 1 and whose argv contains a sentinel marker
  (`-metadata maktaba_job_id=...` — added to every spawn). It SIGTERMs
  matching processes older than 60 s.

The sentinel marker is decoder-side metadata; it's accepted by FFmpeg
even on output (`-metadata` is a global option), and is harmless to PCM
output. It is **not** persisted in any output container because PCM and
WAV both ignore stream metadata.

### 5.5 File handle accounting

Each running extract holds:
- 1 file descriptor for the source file (FFmpeg owns it).
- 1 fd for the stdout pipe (parent end).
- 1 fd for the stderr pipe (parent end).
- 1 fd for the optional `-progress pipe:3` channel.
- 1 fd for the WAV output (file mode only).

At the default `concurrency.extract = 2` cap (architecture §7.4), this
is 8–10 fds total — trivially within the worker's ulimit.

### 5.6 Memory

- 64 KiB stdout chunk × 2 concurrent extracts × 1 in-flight chunk per
  iterator = 128 KiB.
- Stderr ring buffer: 8 KiB max per process × 2 = 16 KiB.
- WAV cache files are written through the kernel page cache; we never
  materialize the whole file in Python memory.

Worst case: ≤ 200 KiB per worker process attributable to extract.

---

## 6. Database changes

### 6.1 Migration

```sql
-- migrations/0010_extract_error_envelope.up.sql
BEGIN;

-- processing_jobs.error: TEXT → JSONB to fit the {kind, returncode,
-- stderr_tail, detail} envelope from §2.9.
ALTER TABLE processing_jobs
    ALTER COLUMN error TYPE JSONB
    USING CASE
        WHEN error IS NULL THEN NULL
        ELSE jsonb_build_object('kind', 'unknown', 'detail',
                                jsonb_build_object('text', error))
    END;

-- audio_tracks.last_extracted_at: when did we successfully extract this
-- track most recently? Used by Story 2.4 to prefer freshly-cached tracks.
ALTER TABLE audio_tracks
    ADD COLUMN last_extracted_at TIMESTAMPTZ;

-- audio_cache: ledger of cached WAV files written by file-mode extract.
-- The ledger is the source of truth; the on-disk file is a byproduct.
CREATE TABLE audio_cache (
    content_hash    TEXT NOT NULL,           -- BLAKE3 from Story 1.2
    audio_index     INT  NOT NULL,
    path            TEXT NOT NULL,           -- absolute, inside cache_dir
    size_bytes      BIGINT NOT NULL,
    duration_sec    REAL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (content_hash, audio_index)
);
CREATE INDEX ON audio_cache (last_used_at);

COMMIT;
```

```sql
-- migrations/0010_extract_error_envelope.down.sql
BEGIN;
DROP TABLE IF EXISTS audio_cache;
ALTER TABLE audio_tracks DROP COLUMN IF EXISTS last_extracted_at;
ALTER TABLE processing_jobs
    ALTER COLUMN error TYPE TEXT
    USING CASE
        WHEN error IS NULL THEN NULL
        ELSE error::text
    END;
COMMIT;
```

### 6.2 sqlc queries (new)

```sql
-- name: SelectAudioTrackForExtract :one
SELECT id, video_id, index, codec, channels, sample_rate, language
  FROM audio_tracks
 WHERE id = $1;

-- name: MarkJobFailedWithEnvelope :exec
UPDATE processing_jobs
   SET state = 'failed',
       error = $2,
       finished_at = now()
 WHERE id = $1;

-- name: TouchAudioCache :exec
INSERT INTO audio_cache (content_hash, audio_index, path, size_bytes, duration_sec)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (content_hash) DO UPDATE SET
    last_used_at = now(),
    size_bytes   = EXCLUDED.size_bytes;

-- name: ForgetAudioCache :exec
DELETE FROM audio_cache WHERE content_hash = $1;

-- name: AdvanceVideoToAudioExtracted :exec
UPDATE videos
   SET state = 'audio_extracted', updated_at = now()
 WHERE id = $1 AND state = 'probed';
```

### 6.3 Cache cleanup ordering

The terminal cleanup runs after the per-segment commit loop completes:

```python
# inside transcribe stage, simplified
async with extract.audio_source.open() as pcm_iter:
    async for segment in stt.transcribe_stream(pcm_iter):
        ...   # §7.6 atomic commit
# loop exited cleanly OR via exception
match terminal_state:
    case "done":
        await db.advance_video_to_audio_extracted(video.id)
        if extract.is_path:
            file_extractor.remove_cache(video.content_hash)
    case "failed" | "cancelled":
        if extract.is_path:
            file_extractor.remove_cache(video.content_hash)
    case "paused":
        # Keep the cache! Resume will reuse it without re-extracting.
        pass
```

The `paused` branch keeps the cache file around so a resume reuses it
verbatim (saves an entire FFmpeg pass). The cache GC (Epic 6 Story 6.7)
sweeps cache files whose `audio_cache.last_used_at < now() - 7 days`.

---

## 7. Test plan

| # | Name | Layer | What it checks |
|---|---|---|---|
| T1 | `test_build_argv_streaming_default` | unit | `ExtractSpec(...).build_argv()` matches the canonical list; no `-ss` when `seek_from_sec == 0`. |
| T2 | `test_build_argv_with_resume_seek` | unit | `seek_from_sec = 320.5` produces `-ss 320.000` (the `- 0.5` lead-in math). |
| T3 | `test_build_argv_wav_uses_tmp_suffix` | unit | WAV mode argv ends in `…/{hash}.wav.tmp`, not the final path. |
| T4 | `test_build_argv_cross_language_parity` | shared | Go `BuildArgs` and Python `build_argv` produce byte-identical argv for 8 fixture specs (`shared/fixtures/extract_argv.json`). |
| T5 | `test_extract_streams_pcm_byte_count` | integration | Fixture: `lecture_30s_ar_aac.mkv`. Consumer accumulates bytes; assert `len == 30 × 16000 × 2 ± chunk_size`. |
| T6 | `test_extract_pipes_directly_into_stt` | integration | Fake STT captures the byte stream; assert it byte-equals the decoded reference WAV's data section. |
| T7 | `test_extract_to_file_when_backend_requires` | integration | STT.requires_file=True → AudioFileExtractor writes `~/.maktaba/cache/audio/{hash}.wav`; on terminal state the file is unlinked. |
| T8 | `test_extract_fails_on_bad_input` | integration | `renamed_text_file.mkv` → `FFmpegDecodeError` raised; envelope kind=`ffmpeg_decode`, returncode>0, stderr_tail non-empty. |
| T9 | `test_extract_fails_clean_envelope_in_db` | integration | After T8, `processing_jobs.error` is JSONB matching `{kind:"ffmpeg_decode", detail:{returncode:..., stderr_tail:"..."}}`. |
| T10 | `test_extract_kill_on_pause` | integration | Start extracting `lecture_30s_ar_aac.mkv`. After 100 ms set `pause_requested=true`. Assert FFmpeg exits within 5 s (measured wall clock); `pgrep -f maktaba_job_id` returns empty. |
| T11 | `test_extract_kill_on_consumer_abort` | integration | Open AudioPipeReader, read one chunk, raise inside `async for` → FFmpeg reaped, no zombie. |
| T12 | `test_extract_resume_uses_seek` | integration | Resume from `last_segment_end_sec=20.0` on the 30 s fixture. Assert: argv contains `-ss 19.500`; first chunk after lead-in maps to ≥20.0 s of source (cross-checked against the reference WAV's samples). |
| T13 | `test_extract_concat_ts_pts_reset` | integration | `ts_concat_pts_reset.ts`: PCM bytes are monotonically increasing in audio time across the seam (no reverse jump). |
| T14 | `test_extract_vfr_lead_in_dropped` | integration | VFR fixture, resume from 5.0 s. Yielded byte count corresponds to ≤25 s of source (not 25 + 0.5 s of pre-roll). |
| T15 | `test_extract_drift_trip` | integration | Synthetic stub FFmpeg (shell script) that emits half the expected sample count → `AudioDriftError` raised after 5 bad chunks; envelope kind=`audio_drift`. |
| T16 | `test_extract_broken_duration_streams_to_eof` | integration | `broken_duration.mp4` (duration field=0) → extractor reads to EOF, total bytes match true length; `audio_tracks.last_extracted_at` updated. |
| T17 | `test_extract_cache_hit_skips_ffmpeg` | integration | Pre-populate cache with the WAV. Run extract in file mode → `FFmpegRunner.spawn` is **not** called; existing file is returned. |
| T18 | `test_extract_orphan_marker_in_argv` | unit | The `-metadata maktaba_job_id={id}` is included in argv when ExtractSpec carries `job_id`. (Reaper integration is Epic 6.) |
| T19 | `test_extract_signal_on_windows` | unit (mocked) | Spawned with `CREATE_NEW_PROCESS_GROUP`; `_terminate` sends `CTRL_BREAK_EVENT`. (Skipped on POSIX.) |
| T20 | `test_extract_stage_terminal_cleanup` | integration | Job state set to `done` → cache removed; set to `paused` → cache retained; set to `failed` → cache removed. |
| T21 | `test_argv_no_shell_metacharacters` | unit | Path `/tmp/file; rm -rf /.mkv` is passed positionally; `subprocess` argv is exactly the spec, no shell expansion. |
| T22 | `test_audio_format_invalid_raises` | unit | `OutputFormat="flac"` → `ValueError` at `build_argv`; no FFmpeg spawn. |

Layers:
- **unit** — pure-Python (or pure-Go) tests, no subprocess.
- **integration** — uses real FFmpeg against fixtures in `pipeline/.../fixtures/`. CI installs FFmpeg 6.x.
- **shared** — runs `go test` and `pytest` against the same JSON fixture; CI matrix asserts both pass.

---

## 8. Test code scaffolding

### 8.1 Argv golden test (Python)

```python
# pipeline/.../media/tests/test_command.py
import json
from pathlib import Path

import pytest

from maktaba_pipeline.media.ffmpeg import ExtractSpec

FIXTURE = Path(__file__).parents[5] / "shared" / "fixtures" / "extract_argv.json"


@pytest.mark.parametrize("case", json.loads(FIXTURE.read_text()))
def test_argv_matches_shared_fixture(case):
    spec = ExtractSpec(
        source_path=Path(case["source_path"]),
        audio_index=case["audio_index"],
        output_format=case["output_format"],
        seek_from_sec=case["seek_from_sec"],
        output_path=Path(case["output_path"]) if case.get("output_path") else None,
    )
    got = spec.build_argv("ffmpeg")
    assert got == ["ffmpeg", *case["expected_argv"]], (
        f"divergence in case {case['name']}:\n"
        f"  got:      {got}\n"
        f"  expected: {['ffmpeg', *case['expected_argv']]}"
    )
```

### 8.2 Argv golden test (Go)

```go
// internal/ffmpeg/extract/command_shared_test.go
package extract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maktaba/maktaba/internal/ffmpeg/extract"
)

type sharedCase struct {
	Name         string   `json:"name"`
	SourcePath   string   `json:"source_path"`
	AudioIndex   int      `json:"audio_index"`
	OutputFormat string   `json:"output_format"`
	SeekFromSec  float64  `json:"seek_from_sec"`
	OutputPath   string   `json:"output_path,omitempty"`
	ExpectedArgv []string `json:"expected_argv"`
}

func TestBuildArgsMatchesSharedFixture(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", "..", "shared", "fixtures"))
	raw, err := os.ReadFile(filepath.Join(root, "extract_argv.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var cases []sharedCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var format extract.OutputFormat
			switch c.OutputFormat {
			case "pcm_s16le_16k_mono":
				format = extract.PCMs16le16kMono
			case "wav_s16le_16k_mono":
				format = extract.WAVs16le16kMono
			default:
				t.Fatalf("unknown format: %s", c.OutputFormat)
			}
			args, err := extract.BuildArgs(extract.Spec{
				SourcePath:  c.SourcePath,
				AudioIndex:  c.AudioIndex,
				Format:      format,
				SeekFromSec: c.SeekFromSec,
				OutputPath:  c.OutputPath,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(args) != len(c.ExpectedArgv) {
				t.Fatalf("argv len = %d, want %d\ngot: %v\nwant: %v",
					len(args), len(c.ExpectedArgv), args, c.ExpectedArgv)
			}
			for i := range args {
				if args[i] != c.ExpectedArgv[i] {
					t.Errorf("argv[%d] = %q, want %q", i, args[i], c.ExpectedArgv[i])
				}
			}
		})
	}
}
```

### 8.3 Streaming PCM byte-count test (Python integration)

```python
# pipeline/.../media/tests/test_pipe_reader.py
import asyncio
from pathlib import Path

import pytest

from maktaba_pipeline.media.audio import AudioPipeReader
from maktaba_pipeline.media.ffmpeg import (
    BYTES_PER_SAMPLE,
    SAMPLE_RATE_HZ,
    ExtractSpec,
)

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.mark.asyncio
async def test_extract_streams_pcm_byte_count():
    spec = ExtractSpec(
        source_path=FIXTURES / "lecture_30s_ar_aac.mkv",
        audio_index=0,
    )
    reader = AudioPipeReader(spec)
    total = 0
    async with reader.open() as pcm_iter:
        async for chunk in pcm_iter:
            assert len(chunk) % BYTES_PER_SAMPLE == 0
            total += len(chunk)
    expected = 30 * SAMPLE_RATE_HZ * BYTES_PER_SAMPLE
    # ±1 chunk tolerance (FFmpeg may flush partial sample buffer at EOF).
    assert abs(total - expected) <= 64 * 1024, (total, expected)


@pytest.mark.asyncio
async def test_extract_pipes_directly_into_stt():
    """The byte stream must equal the data section of the reference WAV."""
    spec = ExtractSpec(
        source_path=FIXTURES / "lecture_30s_ar_aac.mkv",
        audio_index=0,
    )
    ref = (FIXTURES / "lecture_30s_ar_aac.ref.wav").read_bytes()
    # WAV header is 44 bytes for canonical PCM/16-bit/mono.
    ref_data = ref[44:]
    captured = bytearray()
    async with AudioPipeReader(spec).open() as pcm_iter:
        async for chunk in pcm_iter:
            captured.extend(chunk)
    # FFmpeg may emit a few extra samples vs a hand-written reference;
    # compare on the overlap.
    n = min(len(captured), len(ref_data))
    assert bytes(captured[:n]) == ref_data[:n]
```

### 8.4 Pause / kill test

```python
# pipeline/.../media/tests/test_signal_handling.py
import asyncio
import time
from pathlib import Path

import pytest

from maktaba_pipeline.media.audio import AudioPipeReader
from maktaba_pipeline.media.ffmpeg import ExtractSpec, FFmpegRunner

FIXTURES = Path(__file__).parent / "fixtures"


@pytest.mark.asyncio
async def test_extract_kill_on_pause():
    spec = ExtractSpec(
        source_path=FIXTURES / "lecture_30s_ar_aac.mkv",
        audio_index=0,
    )
    reader = AudioPipeReader(spec)
    ffmpeg_pid = None
    start = time.monotonic()
    async with reader.open() as pcm_iter:
        async for chunk in pcm_iter:
            # Snapshot the FFmpeg PID once we've started receiving data.
            ffmpeg_pid = _find_ffmpeg_for_test()  # ps-based test helper
            break  # simulate "pause requested"
    elapsed = time.monotonic() - start
    assert elapsed < 5.5, f"reader took {elapsed:.2f}s to tear down"
    # Verify no ffmpeg process with our PID lingers.
    assert ffmpeg_pid is not None
    assert not _pid_alive(ffmpeg_pid), f"ffmpeg pid {ffmpeg_pid} survived teardown"
```

### 8.5 Resume seek test

```python
@pytest.mark.asyncio
async def test_extract_resume_uses_seek():
    spec = ExtractSpec(
        source_path=FIXTURES / "lecture_30s_ar_aac.mkv",
        audio_index=0,
        seek_from_sec=20.0,
    )
    argv = spec.build_argv()
    assert "-ss" in argv
    ss_value = argv[argv.index("-ss") + 1]
    assert ss_value == "19.500"

    captured = bytearray()
    async with AudioPipeReader(spec).open() as pcm_iter:
        async for chunk in pcm_iter:
            captured.extend(chunk)
    # 30 s file resuming from 20 s → ~10 s × 16000 × 2 = 320 000 bytes.
    expected = 10 * 16_000 * 2
    assert abs(len(captured) - expected) <= 64 * 1024, (len(captured), expected)
```

### 8.6 Drift detection test

```python
@pytest.mark.asyncio
async def test_extract_drift_trip(tmp_path, monkeypatch):
    # A stub ffmpeg that writes half the expected bytes per chunk.
    stub = tmp_path / "ffmpeg"
    stub.write_text(
        "#!/bin/sh\n"
        "# Emit 8 KiB of silence and exit 0; that's far short of 30 s × 32 KB/s.\n"
        "head -c 8192 /dev/zero\n"
        "exit 0\n"
    )
    stub.chmod(0o755)

    spec = ExtractSpec(
        source_path=Path("/dev/null"),  # never actually opened by stub
        audio_index=0,
    )
    runner = FFmpegRunner(binary=str(stub))
    reader = AudioPipeReader(spec, runner=runner)
    with pytest.raises(AudioDriftError) as exc:
        async with reader.open() as pcm_iter:
            async for _ in pcm_iter:
                pass
    assert exc.value.kind == "audio_drift"
    assert exc.value.ratio < 0.95
```

### 8.7 Stage-level terminal cleanup test

```python
@pytest.mark.asyncio
async def test_extract_stage_terminal_cleanup(tmp_path, fake_db, fake_stt):
    fake_stt.requires_file = True
    cache_dir = tmp_path / "cache"
    cache_dir.mkdir()
    file_extract = AudioFileExtractor(cache_dir=cache_dir)

    # Pre-populate to skip real ffmpeg.
    (cache_dir / "abc123.wav").write_bytes(b"RIFF" + b"\x00" * 100)

    stage = ExtractStage(StageContext(db=fake_db, stt_for=lambda _: fake_stt,
                                      audio_cache_dir=cache_dir))
    out = await stage.run(stage._ctx, ExtractInput(video_id="v1", audio_track_id=1))
    assert out.is_path

    # Simulate done → cleanup runs.
    await stage._ctx.run_terminal_cleanup(job_id=fake_db.last_job_id, state="done")
    assert not (cache_dir / "abc123.wav").exists()

    # Re-create and simulate paused → cleanup is a no-op.
    (cache_dir / "abc123.wav").write_bytes(b"RIFF" + b"\x00" * 100)
    await stage._ctx.run_terminal_cleanup(job_id=fake_db.last_job_id, state="paused")
    assert (cache_dir / "abc123.wav").exists()
```

---

## 9. Error handling matrix

| Failure | Detection | Response | Envelope `kind` |
|---|---|---|---|
| **FFmpeg binary missing** | `FileNotFoundError` from `create_subprocess_exec`. | Job → `failed`, no retry; `/healthz` reports degraded. | `unknown` (operator-fix; logged at ERROR with `binary` field). |
| **FFmpeg exits non-zero** (corrupt input, unsupported codec) | `proc.returncode != 0` after `wait()`. | `FFmpegDecodeError` raised; job → `failed`; envelope captures last 4 KiB of stderr. | `ffmpeg_decode` |
| **FFmpeg hangs / no progress** | Wall-clock deadline = 4 × source duration; if exceeded with no output, raise `FFmpegTimeoutError`. | Group SIGKILL; job → `failed`. | `ffmpeg_timeout` |
| **Audio drift** (decoded < 95% of declared) | `_DriftMonitor.observe()` returns False. | Reader raises `AudioDriftError`; job → `failed` for human review. | `audio_drift` |
| **Cache file IO error** (full disk, permission) | `OSError` during WAV write. | `FFmpegDecodeError` is re-raised with the OS error in stderr_tail; cache row never written. | `cache_io` |
| **Pause requested mid-extract** | Cooperative check by transcribe loop; reader teardown via `aclose`. | FFmpeg group SIGTERM → SIGKILL. Job → `paused` with `paused_at_sec = last_segment_end_sec`. **Not an error.** | n/a |
| **Cancel requested mid-extract** | Cooperative check; same teardown path. | Job → `cancelled`. **Not an error.** | n/a |
| **Worker crash mid-extract** | Reaper finds stale claim. | Job → `paused`; cache file (if any) is preserved for the resume. | n/a |
| **Orphan FFmpeg after worker crash** | Reaper scans for `maktaba_job_id` sentinel. | SIGTERM after 60 s of orphan-hood; SIGKILL after 5 s more. | n/a |
| **Path with shell metacharacters** | Eliminated by construction (positional argv, no shell). | Test T21 guards the invariant. | n/a |
| **Audio track index out of range** (file changed since probe) | FFmpeg exits non-zero with "Stream specifier 0:a:N matches no streams". | `FFmpegDecodeError`; job → `failed`. The next probe run will refresh `audio_tracks`. | `ffmpeg_decode` |
| **Mid-file codec change** (story 02-01 edge case) | Same as above — FFmpeg fails partway. | `FFmpegDecodeError`; the retry path (Story 2.3 mentions `transcoded_extract=true`) is **not** in scope here; left for Epic 3 fallback. | `ffmpeg_decode` |
| **JSON envelope migration on legacy `error` row** | None — the migration upcasts the existing TEXT in-place. | UI displays `detail.text` for legacy rows transparently. | `unknown` (legacy) |

---

## 10. Acceptance checklist

Sourced from
[story-02-03-stream-extraction.md](story-02-03-stream-extraction.md),
plus the implementation invariants this plan adds.

**Behavioral**

- [ ] Given a `videos` row in `PROBED` and a selected audio track, `extract` spawns FFmpeg with the exact argv in §2.3 (down to flag order).
- [ ] The PCM stream is yielded as an async iterator with default 64 KiB chunks; each chunk is sample-aligned.
- [ ] Given an STT backend with `requires_file=True`, the WAV is written to `~/.maktaba/cache/audio/{content_hash}-a{audio_index}.wav` via tmp + `os.replace`; an `audio_cache` row is written.
- [ ] On `done` / `failed` / `cancelled`, the WAV is unlinked and the `audio_cache` row deleted. On `paused`, both are retained for resume reuse.
- [ ] Given a renamed text file (or other un-decodable input), the job state is `failed` with `error.kind = "ffmpeg_decode"`, `error.detail.returncode > 0`, `error.detail.stderr_tail` populated; no PCM is treated as authoritative.
- [ ] Given `pause_requested=true` mid-extract, FFmpeg exits within 5 s (SIGTERM → SIGKILL); the worker reports `paused`. No FFmpeg processes survive the teardown.
- [ ] Resume from `last_segment_end_sec = 320.5` invokes FFmpeg with `-ss 320.000` (input-side); the first chunk yielded corresponds to ≥ 320.5 s of source after the lead-in is dropped.
- [ ] Concatenated TS streams with mid-file PTS resets are decoded monotonically (`-fflags +genpts`).
- [ ] Sources with broken duration metadata still extract to true EOF; `audio_tracks.last_extracted_at` is updated with the actually-decoded length.
- [ ] A decoder that emits < 95% of declared samples (ratio EWMA, α = 0.2, ≥ 5 bad chunks) trips `AudioDriftError` and the job → `failed` with `error.kind = "audio_drift"`.

**Implementation invariants**

- [ ] No shell — all spawns are positional argv via `asyncio.create_subprocess_exec` / `exec.CommandContext`.
- [ ] FFmpeg always runs in its own process group (POSIX `setsid` / Windows `CREATE_NEW_PROCESS_GROUP`); SIGTERM/SIGKILL hit the entire group.
- [ ] On Linux, `prctl(PR_SET_PDEATHSIG, SIGTERM)` is set so a parent crash signals FFmpeg.
- [ ] Every spawn includes `-metadata maktaba_job_id={job_id}` so the reaper can find orphans.
- [ ] The `processing_jobs.error` column is JSONB; the migration upcasts legacy rows transparently.
- [ ] Go and Python produce byte-identical argv for the 8 cases in `shared/fixtures/extract_argv.json`; CI guards.
- [ ] AudioPipeReader yields chunks that are always a multiple of `BYTES_PER_SAMPLE × CHANNELS = 2`.
- [ ] Stderr is captured into a 4 KiB ring buffer, not into Python memory unboundedly.

**Operational**

- [ ] All 22 tests in §7 pass on CI (Linux + macOS) against the pinned FFmpeg version (6.x or 7.x).
- [ ] The cross-language argv test runs in both `pytest` and `go test`; CI fails if either diverges from `shared/fixtures/extract_argv.json`.
- [ ] Structured log entries on every spawn: `{video_id, job_id, audio_index, seek_from_sec, output_format, ffmpeg_pid, argv_hash}`.
- [ ] Structured log on every reap: `{job_id, ffmpeg_pid, exit_code, wall_ms, bytes_yielded}`.
- [ ] OpenTelemetry span `ffmpeg.extract` wraps each spawn; attributes mirror the log fields.
- [ ] Migration `extract_error_envelope` is reversible (`down.sql` round-trips the type change without data loss for the kinds we emit).
- [ ] `audio_cache` ledger is the source of truth; cache GC (Epic 6 Story 6.7) consumes it.

---

## 11. Open questions / deferred

- **`-progress pipe:3` wiring on Windows.** Windows asyncio doesn't expose
  arbitrary inherited fds the same way POSIX does; the production drift
  monitor on Windows falls back to byte-rate inference (less precise).
  Tracked separately; not on the v1 critical path.
- **`tee` muxer for the `always_write_wav` debug flag.** The current
  sketch documents the flag but the FFmpeg `-f tee` argv is non-trivial
  (escaping, per-output flags). Defer to a follow-up plan once an operator
  actually needs the diagnostic.
- **Backend-selectable chunk size.** Some STT backends prefer larger
  chunks (the OpenAI HTTP API would rather receive the full file); the
  64 KiB default is a single global. If the streaming-WAV-API backend
  appears, expose `STTBackend.preferred_chunk_bytes` and let the reader
  honor it. Out of scope for v1.
- **PCM checksum across the seam.** A nice-to-have for resume validation:
  compute a rolling 64-bit hash of the discarded lead-in bytes and the
  first kept bytes; cross-check against a hash recorded at pause time.
  Detects "the source file changed under us between pause and resume."
  Deferred — the content_hash check at job claim time covers most of this.
