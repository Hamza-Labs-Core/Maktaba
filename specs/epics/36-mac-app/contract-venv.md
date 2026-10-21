# Epic 36 — contract: the pipeline inside Maktaba.app (track B)

> **Status:** implemented (stories 36.3 and the pipeline half of 36.4).
> **Callers:** track A (`maktaba-server`: the supervisor, `setup --json --step venv|models`,
> `maktaba-server models download`), track D (the payload layout, `make mac-app`, updates).
> Track C (the app) shows the progress track A relays; it calls nothing here directly.

Three things the rest of the app calls:

1. **`venv_bootstrap`** — builds `~/Library/Application Support/Maktaba/venv` from the bundled
   wheel and constraints, with the bundled Python, before any venv exists.
2. **`maktaba-pipeline models status|download`** — the model files the workers load.
3. **The environment** the supervisor gives the pipeline (`MAKTABA_STT_*`,
   `MAKTABA_MODEL_CACHE_DIR`, `MAKTABA_CHROMA_DIR`).

## 1. The venv bootstrap

Code: `pipeline/src/maktaba_pipeline/venv_bootstrap.py` (standard library only).
Tests: `pipeline/tests/test_venv_bootstrap.py` (run by the node-packaging CI gate, which also
builds a real venv from the release wheel on macOS arm64).

### How to run it

The wheel is a zip, so the bootstrap runs from the wheel itself; nothing else needs to ship:

```sh
P="Maktaba.app/Contents/Resources/server"
PYTHONPATH="$P/pipeline/maktaba_pipeline-<v>-py3-none-any.whl" \
  "$P/python/bin/python3" -m maktaba_pipeline.venv_bootstrap --payload "$P/pipeline"
```

(Do not pass `-I` or `-E` to Python: they make it ignore `PYTHONPATH`.)

### Inputs

| Flag | Env fallback | Default |
|---|---|---|
| `--python PATH` | `MAKTABA_PYTHON` | the interpreter running it |
| `--payload DIR` (one `maktaba_pipeline-*.whl` + one `*constraints.txt`) | `MAKTABA_PIPELINE_PAYLOAD` | — |
| `--wheel F --constraints F` (instead of `--payload`) | — | — |
| `--venv DIR` | `MAKTABA_VENV` | `~/Library/Application Support/Maktaba/venv` (Linux: `$XDG_DATA_HOME/maktaba/venv`) |
| `--extras a,b` (must be complete in the venv) | `MAKTABA_PIPELINE_EXTRAS` | `stt,search` |
| `--status-file F` | — | `venv-status.json` beside the venv |
| `--check` | — | change nothing; say what a run would do |
| `--force` | — | rebuild from scratch |

