# Hardware: what Maktaba needs under different loads

This page says what a Maktaba home (and a worker machine) needs, for small to large libraries,
on macOS, Windows and Linux. Every number comes from one of three places, and says which:

- **Measured**: read on the live home on 2026-11-03 (Maktaba v0.3.62), from its database
  (`processing_jobs`, `health_samples`, `workers`), `top`, `ps`, `vm_stat`, `ioreg` and `du`.
  All read-only.
- **Code**: a value or a measurement written in the source (file named).
- **Estimate**: worked out from the two above. Each one says how. Treat it as a starting point,
  not a promise.

Where we have no number, the page says **not measured**.

Why this care: an earlier estimate said a backlog of about 45,000 videos would take "a day or
two" to transcribe. The measured answer is about two weeks. Transcription of a backlog is
the job that decides the hardware, and it is easy to get wrong by ten times.

---

## 1. What uses what

Every step of the work is a **stage** with its own queue. Each stage takes a slot in a **pool**
(`worker_pools`, `pipeline/src/maktaba_pipeline/engine/graph.py` `POOLS`). "Global" pools
count across the whole cluster; "per worker" pools count on each worker process.

| Pool | Scope | Live size | Default |
|---|---|---|---|
| `gpu` (transcription) | per worker | **2** | 32 |
| `cpu` | per worker | no cap | no cap |
| `nas_read` | global | 2 | 2 |
| `nas_write` | global | 4 | 4 |
| `network_plex` | global | 2 | 2 |
| `network_tv` (dizqueTV) | global | 1 | 1 |
| `network_yt` (YouTube) | global | 1 | 1 |
| `lookup` (LLM and encyclopedia) | global | 1 | 1 |
| `reports`, `audits`, `repairs`, `convert` | global | 1 each | 1 each |

The `gpu` pool's size is the **batch**: the jobs of one transcription worker share one batched
Whisper, and each GPU pass decodes one 30-second window of every file in flight
(`stt/batched.py`).

### Stage groups

Times are the median and mean of `finished_at − started_at` over the last 3 days (measured).

| Group | Stages | Pool | CPU | GPU | Memory | Disk | Network |
|---|---|---|---|---|---|---|---|
| **Transcription** | `transcribe` | gpu | low (each GPU worker ~35–50% of one core) | **the bottleneck**: 89–98% busy | **highest**: 10–11 GB footprint per GPU worker | models ~6 GB | reads each file from the NAS |
| Search index | `index` | cpu | embeddings on the CPU (multilingual-e5-small) | none | 3.4 GB footprint (one worker) | DB: the vectors (44 GB live) | DB only |
| Speech follow-ups | `subtitle_gen`, `chapter_infer`, `topic_assign` | cpu / nas_write | light (median 0.2–0.7 s) | none | small | subtitles beside the video | NAS writes |
| Media reads | `probe`, `content_hash`, `thumb_hash`, `fingerprint`, `detect`, `scan`, `extract` | nas_read / cpu | ffmpeg / hashing; `detect` median 40 s, mean 177 s | none | small | — | NAS reads |
| Thumbnails | `thumbnail`, `thumb_small`, `chapter_thumbs` | nas_read / cpu | ffmpeg frame grabs | none | small | ~150 KB a video (below) | NAS reads |
| LLM checks | `tag`, `check_tags`, `check_clips`, `display_title` | lookup / cpu | — | the LLM's, while loaded | the LLM's (qwen2.5:7b, 4.4 GB on disk) | model | — |
| Channels | `plan_channels`, `write_channels`, `tv_stage`, `tv_swap`, `tv_gc` | cpu | `plan_channels` median 182 s; `write_channels` 354 s | none | not measured | DB (channel plans ~1.7 GB) | — |
| Plex, YouTube | `plex_*`, `youtube_*` | network_* | light | none | small | — | internet / Plex |
| **Live TV** | the streaming role, not a stage | — | one ffmpeg per **watched** channel | VideoToolbox / NVENC | not measured | stream cache | **10 Mbps out per watched channel** |

