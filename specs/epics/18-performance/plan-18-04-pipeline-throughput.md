# Implementation Plan — Story 18.4 Pipeline Throughput Targets

> Companion to [story-18-04-pipeline-throughput.md](story-18-04-pipeline-throughput.md).
> Transcribe ≥ 4× realtime, indexing ≥ 50 seg/s, thumbs ≤ 90 s/60 min,
> with `pipeline_stage_duration_seconds` histograms.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Owner | `pipeline/maktaba_pipeline/` (Python 3.12). |
| Stages | `transcribe`, `index`, `thumbnail`, `embed`, `diarize`. |
| Telemetry | Prometheus client; one histogram per stage `pipeline_stage_duration_seconds{stage}`. |
| Budgets registered | `shared/perf_budgets.yaml` references; assertions live in the perf harness. |
| Out of scope | Worker-pool architecture (Epic 6 job-queue); model selection (Epic 3 transcription). |

## 1. Project layout

```
pipeline/
├── maktaba_pipeline/
│   ├── stages/
│   │   ├── __init__.py
│   │   ├── transcribe.py       # Whisper MLX/faster-whisper
│   │   ├── index.py            # FTS upsert + Chroma upsert
│   │   ├── thumbnail.py        # FFmpeg sprite + posters
│   │   ├── embed.py
│   │   └── diarize.py
│   ├── metrics.py              # Prometheus histograms
│   ├── runner.py               # one-shot stage CLI for benchmarks
│   └── benchmarks/
│       ├── bench_transcribe.py
│       ├── bench_index.py
│       └── bench_thumbnail.py
├── tests/
│   ├── perf/
│   │   ├── test_throughput.py
│   │   └── fixtures.py
│   └── fixtures/
│       ├── arabic-20min.wav
│       ├── film-90min.mkv     # synthetic colorbar with audio track
│       └── multi-track-5.mkv
└── pyproject.toml
```

## 2. Stage histogram

```python
# pipeline/maktaba_pipeline/metrics.py
from prometheus_client import Histogram

PIPELINE_STAGE_DURATION = Histogram(
    "pipeline_stage_duration_seconds",
    "Per-stage wall-clock duration",
    labelnames=("stage",),
    buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200),
)

class StageTimer:
    def __init__(self, stage: str): self.stage = stage
    def __enter__(self):
        self.t = time.perf_counter()
        return self
    def __exit__(self, *exc):
        PIPELINE_STAGE_DURATION.labels(self.stage).observe(time.perf_counter() - self.t)
```

Usage:

```python
with StageTimer("transcribe"):
    out = transcribe(audio_path)
```

## 3. Transcribe stage

```python
# stages/transcribe.py
class Transcriber:
    def __init__(self, cfg):
        self.backend = self._select_backend(cfg)         # mlx | faster | api | cpu
        self.model = self.backend.load(cfg.model_id)

    def run(self, audio_path: Path) -> TranscriptResult:
        with StageTimer("transcribe"):
            try:
                return self.backend.transcribe(self.model, audio_path)
            except MLXInitError:
                fallback = FasterWhisperBackend()
                self.backend = fallback                  # remember for next call
                self.model = fallback.load(self.model.id)
                metrics.PIPELINE_FALLBACK.labels("mlx_to_faster").inc()
                return fallback.transcribe(self.model, audio_path)
```

Backend selection (priority): `mlx` if `arch=arm64` and `mlx-whisper` importable → `faster-whisper` GPU → `faster-whisper` CPU → API.

## 4. Index stage (≥ 50 seg/s)

```python
# stages/index.py
def index_segments(db, chroma, video_id: str, segments: list[Segment]) -> None:
    with StageTimer("index"):
        # FTS: single multi-row INSERT
        db.execute_values(
            "INSERT INTO segments(...) VALUES %s",
            [(s.id, s.video_id, s.start, s.end, s.text) for s in segments],
            page_size=500,
        )
        # Chroma: batched upsert
        chroma.upsert(
            ids=[s.id for s in segments],
            embeddings=[s.embedding for s in segments],
            metadatas=[s.metadata for s in segments],
            documents=[s.text for s in segments],
        )
```

Batch size 500 fits comfortably under the per-batch 200 ms target → ≥ 50 seg/s.

## 5. Thumbnail stage (≤ 90 s for 60 min)

```python
# stages/thumbnail.py
def make_sprites(video_path: Path, out_dir: Path, *, fps_pick: float = 0.1) -> SpriteSet:
    """Sample 1 frame every 10s; assemble 10x10 grid; output 4 posters."""
    with StageTimer("thumbnail"):
        # Single ffmpeg call dumps all stills + 4 posters
        sprite_glob = out_dir / "frame_%05d.jpg"
        subprocess.run([
            "ffmpeg", "-y", "-i", str(video_path),
            "-vf", f"fps={fps_pick},scale=160:90",
            "-q:v", "5",
            str(sprite_glob),
        ], check=True)
        composite = compose_grid(sorted(out_dir.glob("frame_*.jpg")))
        posters   = [extract_poster(video_path, t, out_dir) for t in [60, 600, 1800, 3000]]
        return SpriteSet(grid=composite, posters=posters)
```