The constraints file must pin every dependency **with its hash** (`build-release.sh` exports it
from `uv.lock` that way), and it names the package indexes they come from: PyPI and, for
torch's CUDA build on Windows, PyTorch's (`--index-url https://pypi.org/simple`,
`--extra-index-url https://download.pytorch.org/whl/cu130`; story 38.9). Every pin in it is
installed (`pip install --require-hashes --no-deps -r constraints`: pip takes no hashes from a
constraints file, so the pins are installed as requirements), then the wheel, checked against
its own SHA-256. pip looks for each pin on both indexes and takes the file one of its hashes
names, whichever index has it, so a file from either index is installed only when it is the
pinned one (PyTorch's index also serves some packages PyPI has, a few of them as other files:
Linux's triton 3.8.0, and its torch 2.14.1+cu130, which `torch==2.14.1` matches). So the extras the venv gets are
the ones the constraints file was exported with (today all but `dev`: `stt`, `search`,
`mediaintel`); `--extras` then checks that each requirement of `maktaba-pipeline[stt,search]`
for this machine is installed at an allowed version, and fails the run if not.

### What a run does

Under a lock (`.venv.lock` beside the venv; a second run waits, then finds nothing to do) it
compares the inputs with the stamp the last run left, `<venv>/maktaba-venv.json`:

| Plan (`action`) | When | What it does |
|---|---|---|
| `none` | same constraints hash, wheel hash, extras and Python | nothing (~0.2 s) |
| `build` | no venv; no stamp; **constraints hash changed**; Python minor version changed; `--force` | removes the venv, builds it afresh |
| `resume` | the stamp says `building` for these same constraints (a build that failed or was stopped after creating the venv) | keeps the venv; pip skips what is installed |
| `repair` | the bundled Python moved (the app was moved or renamed) or got a patch release | `python -m venv --upgrade` (repoints the venv), reinstalls the wheel if it changed |
| `wheel` | only the wheel changed (an app update with the same dependencies) | reinstalls the wheel alone |

A `wheel` or `repair` run that fails keeps the previous `ready` stamp, so the next run plans
the same action again. A `build` removes the stamp before the rest of the old venv, so a removal
cut short is never taken for a ready venv.

The venv is built in place (a venv cannot be moved), so **the supervisor stops the pipeline
workers before a run that is not `none`** (`--check` says which, without changing anything).

### Outputs

**stdout:** one JSON object per line. Each has `"event": "status"` and the status-file fields
below; `--check` prints one `{"event": "plan", "action": "...", "reason": "..."}` instead.
**stderr:** a human-readable error line on failure.

**The status file** (rewritten whole, atomically, on every change; readable by any process):

```json
{
  "venv": "/Users/me/Library/Application Support/Maktaba/venv",
  "wheel": "maktaba_pipeline-1.4.0-py3-none-any.whl",
  "constraints_sha256": "75f6…",
  "state": "building",
  "step": "deps",
  "progress": 0.41,
  "message": "Collecting torch==2.9.1 (from -r …)",
  "done": 61,
  "total": 153,
  "pid": 41234,
  "updated_at": "2026-10-20T23:48:45Z"
}
```

- `state`: `waiting` (another run holds the lock) · `building` · `ready` · `failed` · `interrupted`.
- `step`: `plan` → `venv` → `deps` → `wheel` → `verify` → `done` (or `inputs`, `lock`).
- `progress`: 0–1 over the whole run, never going back. `done`/`total`: packages seen of the
  constraints' pins, on `deps` events. `message`: a line for the UI. `error`: on `failed`, the
  failing step's last output lines.
- Progress events are sent at most twice a second; step changes always are.
- A run waiting on the lock says `waiting` on its own stdout only; the file stays the running
  build's.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | the venv is ready (built, repaired, or already up to date) |
| 1 | a step failed (network, disk, a hash mismatch, verify, any unexpected error); the status says `failed`; the next run resumes |
| 2 | bad inputs (missing file, no hashes in the constraints, not one wheel in the payload) |
| 10 | `--check` only: a run would change the venv |
| 130 | stopped by SIGTERM / Ctrl-C (pip is stopped too); the next run resumes |

### After it

The workers run `<venv>/bin/maktaba-pipeline run …` (or `<venv>/bin/python -m maktaba_pipeline`).
**Track A:** point `MAKTABA_PYTHON` / the pipeline role's command at the venv's
`bin/python`, not at the bundled bare Python (which has no dependencies).

## 2. `maktaba-pipeline models status|download`

Code: `pipeline/src/maktaba_pipeline/models/fetch_cli.py`. Runs with the venv's
`maktaba-pipeline`. This is what `maktaba-server models download` and the setup's models step
call.

```sh
maktaba-pipeline models status   [--only NAMES] [--json]     # no network
maktaba-pipeline models download [--only NAMES] [--share DIR] [--json]
```

The items are exactly what the workers load, read from the same settings the workers read:

| Name | Model | How the worker loads it |
|---|---|---|
| `whisper` (Apple Silicon: MLX) | `MAKTABA_STT_MODEL` (default `mlx-community/whisper-large-v3-turbo`, ~1.6 GB) | `mlx_whisper` → `snapshot_download(repo)` (whole repo) |
| `whisper` (Windows, Linux: CTranslate2, story 38.8) | `MAKTABA_STT_MODEL` (default `dropbox-dash/faster-whisper-large-v3-turbo`, float16, 1.6 GB; an MLX id is replaced by its CTranslate2 conversion, logged) | `stt/ct2.py` → `snapshot_download(repo, allow_patterns=engine.CT2_FILES)`: `config.json`, `preprocessor_config.json`, `model.bin`, `tokenizer.json`, `vocabulary.*` (faster-whisper's own list) |
| `cohere` | `MAKTABA_STT_REFINE_MODEL` (default `CohereLabs/cohere-transcribe-arabic-07-2026`, gated, 4.13 GB) | Apple Silicon: `mlx_audio` with its file patterns. Windows, Linux (story 38.9): transformers' `cohere_asr` model, `stt/cohere_cuda.py` → `snapshot_download(repo, allow_patterns=["*.json", "*.safetensors", "*.model"])` (not the repo's own code or example recordings). Else the home's copy on the share |
| `dictionary` | CAMeL Tools `morphology-db-msa-r13` | `CATALOGUE.download_package` (into `CAMELTOOLS_DATA`, made first when missing: CAMeL Tools does not make it) |
| `embedder` | `MAKTABA_EMBED_MODEL` (default `intfloat/multilingual-e5-small`) | sentence-transformers |

`cohere` and `dictionary` are `skipped` when `MAKTABA_STT_REFINE=0`, and on a Windows or
Linux machine that cannot run the second listen (below), with the reason as the item's
`message`: no Hugging Face sign-in is asked for a model that will never run there (the run
exits 0). `whisper` is skipped when `MAKTABA_STT_MODEL` is a local folder. Silero VAD ships
inside its wheel (nothing to fetch).

The CTranslate2 default is the conversion faster-whisper 1.2.1 itself fetches for
`large-v3-turbo` (it names it `mobiuslabsgmbh/faster-whisper-large-v3-turbo`, a repo since
moved to `dropbox-dash/`; the old name only redirects). Its CUDA needs cuBLAS 12 only (the
`nvidia-cublas-cu12` wheel of the `stt` extra; CTranslate2 ≥ 4.6.3 runs Whisper without
cuDNN) and the NVIDIA driver; no CUDA toolkit.

**The second listen on Windows and Linux (story 38.9).** Cohere Transcribe Arabic runs through
transformers (≥ 5.4: its `cohere_asr` model, loaded with `accelerate` straight onto the GPU;
`librosa` for the feature extractor's mel filters; `sentencepiece`) on torch's CUDA build, on
the NVIDIA GPU Whisper uses (`MAKTABA_STT_DEVICE`), and respells with the same CAMeL
dictionary as the Mac (`camel-tools` installs on both: Windows needs no kenlm, Linux has its
wheel; `morphology-db-msa-r13` is 39 MB). Never on the CPU: there the 2 B model would take
hours for an hour of audio. `stt/cohere.py` `unavailable()` (and so `models`) says why a machine
has none: transcription set to the CPU; torch, transformers, accelerate or librosa missing;
a torch without CUDA; no CUDA device; a GPU older than torch's build runs on (CUDA 13: Turing,
sm_75, and newer); a GPU smaller than **9.5 GiB on Windows** (a 10 GB card) **or 7.5 GiB
elsewhere** (an 8 GB card). The Windows threshold is measured: the driver's budget (WDDM, what
`torch.cuda.mem_get_info` reports) leaves a process 6.95 of an 8 GB card, Whisper takes 1.1 of
it, and loading the model beside Whisper takes 1.6 GiB more than its 3.85 GiB of weights, so
an 8 GB card has 5.8 GiB where the model, a batch and Whisper's room need 6.4. When the
machine can, the worker checks the GPU's free memory before the model loads (weights + 1.6 on
Windows, 0.5 elsewhere, + 4 stretches × 0.09 + Whisper's batch × 0.14 GiB) and keeps Whisper's
words, saying how much was free, until a file finds the room; once loaded it hears the
stretches in batches sized from what is left (at most 32, halved on an out-of-memory) and
gives the memory back after each file. Precision: bfloat16 on Ampere and newer, float16
before. A transcript's `metadata.refine` says `source` (`hf`, `share`, or `skipped` with the
reason), on CUDA `runtime` (`transformers 5.17.0, torch 2.14.1+cu130, cuda:0 float16`), and
`dictionary: "unavailable"` when the respelling had no dictionary (on any machine).