**Live TV encoder CPU: not measured.** A channel only encodes while someone watches it, and no
channel was being watched during the measurement (the most on air at once in the last 24 hours
was 5, from `health_samples`). What the code says: each watched channel runs one ffmpeg, 1080p30
H.264 at `video_kbps` (10,000 on live), with a watermark overlay and speechnorm audio.

---

## 2. Measured reference: the live home

**Machine:** Mac Studio, M3 Ultra (28 CPU cores, 60 GPU cores, 96 GB unified memory). Media on a
NAS over NFS. Maktaba v0.3.62. A home that is **also a dev machine** (section 6).

### Library

| | Measured |
|---|---|
| Videos probed | 148,592 (27 TB of media, 36,871 hours; mean 14.9 min a video) |
| Transcribed | 49,993 videos, 22,663 audio-hours |
| Waiting to transcribe | 98,598 videos, **14,206 audio-hours** (mean 8.6 min a video) |

### Transcription

- **Engine:** `mlx-community/whisper-large-v3-turbo` through the batched Whisper (`mlx-batched`),
  then the **second listen** (`CohereLabs/cohere-transcribe-arabic-07-2026`) on every
  transcript (all 3,245 transcripts of the last 24 hours have it).
- **Workers:** 2 transcription workers (`stt-1`, `stt-2`), `gpu` pool 2 each, so **4 files at
  once**. The default pool of 32 ran out of memory on this 96 GB Mac.
- **Plan in use** (`[transcription]` in `server.toml`, the 96 GB tier of
  `cmd/maktaba-server/internal/supervisor/stt.go`): 2 workers, `gpu_gb` 20, `gpu_cache_gb` 2,
  `audio_hours` 16, `min_free_pct` 25.

| | Last 24 hours | Last 3 days |
|---|---|---|
| Jobs done | 3,245 | 7,017 |
| Audio done | **1,125 hours** | 3,215 hours |
| Job time, median / mean | 50 s / 106 s | 77 s / 148 s |
| Speed of one job (its own `rtf`), median | 18× real time | 17× |
| Speed of the whole home | **~47× real time** (1,125 h ÷ 24 h) | ~45× |

Per day over the last 10 days: 572 to 2,870 videos, 623 to 1,176 audio-hours. Videos per day
swing with their length; **audio-hours per day are the steady number** (about 900–1,180 on a
full day).

**The queue:** 14,206 audio-hours ÷ ~1,121 audio-hours a day = **about 13 days**.

### GPU and memory while transcribing

| | Measured |
|---|---|
| GPU busy (`ioreg -c IOAccelerator`) | Device 89–98%, Renderer 85–98% |
| GPU memory allocated / in use | 33–39 GB / 9–15.5 GB (it moves with the files in flight) |
| Per transcription worker (MLX, `health_samples`, 24 h) | active 6.5 GB on average (max 9.2), **peak 19–24 GB** |
| Pipeline processes (`top` footprint) | 2 GPU workers 10–11 GB each; index worker 3.4 GB; main worker 2.7–3.2 GB |
| Postgres | two processes of about 0.4–1 GB each, the rest small |
| `maktaba-api` | 230 MB RSS |
| Whole machine | ~1 GB free, 22–23 GB compressed, 7.9 of 9.2 GB swap used |
| CPU | 72% idle at a load average of ~30; 20–24% busy on average over 24 h |

**The GPU's compute is the bottleneck, not the pool size.** At 4 files in flight the GPU is
already near fully busy, while the CPU is mostly idle.

### Disk