Single FFmpeg invocation amortizes the decoder warm-up; 60 min × 0.1 fps = 360 frames; ~50 s on Apple-Silicon hardware-decoded H.264.

## 6. Benchmark harness

```python
# pipeline/maktaba_pipeline/benchmarks/bench_transcribe.py
def main():
    audio = Path(sys.argv[1])
    duration = librosa.get_duration(path=audio)
    t0 = time.perf_counter()
    Transcriber.from_config(load_cfg()).run(audio)
    elapsed = time.perf_counter() - t0
    rt = duration / elapsed
    print(json.dumps({"duration_s": duration, "elapsed_s": elapsed, "rt_multiple": rt}))
```

Make target:

```makefile
bench-pipeline:
	python -m maktaba_pipeline.benchmarks.bench_transcribe pipeline/tests/fixtures/arabic-20min.wav
	python -m maktaba_pipeline.benchmarks.bench_index      pipeline/tests/fixtures/segments-1k.json
	python -m maktaba_pipeline.benchmarks.bench_thumbnail  pipeline/tests/fixtures/film-90min.mkv
```

## 7. Test cases

### TC1 — Per-stage benchmark
`tests/perf/test_throughput.py`:

```python
@pytest.mark.perf
def test_transcribe_4x_realtime_arabic(arabic_20min):
    duration_s = 20 * 60
    with timer() as t: transcribe(arabic_20min)
    rt = duration_s / t.elapsed
    assert rt >= 4.0, f"transcribe rt={rt:.2f}, expected >= 4.0"

@pytest.mark.perf
def test_index_50_segs_per_sec(segments_1k):
    with timer() as t: index_segments(db, chroma, "v1", segments_1k)
    rate = len(segments_1k) / t.elapsed
    assert rate >= 50, f"index rate={rate:.1f} seg/s"

@pytest.mark.perf
def test_thumbnail_under_90s(film_90min):
    with timer() as t: make_sprites(film_90min, tmp_path)
    assert t.elapsed <= 90, f"thumbnail took {t.elapsed:.1f}s"
```

### TC2 — End-to-end
Single 60-minute Arabic lecture: enqueue, wait until READY. Assert wall-clock ≤ 20 min.

### TC3 — Backpressure
Set `concurrency.transcribe = 1`. Enqueue 10 hours of audio. Assert queue drains in ≤ 2.5 h, no worker exceeds its `worker_timeout_s`. Captured via job state transitions in DB and `pipeline_worker_active_seconds` histogram.

### EC1 — Mixed-language drop
Fixture `arabic-english-codeswitch-20min.wav` (Arabic with English proper nouns). Assert rt ≥ 3.2 (4.0 × 0.8). Below 3.2 fails.

### EC2 — Very short clip
30 s clip. RT-multiple meaningless; assert wall-clock ≤ 60 s.

### EC3 — GPU fallback
Set `MAKTABA_FORCE_MLX_FAILURE=1` env var. Run transcribe; assert: stage logs `mlx → faster-whisper fallback`, `pipeline_fallback_total{from_to="mlx_to_faster"}` == 1, transcribe completes (relaxed budget).

## 8. Edge case implementation

```python
# stages/transcribe.py — fallback hook
import os
def _select_backend(cfg) -> Backend:
    if os.getenv("MAKTABA_FORCE_MLX_FAILURE"):
        raise MLXInitError("forced by env")    # tested in EC3
    if platform.machine() == "arm64" and importlib.util.find_spec("mlx_whisper"):
        return MLXBackend()
    if importlib.util.find_spec("faster_whisper"):
        return FasterWhisperBackend()
    return CPUWhisperBackend()
```

## 9. Configuration

```yaml
# pipeline/config.yaml
concurrency:
  transcribe: 1
  index: 4
  thumbnail: 2
  embed: 2

worker_timeout_s:
  transcribe: 7200
  index: 600
  thumbnail: 600
  embed: 600

models:
  transcribe: "whisper-large-v3"
  embed: "intfloat/multilingual-e5-large"
```

## 10. Metrics summary

| Metric | Type | Notes |
|---|---|---|
| `pipeline_stage_duration_seconds{stage}` | histogram | per Story AC4. |
| `pipeline_throughput_ratio{stage}` | gauge | rolling 1-h. |
| `pipeline_worker_active_seconds{worker}` | histogram | for backpressure. |
| `pipeline_fallback_total{from_to}` | counter | EC3. |

## 11. Dependencies

- Epic 3 (transcription, model picker).
- Epic 5 (FTS / Chroma schemas).
- Story 18.1 (budgets file lists these throughputs as "stage targets").
- Story 21.2 (metrics).