**torch.** On Linux it comes from PyPI, whose x86_64 wheel is the CUDA 13.0 build (with the
`nvidia-*` / `cuda-toolkit` wheels); macOS keeps PyPI's (MLX does its second listen). PyPI's
Windows torch is a CPU build, so on Windows x86_64 `pyproject.toml` takes it from PyTorch's own
index (`[tool.uv.sources]`, an explicit `[[tool.uv.index]]`: nothing else comes from it):
`torch==2.14.1+cu130`, a 2.0 GB download, CUDA 13.0 like Linux's (Turing and newer GPUs, an
NVIDIA driver of R580 or later). The lock pins it with its hash, and `build-release.sh` exports
the constraints with `uv export --emit-index-url` (uv 0.12 or later: it stops on an older uv,
and on a file that does not name every index of `pyproject.toml`). A Windows install therefore
reaches `download.pytorch.org` (and `pypi.nvidia.com`, where that index sends some NVIDIA
wheels, the same files as PyPI's) beside PyPI. The Windows venv with every extra but `dev` is
5.0 GB as the bootstrap's pip installs it (4.6 GB as `uv sync` does; torch 2.9 GB of it), where
it was 2.1 GB before this story with PyPI's CPU torch; Linux's is 7.7 GB, nearly all torch's
CUDA 13 libraries, which the `search` extra brought already.

**The gated model.** Hugging Face serves it only to an account that accepted its licence:
`HF_TOKEN` in the environment, or a token saved by `hf auth login` **in this `HF_HOME`**
(`<models>/huggingface/token` under the app). When Hugging Face refuses, the newest complete copy
the home published on `--share` (default `$MAKTABA_MODELS_DIR`, the share's
`maktaba/models` folder) is copied to `<models>/share-copies/` and checked file by file
(PR 117's `stt.model_share`); the worker uses that copy from then on.

**Output** with `--json`, one object per line:

```json
{"event": "model", "name": "whisper", "model": "mlx-community/whisper-large-v3-turbo",
 "state": "downloading", "bytes": 412000000, "total_bytes": 1610000000}
{"event": "model", "name": "cohere", "model": "CohereLabs/…", "state": "needs_login",
 "message": "Hugging Face: GatedRepoError: 401 … | share: no share here"}
{"event": "done", "ok": false, "exit": 3, "items": {"whisper": "ready", "cohere": "needs_login", …}}
```

`state`: `downloading` · `ready` (`source`: `cache` | `hf` | `share` | `share-copy` | `camel`,
`path`) · `skipped` · `missing` (status) · `needs_login` · `failed` (`message`).
`status` calls a cached Hugging Face model ready only when `download` saw it through (it writes
`.maktaba-complete` into the snapshot; the cache's own `refs/` is written before the first file).
A model a worker downloaded by itself shows `missing` until `download` checks it (a few
metadata requests, no files fetched again). Byte progress
at most twice a second. Logs go to stderr. Without `--json`, one readable line per change.

**Exit codes:** 0 every item ready or skipped · 3 only the gated model is missing for want of a
Hugging Face sign-in (the app shows its sign-in screen, or offers the NAS copy) · 1 any other
failure · 2 an unknown `--only` name · 10 (`status` only) something is not here yet.
A download is resumable: run it again (Hugging Face keeps partial files).

## 3. The environment the supervisor sets

| Variable | The app's value | Read by |
|---|---|---|
| `MAKTABA_STT_MODEL` | `[transcription].model` (default turbo, in the engine's format) | `engine/stages/transcribe.py` `settings_from_env` (`stt/engine.py` `model_for`) |
| `MAKTABA_STT_REFINE` | `1` / `0` (second listen on/off; default on). On Windows and Linux it runs where the NVIDIA GPU holds it beside Whisper (above); the GPU tiers then leave it room | `transcribe.refine_model`; the supervisor's `PlanSTT` |
| `MAKTABA_STT_REFINE_MODEL` | the Cohere repo id (default as above) | `transcribe.refine_model` |
| `MAKTABA_STT_LANGUAGE`, `_WORDS`, `_BATCH`, `_GPU_GB`, `_GPU_CACHE_GB`, `_AUDIO_HOURS`, `_SLOT` | the RAM tiers of 36.4 (`_GPU_GB`, `_GPU_CACHE_GB`: MLX only) | `transcribe.py`, `stt/batched.py` |
| `MAKTABA_STT_BATCH` | files decoded together per pass: `[transcription].batch`, else sized by the supervisor (NVIDIA GPU tiers; 4 on the CPU; unset on a Mac: 32) | `transcribe.settings_from_env` (default 32 on MLX, 8 on CTranslate2) |
| `MAKTABA_STT_DEVICE` (CTranslate2) | `[transcription].device`; unset: `auto` (CUDA when usable, else the CPU). `cuda` / `cuda:<n>` that cannot be used makes the worker `unavailable` for transcribe, saying why. The second listen uses the same GPU (`cpu`: none) | `stt/ct2.py` `choose_device`, `stt/cohere_cuda.py` |
| `MAKTABA_STT_COMPUTE_TYPE` (CTranslate2) | `[transcription].compute_type`, else `int8_float16` on a GPU under 7 GB; unset: `auto` (float16 on CUDA, int8 on the CPU); one the device cannot run falls back to its default, logged | `stt/ct2.py` `choose_compute_type` |
| `MAKTABA_STT_CPU_THREADS` (CTranslate2) | `[transcription].cpu_threads`, else half the logical CPUs (2 to 16) when the machine transcribes on the CPU | `stt/ct2.py` `load` |

On Windows and Linux (story 38.8) the supervisor sizes the plan from the NVIDIA GPU
(`nvidia-smi --query-gpu=name,memory.total`): the transcription workers and `MAKTABA_STT_BATCH`
per GPU-memory tier (an 8 GB card: one worker, 8 files), `MAKTABA_STT_AUDIO_HOURS` and
`_MIN_FREE_PCT` from the RAM. Without a GPU (or `[transcription] device = "cpu"`) there are no
transcription workers: the main worker transcribes, and it gets the knobs. `[pipeline]
instances` and `[transcription] workers` still set the number of workers. A transcript records
its engine in `transcripts.backend` (`mlx-batched`, `ct2-batched`) and the runtime in
`backend_version` (e.g. `faster-whisper 1.2.1, ctranslate2 4.8.3, cuda float16`); the
transcribe fingerprint names the CTranslate2 turbo as the MLX one, so a PC does not transcribe
again what a Mac did. With the second listen on (story 38.9) and a GPU it fits on (9.5 GiB and
up on Windows, 7.5 elsewhere: the pipeline's own threshold, kept equal by a test), the tiers
give Whisper a smaller batch to leave it room: 10 GB: 4 files; 12 GB: 8; 16 GB: 16; 20 GB and
up: 32; 40 GB and up: two workers of 24; outside Windows an 8 GB card: 4 files in
`int8_float16`. A smaller GPU keeps Whisper's tiers (its transcripts keep Whisper's words).
| **`MAKTABA_MODEL_CACHE_DIR`** (new) | `~/Library/Application Support/Maktaba/models` | `maktaba_pipeline/model_dirs.py`: sets `HF_HOME=<dir>/huggingface` and `CAMELTOOLS_DATA=<dir>/camel_tools` (each only when not already set) when the package is imported; share copies go to `<dir>/share-copies` |
| `MAKTABA_CHROMA_DIR` | `~/Library/Application Support/Maktaba/chroma` | `search/semantic.py`, `search/index_stage.py` (default `~/.maktaba/chroma`) |
| `MAKTABA_EMBED_MODEL` | unset (e5-small) | `search/semantic.py` |
| `MAKTABA_MODELS_DIR` | **leave unset on a home with a NAS** (see below) | `stt/model_share.py`: the *share's* models folder |

The pipeline never read `MAKTABA_TRANSCRIPTION_BACKEND` / `MAKTABA_TRANSCRIPTION_MODEL`, which
`ChildEnv` sets today: track A maps `[transcription]` to the `MAKTABA_STT_*` names above.

**`MAKTABA_MODELS_DIR` is not the model cache.** `ChildEnv` sets it to `<data>/models` today,
but the pipeline reads it as the *share's* models folder: where the home publishes the gated model
for the other machines, overriding the share it would find (`<share root>/maktaba/models`). Set
to a local folder, the home publishes there and the nodes never see it. Track A: set
`MAKTABA_MODEL_CACHE_DIR=<data>/models` instead and drop `MAKTABA_MODELS_DIR` (keep honouring an
operator's own). The two can point at the same folder without clashing (the published copies
are `<org>__<name>/` folders; the cache's are `huggingface/`, `camel_tools/`, `share-copies/`).