| Where | What | Size |
|---|---|---|
| `/Volumes/Big/Maktaba/server/postgres` | the database (`pg_database_size` 64 GB) | 66 GB |
| └ of which `segment_embeddings` | search vectors (halfvec 384, 23.5 M rows; HNSW index 22 GB) | 44 GB |
| └ of which `transcript_segments` | the transcripts' text | 9.8 GB |
| `/Volumes/Big/Maktaba/backups` | database dumps (~17–23 GB each, 12 of them) | 235 GB |
| `/Volumes/Big/Maktaba/server/models` | Whisper turbo 1.5 GB, Cohere 3.8 GB, e5-small 0.47 GB, Ollama 4.4 GB, CAMeL 39 MB | 10 GB |
| `/Volumes/Big/Maktaba/server/venv` | the pipeline's Python | 1.9 GB |
| `/Volumes/Big/Maktaba/server/updates` | downloaded releases | 3.0 GB |
| `/Volumes/Big/Maktaba/server/cache` | streaming cache | 0.8 GB |
| `~/.maktaba/cache/thumbnails` | 145,122 videos' thumbnails | 22 GB (~150 KB a video) |
| `~/.maktaba` (rest) | backups 14 GB, import 2.7 GB, cluster 0.9 GB, logs 0.35 GB, archive 0.2 GB | ~18 GB |

The data disk is 1.8 TB with 919 GB used; Maktaba's part of it is about 320 GB (the rest is the
owner's other files). The media itself (27 TB) stays on the NAS.

**Database growth (estimate):** 64 GB for 22,663 transcribed hours is about **2.8 GB per 1,000
audio-hours transcribed** (whole database ÷ transcribed hours; ~2.4 GB of it is transcripts and
their vectors). Transcribing the rest of this library (36,871 h in all) would take it to about
100 GB, assuming it grows in line.

### Settings on live

- `rules.tv_output` = `{video_kbps: 10000, max_active_channels: 28}`; 28 channels.
- LLM: bundled Ollama, `qwen2.5:7b` (4.4 GB on disk), not loaded while idle; `lookup` pool 1.
- Embeddings: `intfloat/multilingual-e5-small` (470 MB).

### The cluster on live

A Windows PC has joined as a full node (`windows-pc`, Windows 11, 20 logical CPUs, 128 GB RAM,
an NVIDIA RTX 2080 SUPER with 8 GB). It pulls 33 of the "any machine" stages. In the last 10
days every finished job, every transcription included, was done by the home's own workers.

On v0.3.62 its capability report said `"gpus": []` and listed no `stt` extra. **That was a
detection bug, not the PC** (fixed in the release after v0.3.62):

- The GPU was looked for only through the `pynvml` package, which no install ever brings, and
  every failure was swallowed. The report now asks the NVIDIA driver itself (NVML, through
  `nvml.dll`), then `nvidia-smi` (on `PATH`, then `System32` and `NVIDIA Corporation\NVSMI`),
  and reports each GPU's name, memory, compute capability and driver, or why it found none
  (`gpu_problem`, also in the log).
- The `stt` extra counted as installed only when MLX-Whisper (the Mac's engine) was there. On
  Windows and Linux the engine is CTranslate2 (faster-whisper), which the app's environment
  has always installed: every pin of the release's constraints goes in, the `stt` extra's
  `faster-whisper`, `ctranslate2` and `nvidia-cublas-cu12` with them. The report now says which
  engine a machine has (`stt_backend`: `mlx`, `ct2-cuda` or `ct2-cpu`).

The report was only the visible part. The PC still did not transcribe, for a reason the report
did not show: the **second listen** is on by default, and while it is on only the machines that
run it transcribe. On Windows the second listen needed a GPU of 9.5 GiB or more beside Whisper
(Windows' driver keeps ~1 GiB of the card from a process, and the 8 GB card has 5.8 GiB left once
Whisper is loaded, short of the 6.4 GiB Cohere needs: measured on this PC, `stt/cohere_cuda.py`).
So the PC left transcription to the Mac, and its stage table said only "cannot run here". Since
the second listen became a stage of its own, the two take turns on that card (below).

How a Windows NVIDIA worker transcribes, from that release on:

- **Admin > Machines** says, per machine: "Transcription: ready on NVIDIA GeForce RTX 2080
  SUPER", "installing…" while an update builds its environment, or the exact reason it cannot
  (the pipeline's own words: a missing engine, the share), and a line of its own for the second
  listen.
- **The second listen is a stage of its own** (`second_listen`, after `transcribe`), and the
  8 GB card runs it: Whisper and Cohere **take turns** on the GPU, one loaded at a time (section
  4). Whisper's transcript is stored with its words; the second listen reads it and the file's
  audio, and the stages that read a transcript (subtitles, the index, tags) follow it. Admin >
  Machines says "Second listen: runs here, taking turns with Whisper". A machine that cannot run
  it at all still transcribes, and one that can corrects its transcripts; *Transcribe without
  the second listen* now only lets such a machine pass a transcript on after 15 minutes.
- **The batch is the GPU's own:** the `gpu` pool is per worker, and its cluster size (2 on live)
  was set for the Mac. A worker whose supervisor sized a batch from its NVIDIA GPU takes that
  many files at once instead (`MAKTABA_POOL_GPU`): 8 on the 2080 SUPER, the batch that ran at
  72×.
- **The share:** `transcribe` decodes the file off the NAS. A machine whose storage read check
  fails no longer claims it, and a file whose home path the path map does not cover fails
  saying so (`path_map: {"/Volumes/Originals/": "O:/"}` on the PC; a library elsewhere needs its
  own entry).
- **The install:** the app and every update install the `stt` extra's wheels; the node installers
  (`install-node.ps1`, `install-node.sh`) now default to `stt` on an x86_64 machine with an NVIDIA
  GPU of 4 GiB or more (they defaulted to no extras), and an update adds `stt` to such a machine
  that lacks it. No administrator step: the CUDA libraries Whisper needs come as wheels, and the
  NVIDIA driver is all the machine needs.

---

## 3. Sizing tiers

**How to read the speeds.** Audio-hours = videos × 14.9 min (this library's mean; use your
own). Days to catch up = audio-hours ÷ (24 × the machine's speed in × real time). Steady state
is the same sum for the new videos of a day.

**Speeds used** (all one machine):

| Machine | Speed | Source |
|---|---|---|
| M3 Ultra, 60 GPU cores, second listen on | **47×** | **measured** (production, above) |
| M3 Ultra, each stage on its own, one file | Whisper **43–48×**, second listen **91–116×**, one after the other **29–34×** | **measured** on a fixture (8.8 min of synthetic Arabic speech, one worker, model loads excluded; two runs, while the live pipeline was busy on the same GPU). Production runs 2 workers with 2 files each, hence its 47× |
| Apple Silicon, 40 GPU cores | ~31× | **estimate**: 47× × 40/60 (linear in GPU cores, same generation; not measured) |
| Apple Silicon, 20 GPU cores | ~16× | **estimate**, as above |
| Apple Silicon, 10 GPU cores | ~8× | **estimate**, as above |
| NVIDIA RTX 2080 SUPER (8 GB), Whisper alone | 72× | **code**: story 38.8 benchmark, batch 8 (`supervisor/stt.go`) |
| NVIDIA RTX 2080 SUPER, Whisper + second listen | ~26–27×, Windows and Linux | **estimate, not measured on the PC**: the second listen alone ran 43× on that card at batch 4 (`stt/cohere_cuda.py`) and Whisper 72× at batch 8; the GPU runs one after the other, 1 ÷ (1/72 + 1/43) ≈ 27×. On Windows the two take turns (section 4): a swap moves ~5.5 GB over PCIe, ~1–3 s (estimate), once per turn of 8 files (~2 h of audio at this library's mean, ~270 s of GPU work), so ~1–2% |
| CPU only, 10 threads (the story 38.8 PC, 20 logical CPUs) | **2.6×** | **code**: measured with a batch of 4 (`supervisor/stt.go`, `cpuBatch`) |
| Other NVIDIA cards, other CPUs | **not measured** | — |

The two NVIDIA numbers are engine benchmarks, not production days. On the Mac the engine
benchmark was 148× (batch 32, Whisper alone, `stt/batched.py`) and production is 47× (batch 2
× 2 workers, with the second listen and the rest of the job). Expect production on a PC to be
lower than its benchmark too, by an amount we have not measured.

### Catch-up time (initial backlog)

| | Small: ~5k videos | Medium: ~50k videos | Large: ~150k videos |
|---|---|---|---|
| Audio (× 14.9 min) | ~1,240 h | ~12,400 h | ~37,200 h |
| M3 Ultra, 47× (measured speed) | ~1 day | ~11 days | ~33 days |
| Apple Silicon 40-core GPU, ~31× (estimate) | ~2 days | ~17 days | ~50 days |
| Apple Silicon 20-core GPU, ~16× (estimate) | ~3 days | ~33 days | ~99 days |
| Apple Silicon 10-core GPU, ~8× (estimate) | ~7 days | ~66 days | ~200 days |
| RTX 2080 SUPER, Whisper alone, 72× (benchmark) | <1 day | ~7 days | ~22 days |
| RTX 2080 SUPER + second listen, ~27× (estimate) | ~2 days | ~19 days | ~57 days |
| CPU only, 2.6× (measured) | ~20 days | ~200 days | ~600 days |

**Steady state** is small by comparison: 50 new videos a day of 15 minutes is 12.5 audio-hours,
about 16 minutes of the M3 Ultra, about 10 minutes of the 2080 SUPER (Whisper alone), or about
5 hours on 10 CPU threads. CPU-only transcription is fine for keeping up with a small library;
it is not a way to catch up a large one.

### Recommended hardware per tier

These are **recommendations**, built from the measurements above and the plans in
`supervisor/stt.go`. "Second listen" needs the GPU sizes given in section 4.

| | Small (~5k, no live TV) | Medium (~50k, a few channels) | Large (~150k, 20+ channels) |
|---|---|---|---|
| **macOS (Apple Silicon)** | any M-series, 16–24 GB (plan: 1 transcription worker) | M-series Max-class GPU, 36–64 GB (plan: 1–2 workers) | Ultra-class GPU, 96 GB or more (the measured home; plan: 2 workers) |
| **Windows** (x86_64, 10 1809+ / 11) | 16 GB RAM; NVIDIA 4 GB+ for Whisper, **8 GB+** to also run the second listen (taking turns; 10 GB+ keeps both loaded); CPU only works (slow) | 32 GB RAM; NVIDIA 10 GB+ | 64 GB+ RAM; NVIDIA 20 GB+ (plan: 2 workers), or several machines |
| **Linux** (x86_64, glibc 2.35+) | 16 GB RAM; NVIDIA 4 GB+, **6 GB+** for the second listen (taking turns; 8 GB+ keeps both loaded); CPU only works (slow) | 32 GB RAM; NVIDIA 8 GB+ | 64 GB+ RAM; NVIDIA 20 GB+, or several machines |
| CPU | any 4+ cores | 8+ cores | 8+ cores (the home's CPU was 72% idle at full transcription load) |
| Disk for Maktaba's data (estimate) | ~20 GB + backups | ~60 GB + backups | ~150 GB + backups |
| NAS and network | NAS mounted (SMB or NFS); 1 Gb LAN | 1 Gb LAN | 1 Gb LAN; plan ~10 Mbps per watched channel |

How the disk line is worked out (estimate): database 2.8 GB per 1,000 transcribed audio-hours
(3.5 / 35 / 104 GB), thumbnails 150 KB a video (0.75 / 7.5 / 22 GB), models ~10 GB, Python
~2 GB, update downloads ~3 GB, logs and caches ~2 GB. **Add backups:** each daily dump on live
is about a third of the database (23 GB for 64 GB), and several are kept, plus one before each
update; on live they take 235 GB.

How the network line is worked out: live TV is `video_kbps` (10 Mbps) per watched channel, so
28 channels watched at once is ~280 Mbps out (setting × channels). NAS reads for transcription
(estimate, if a whole file is read): ~2,000 videos a day × 182 MB mean file = ~360 GB a day,
about 34 Mbps on average.

---

## 4. Settings that change the load

### The `gpu` pool (the transcription batch)

- **What it is:** how many files one transcription worker decodes together. Bigger is faster
  until the GPU's memory runs out (`[transcription] batch`, `MAKTABA_STT_BATCH`, or the pool on
  the Queues page).
- **Apple Silicon:** the default is 32. On the 96 GB home it ran out of memory: with 2 workers,
  each worker's MLX memory already peaks at 19–24 GB with a batch of **2** (measured), against a
  20 GB cap per worker (`gpu_gb`), next to the second listen's model and everything else on the
  machine. Live runs **2 per worker**, and the GPU is still 89–98% busy, so a bigger batch has
  little left to gain here.
- **Safe value by memory size:** the only measured safe value is **2 per worker on 96 GB with 2
  workers**. Smaller Macs are **not measured**: start at 2, and raise it only while
  `stt.gpu_peak_gb` (Admin > Machines) stays under `gpu_gb` and swap does not grow.
- **NVIDIA (code, measured on an RTX 2080 SUPER):** Whisper turbo takes 2.3 GB in float16
  (1.2 GB in int8_float16) plus 0.14 GB per file of the batch. The plan picks the batch from the
  card, Whisper alone (`gpuTiers`):

  | GPU memory | Workers | Batch |
  |---|---|---|
  | 40 GB+ | 2 | 32 |
  | 20 GB+ | 2 | 16 |
  | 14 GB+ | 1 | 24 |
  | 10 GB+ | 1 | 16 |
  | 7 GB+ | 1 | 8 |
  | 5 GB+ | 1 | 8 (int8_float16) |
  | 4 GB+ | 1 | 4 (int8_float16) |
  | under 4 GB | CPU plan | 4 |

  With the second listen loaded beside Whisper on the same card the batches are smaller
  (`refineGPUTiers`: 40 GB+ 2 × 24, 20 GB+ 1 × 32, 14 GB+ 16, 11 GB+ 8, 9.5 GB+ 4). A card too
  small for both keeps Whisper alone's batch: the two take turns (below). On the 2080 SUPER a
  batch of 8 ran at 72× and one of 12 no faster (69×).

### The transcription plan on Apple Silicon (from the RAM)

From `sttTiers` in `supervisor/stt.go`:

| Unified memory | Workers | `audio_hours` | `gpu_gb` | `gpu_cache_gb` | `min_free_pct` |
|---|---|---|---|---|---|
| 96 GB+ | 2 | 16 | 20 | 2 | 25 |
| 64 GB+ | 2 | 10 | 12 | 2 | 25 |
| 36 GB+ | 1 | 8 | 12 | 2 | 25 |
| 24 GB+ | 1 | 6 | 8 | 1 | 25 |
| 16 GB+ | 1 | 3 | 5 | 1 | 20 |
| less | 1 | 2 | 3 | 0.5 | 15 |

A job waits while free memory is under `min_free_pct`.

### The second listen

- On by default (`[transcription] refine`). It runs Cohere Transcribe Arabic (3.8 GB on disk)
  as the `second_listen` stage after Whisper's `transcribe` and corrects its words. Whisper's
  transcript is stored with each sentence's words (their timings, which the alignment needs);
  the stage reads it, decodes the file's audio off the share again, and stores the corrected
  transcript as the active one. Subtitles, the index and tags follow the second listen, so they
  run once. Its own pool (`listen`) is per machine: a machine's worker takes its batch.
- Where it runs (code): Apple Silicon; an NVIDIA GPU of **7.5 GiB+ on Windows** (an 8 GB card;
  Windows' driver keeps ~1 GiB of the card) or **6 GiB+ on Linux**. It holds 3.85 GB of
  weights + 1.6 GB (Windows) or 0.5 GB (Linux) to load + 0.09 GB per stretch on the GPU of each
  worker: 6.1 GiB on Windows with Whisper off the GPU (`cohere_cuda.alone_gb`).
- **Together or taking turns**, from the GPU's measured free memory (NVML), not the operating
  system (`stt/gpu_turns.py`): where it holds Whisper (2.3 GB + 0.14 GB a file) and Cohere at
  once, both stay loaded (9.5 GiB+ on Windows, 7.5 GiB+ on Linux, at the plan's batch). Where it
  holds one at a time (the 2080 SUPER on Windows: 6.95 GiB free for a process, 5.8 GiB once
  Whisper is loaded), they take turns: the GPU runs one model's files while the other's wait,
  each turn a batch of files (the worker's batch, at most 30 minutes while the other waits);
  the idle model's weights move to the host's memory and back (CTranslate2's
  `unload_model(to_cpu=True)`, torch's `.to("cpu")`), ~1–3 s a swap (**estimate**), against
  21 s to load Cohere from disk (measured on the M3 Ultra) or 20–26 s on the PC. A GPU that was
  to hold both but has no room after all (another program took part of it) switches to turns.
  Admin > Machines says which ("beside Whisper", "taking turns with Whisper").
- Speed: 43× at a batch of 4 on the 2080 SUPER, 11× alone (code). On the M3 Ultra 91–116× for
  one file (measured on a fixture, section 3).
- **A Windows or Linux machine that cannot run it still transcribes**; a machine that runs it
  corrects those transcripts. Settings > Transcription > *Transcribe without the second listen*
  lets a machine without it pass a transcript on with Whisper's words when no machine with it
  takes it within 15 minutes; one that runs it gives those theirs later, a few at a time.

### Live TV: `max_active_channels` and `video_kbps`

- `video_kbps`: the bitrate of every watched channel (10,000 on live). It sets the network
  (10 Mbps per watched channel) and the encoder's work.
- `max_active_channels`: how many channels may be on air at once. Unset, the code defaults to 16
  with VideoToolbox and 8 otherwise (`MAKTABA_CHANNEL_MAX_ACTIVE`); live allows 28.
- **NVENC:** at most 6 sessions at once by default (`MAKTABA_CHANNEL_NVENC_MAX`; GeForce cards
  open only a few). A block that finds no free session is encoded with libx264 on the CPU.
- **CPU and memory per encoder: not measured** (no channel was watched while we measured).

### The LLM

- Live: `qwen2.5:7b`, 4.4 GB on disk, loaded only while an LLM stage runs, one question at a
  time (`lookup` pool 1). Its memory while loaded: not measured here; at least its 4.4 GB of
  weights.
- A bigger model needs more memory in the same proportion to its file; a smaller one less.
  `[llm] url` can point every LLM stage at a model server on another machine
  ([LLMs](cluster/llms.md)), which takes that memory off the home.
- Only Maktaba.app (macOS) bundles Ollama. On Windows and Linux, point `[llm] url` at an Ollama
  you run, or the LLM stages wait.

### Turning parts off (Epic 51, upcoming)

[Epic 51](../specs/epics/51-configurable-components/README.md) (spec, 2026-11-03, **not
shipped**) will let a home turn transcription, the index worker, the LLM, Live TV and other parts
off or leave them out of the install. Until then, stages can be paused one by one on the Queues
page, or a machine's queues switched off on Admin > Machines.

---

## 5. Worker machines

A cluster is one **home** plus any number of **machines** that pull work from the home's queues
([cluster](cluster/README.md)).

### What a worker needs

- **Network to the home:** TCP 8080 (web app, API, the cluster link) and 5432 (the database);
  optional UDP 5353 to find the home.
- **The NAS mounted, read and write:** it reads the videos itself (media never travels through
  the home) and writes subtitles beside them and its own files in the share's `maktaba` folder.
  A path map tells it where the home's paths are on this machine
  ([join a machine](cluster/join-a-machine.md#step-3--on-the-machine-mount-the-nas-share)).
  On Windows, a worker that runs as a service needs the `\\nas\share` path, not a drive letter.
- **ffmpeg** and the pipeline (the node installer or the app brings both).
- **For transcription:** Apple Silicon, or an NVIDIA GPU (4 GB+; for the second listen 8 GB+ on
  Windows, 6 GB+ on Linux, taking turns with Whisper below 10 GB on Windows and 8 GB on Linux),
  or the CPU (slow). Section 3's per-machine speeds apply.
- **The second listen's model:** only the home needs the Hugging Face login; it puts a copy of
  the model on the NAS for the machines ([models](cluster/models.md)).

### Which stages a worker takes

Stages marked `placement="any"` run on any machine. These stay **on the home** (from the live
Windows node's `workers.stages_off`, which matches `graph.py`):

`index`, `chapter_infer`, `thumbnail`, `thumb_small`, `chapter_thumbs`, `review_pages`,
`display_title`, `check_tags`, `check_clips`, `plan_channels`, `write_channels`, `tv_stage`,
`tv_swap`, `tv_gc`, `tv_rollback`, `plex_livetv`, `dizquetv_channels`, `dizquetv_audit`,
`enrich`, `convert_mp4`, `storage_canonicalize`.

Two exceptions in the code: an LLM stage (`model_rules`) may run on a machine when the model
server's address is not `localhost`, and a Plex stage (`relay`) may run on a machine through the
home's relay, which adds the token.

What this means for sizing:

- The **home always does the search index** of every transcript, wherever it was transcribed.
  On live, `index` runs as fast as `transcribe` (1,962 vs 1,963 on 2026-11-03; median 30 s a
  job) in a 3.4 GB worker. A home that takes transcripts from several GPUs needs the CPU and
  memory for their index too.
- The **home holds the database**, which grows with every transcript (2.8 GB per 1,000
  audio-hours, estimate above).
- The `nas_read` pool (2) is **global**: adding machines does not add NAS-read slots.

### How adding a worker scales transcription

Each machine transcribes its own files through its own GPU, so transcription should add up
roughly in line with the GPUs: the home's 47× plus each machine's own speed. **This is expected,
not measured:** no machine other than the home has transcribed on the live cluster. For the live
PC with its second listen (Whisper and Cohere taking turns on the 8 GB card): **~47× + up to
~27× ≈ 70× for the cluster, an estimate**, the PC's part built from engine benchmarks that
production will not reach in full (section 3). The PC now corrects its own transcripts, so the
Mac's GPU no longer spends time on them. The limits we know of are the home's index (above), the
NAS, and the network to the NAS.

### Windows and Linux workers

| | Windows | Linux |
|---|---|---|
| Supported | yes, x86_64 (the app, or the node installer) | yes, x86_64 and aarch64 |
| Whisper | CTranslate2 (faster-whisper): CUDA on an NVIDIA GPU, else the CPU | same on x86_64; **none on aarch64** (the `stt` extra installs no engine there) |
| Second listen | NVIDIA 8 GB+ (8 GB: taking turns with Whisper; 10 GB+: both loaded) | NVIDIA 6 GB+ (x86_64; both loaded from 8 GB) |
| AMD or Intel GPUs | not used for transcription (CPU path) | not used for transcription (CPU path) |

---

## 6. A home that is also a dev machine

The live home is also where Maktaba is developed. While we measured, these were running beside
Maktaba (`top` footprint):

| Program | Memory |
|---|---|
| Android emulator (qemu) | 11 GB |
| Gradle (two Java processes) | 4.4 GB + 2.8 GB |
| Browsers and other apps (WebKit, Claude, Xcode, …) | several GB more |

With Maktaba's two GPU workers at 10–11 GB each, the 96 GB machine had ~1 GB free, 22 GB
compressed and 7.9 of 9.2 GB swap in use. When the memory runs short, transcription slows
first: each job waits while free memory is under `min_free_pct` (25%).

On a machine you also develop on:

- Count the emulator, the IDE and the build tools in the memory budget. They took ~18 GB here,
  close to two transcription workers.
- Lower `[transcription] workers` (or the `gpu` pool) while you work, and raise it when you stop.
- Or move transcription to another machine and keep the dev machine as the home.

---

## 7. What is not supported

- **Windows on ARM:** no build (the Windows app is x64 only).
- **Linux aarch64:** the server runs; transcription does not (no Whisper engine for it).
- **Intel Macs:** no Maktaba.app (it is Apple Silicon only, macOS 13+); the Homebrew server runs,
  with no transcription engine.
- **AMD and Intel GPUs:** not used for transcription. For video, Live TV uses VideoToolbox (Mac),
  NVENC (NVIDIA) or libx264 (CPU); playback transcoding also uses Intel QuickSync. AMD's AMF and
  VA-API are not used.
- **LLM on Windows and Linux:** no bundled Ollama; run your own and set `[llm] url`.
