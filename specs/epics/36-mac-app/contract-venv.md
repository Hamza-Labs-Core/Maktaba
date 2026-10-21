# Epic 36 — contract: the pipeline inside Maktaba.app (track B)

> **Status:** implemented (stories 36.3 and the pipeline half of 36.4).
> **Callers:** track A (`maktaba-server`: the supervisor, `setup --json --step venv|models`,
> `maktaba-server models download`), track D (the payload layout, `make mac-app`, updates).
> Track C (the app) shows the progress track A relays; it calls nothing here directly.

Three things the rest of the app calls:

1. **`venv_bootstrap`** — builds `~/Library/Application Support/Maktaba/venv` (Windows:
   `%LOCALAPPDATA%\Maktaba\venv`, story 38.7) from the bundled wheel and constraints, with the
   bundled Python, before any venv exists.
2. **`maktaba-pipeline models status|download`** — the model files the workers load.
3. **The environment** the supervisor gives the pipeline (`MAKTABA_STT_*`,
   `MAKTABA_MODEL_CACHE_DIR`, `MAKTABA_CHROMA_DIR`).

## 1. The venv bootstrap

Code: `pipeline/src/maktaba_pipeline/venv_bootstrap.py` (standard library only).
Tests: `pipeline/tests/test_venv_bootstrap.py` (run by the node-packaging CI gate on Linux and
Windows, which also builds a real venv from the release wheel on macOS arm64 and on Windows x64
with the pinned python-build-standalone the Windows app ships).

### How to run it

The wheel is a zip, so the bootstrap runs from the wheel itself; nothing else needs to ship:

```sh
P="Maktaba.app/Contents/Resources/server"
PYTHONPATH="$P/pipeline/maktaba_pipeline-<v>-py3-none-any.whl" \
  "$P/python/bin/python3" -m maktaba_pipeline.venv_bootstrap --payload "$P/pipeline"
```

On Windows (`<install>` is `%LOCALAPPDATA%\Maktaba`, the Python is `server\python\python.exe`):

```powershell
$P = "$env:LOCALAPPDATA\Maktaba\server"
$env:PYTHONPATH = "$P\pipeline\maktaba_pipeline-<v>-py3-none-any.whl"
& "$P\python\python.exe" -m maktaba_pipeline.venv_bootstrap --payload "$P\pipeline"
```

(Do not pass `-I` or `-E` to Python: they make it ignore `PYTHONPATH`.)

### Inputs

| Flag | Env fallback | Default |
|---|---|---|
| `--python PATH` | `MAKTABA_PYTHON` | the interpreter running it |
| `--payload DIR` (one `maktaba_pipeline-*.whl` + one `*constraints.txt`) | `MAKTABA_PIPELINE_PAYLOAD` | — |
| `--wheel F --constraints F` (instead of `--payload`) | — | — |
| `--venv DIR` | `MAKTABA_VENV` | `~/Library/Application Support/Maktaba/venv` (Windows: `%LOCALAPPDATA%\Maktaba\venv`, else `%USERPROFILE%\AppData\Local\Maktaba\venv`; Linux: `$XDG_DATA_HOME/maktaba/venv`) |
| `--extras a,b` (must be complete in the venv) | `MAKTABA_PIPELINE_EXTRAS` | `stt,search` |
| `--status-file F` | — | `venv-status.json` beside the venv |
| `--check` | — | change nothing; say what a run would do |
| `--force` | — | rebuild from scratch |

The constraints file must pin every dependency **with its hash** (`build-release.sh` exports it
from `uv.lock` that way). Every pin in it is installed (`pip install --require-hashes --no-deps
-r constraints`: pip takes no hashes from a constraints file, so the pins are installed as
requirements), then the wheel, checked against its own SHA-256. So the extras the venv gets are
the ones the constraints file was exported with (today all but `dev`: `stt`, `search`,
`mediaintel`); `--extras` then checks that each requirement of `maktaba-pipeline[stt,search]`
for this machine is installed at an allowed version, and fails the run if not.

### What a run does

Under a lock (`.venv.lock` beside the venv: `flock`, on Windows `msvcrt.locking`; a second run
waits, then finds nothing to do) it
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
| 130 | stopped by SIGTERM / Ctrl-C, on Windows by CTRL_BREAK (`SIGBREAK`; pip, in its own process group, gets one too, else is terminated); the next run resumes. A console closed or a sign-out on Windows ends the run before it can say so: the status may stay `building` (its `pid` gone); the stamp says `building` too, so the next run resumes |

### After it

The workers run `<venv>/bin/maktaba-pipeline run …` (or `<venv>/bin/python -m maktaba_pipeline`;
on Windows `<venv>\Scripts\maktaba-pipeline.exe` / `<venv>\Scripts\python.exe`).
**Track A:** point `MAKTABA_PYTHON` / the pipeline role's command at the venv's
`bin/python` (`Scripts\python.exe`), not at the bundled bare Python (which has no dependencies).

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
| `whisper` (Windows, Linux: CTranslate2, story 38.8) | `MAKTABA_STT_MODEL` (default `dropbox-dash/faster-whisper-large-v3-turbo`, float16, 1.6 GB; an MLX id is replaced by its CTranslate2 conversion, logged) | `stt/ct2.py` → `snapshot_download(repo, revision=<pin>, allow_patterns=engine.CT2_FILES)`: `config.json`, `preprocessor_config.json`, `model.bin`, `tokenizer.json`, `vocabulary.*` (faster-whisper's own list), then each file's SHA-256 against `engine.CT2_PINS` |
| `cohere` | `MAKTABA_STT_REFINE_MODEL` (default `CohereLabs/cohere-transcribe-arabic-07-2026`, gated) | `mlx_audio` with its file patterns; else the home's copy on the share |
| `dictionary` | CAMeL Tools `morphology-db-msa-r13` | `CATALOGUE.download_package` |
| `embedder` | `MAKTABA_EMBED_MODEL` (default `intfloat/multilingual-e5-small`) | sentence-transformers |

`cohere` and `dictionary` are `skipped` when `MAKTABA_STT_REFINE=0`, and on the CTranslate2
engine, which has no second listen yet (story 38.9: its transcripts keep Whisper's words and
say why); `whisper` when `MAKTABA_STT_MODEL` is a local folder. Silero VAD ships inside its
wheel (nothing to fetch).

The CTranslate2 default is the conversion faster-whisper 1.2.1 itself fetches for
`large-v3-turbo` (it names it `mobiuslabsgmbh/faster-whisper-large-v3-turbo`, a repo since
moved to `dropbox-dash/`; the old name only redirects). It, and `Systran/faster-whisper-large-v3`
(what an MLX large-v3 maps to), are fetched at a pinned commit (`stt/engine.py` `CT2_PINS`:
`dropbox-dash/...turbo` at `0a363e91`, `Systran/...large-v3` at `edaa852e`) and their files
checked against their SHA-256 once (a marker, `.maktaba-sha256`, in the snapshot); a file that
does not match is removed, so the next download fetches it again. A model of the operator's
own is fetched as its repo has it. Still unpinned (as on `main`): the MLX Whisper and the
Cohere model on Apple Silicon (`mlx_whisper`, `mlx_audio`, `fetch_cli.hf_download`), the
embedder (sentence-transformers) and the CAMeL data. Its CUDA needs cuBLAS 12 only (the
`nvidia-cublas-cu12` wheel of the `stt` extra; CTranslate2 ≥ 4.6.3 runs Whisper without
cuDNN) and the NVIDIA driver; no CUDA toolkit.

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
| `MAKTABA_STT_REFINE` | `1` / `0` (second listen on/off; default on) | `transcribe.refine_model` |
| `MAKTABA_STT_REFINE_MODEL` | the Cohere repo id (default as above) | `transcribe.refine_model` |
| `MAKTABA_STT_LANGUAGE`, `_WORDS`, `_BATCH`, `_GPU_GB`, `_GPU_CACHE_GB`, `_AUDIO_HOURS`, `_SLOT` | the RAM tiers of 36.4 (`_GPU_GB`, `_GPU_CACHE_GB`: MLX only) | `transcribe.py`, `stt/batched.py` |
| `MAKTABA_STT_BATCH` | files decoded together per pass: `[transcription].batch`, else sized by the supervisor (NVIDIA GPU tiers; 4 on the CPU; unset on a Mac: 32) | `transcribe.settings_from_env` (default 32 on MLX, 8 on CTranslate2) |
| `MAKTABA_STT_DEVICE` (CTranslate2) | `[transcription].device`; unset: `auto` (CUDA when usable, else the CPU). `cuda` / `cuda:<n>` that cannot be used makes the worker `unavailable` for transcribe, saying why | `stt/ct2.py` `choose_device` |
| `MAKTABA_STT_COMPUTE_TYPE` (CTranslate2) | `[transcription].compute_type`, else `int8_float16` on a GPU under 7 GB; unset: `auto` (float16 on CUDA, int8 on the CPU); one the device cannot run falls back to its default, logged | `stt/ct2.py` `choose_compute_type` |
| `MAKTABA_STT_CPU_THREADS` (CTranslate2) | `[transcription].cpu_threads`, else half the logical CPUs (2 to 16) when the machine transcribes on the CPU | `stt/ct2.py` `load` |

| **`MAKTABA_MODEL_CACHE_DIR`** (new) | `~/Library/Application Support/Maktaba/models` | `maktaba_pipeline/model_dirs.py`: sets `HF_HOME=<dir>/huggingface` and `CAMELTOOLS_DATA=<dir>/camel_tools` (each only when not already set) when the package is imported; share copies go to `<dir>/share-copies` |
| `MAKTABA_CHROMA_DIR` | `~/Library/Application Support/Maktaba/chroma` | `search/semantic.py`, `search/index_stage.py` (default `~/.maktaba/chroma`) |
| `MAKTABA_EMBED_MODEL` | unset (e5-small) | `search/semantic.py` |
| `MAKTABA_MODELS_DIR` | **leave unset on a home with a NAS** (see below) | `stt/model_share.py`: the *share's* models folder |

On Windows and Linux (story 38.8) the supervisor sizes the plan from the NVIDIA GPU
(`nvidia-smi --query-gpu=name,memory.total`): the transcription workers and `MAKTABA_STT_BATCH`
per GPU-memory tier (an 8 GB card: one worker, 8 files; under 4 GB, and Linux on aarch64, whose
CTranslate2 has no CUDA build: the CPU plan), `MAKTABA_STT_AUDIO_HOURS` and `_MIN_FREE_PCT`
from the RAM. Without a GPU (or `[transcription] device = "cpu"`) there are no transcription
workers: the main worker transcribes, and it gets the knobs. `[pipeline] instances` and
`[transcription] workers` still set the number of workers; more than the plan's share its
budget (each its part of the GPU's batch, the CPU threads, the audio hours). A transcript records
its engine in `transcripts.backend` (`mlx-batched`, `ct2-batched`) and the runtime in
`backend_version` (e.g. `faster-whisper 1.2.1, ctranslate2 4.8.3, cuda float16`); the
transcribe fingerprint names the CTranslate2 turbo as the MLX one, so a PC does not transcribe
again what a Mac did.

**Where transcription runs** (review of 38.8). The operator's own stage selection always
stands: with `transcribe` in `MAKTABA_PIPELINE_EXCLUDE_STAGES` (or a `MAKTABA_PIPELINE_STAGES`
without it) the supervisor starts no transcription workers and leaves the main worker's
exclusion as it is. Planned workers run only where the pipeline's Python has the engine (a
probe of its modules, without importing them) and, while the second listen is on, only where
it runs too (Apple Silicon). Elsewhere the main worker leaves `transcribe` to the machines that
run the second listen (its `unavailable()` says why), unless `rules.transcription`
`without_second_listen` is on (Settings, Transcription: "Transcribe without the second
listen"), read before each stage selection. A transcript made without its second listen stores
`skipped_fingerprint(fingerprint)` as its job's input hash: a worker that runs the second
listen does not take it as done, and every 10 minutes queues up to 20 of them (at least an hour
old, oldest first, priority 500, payload `relisten`) once it has the model; a worker without
the second listen gives a relisten job back for 15 minutes (for a day at most) and lets an
ordinary job's transcript stand.

**Where Whisper loads** (CTranslate2). With `MAKTABA_STT_DEVICE=auto` the GPU must have the
memory free that the model and its batch need (NVML's count: 2.3 GB in float16, 1.2 in
int8_float16, 0.14 a file, 0.25 to spare; another model in proportion to its `model.bin`),
else Whisper loads on the CPU with half the logical CPUs; a load that fails on the GPU all the
same is loaded on the CPU, and the process does not try the GPU again. GPUs are numbered as
nvidia-smi numbers them (`CUDA_DEVICE_ORDER=PCI_BUS_ID` unless set).

The pipeline never read `MAKTABA_TRANSCRIPTION_BACKEND` / `MAKTABA_TRANSCRIPTION_MODEL`, which
`ChildEnv` sets today: track A maps `[transcription]` to the `MAKTABA_STT_*` names above.

**`MAKTABA_MODELS_DIR` is not the model cache.** `ChildEnv` sets it to `<data>/models` today,
but the pipeline reads it as the *share's* models folder: where the home publishes the gated model
for the other machines, overriding the share it would find (`<share root>/maktaba/models`). Set
to a local folder, the home publishes there and the nodes never see it. Track A: set
`MAKTABA_MODEL_CACHE_DIR=<data>/models` instead and drop `MAKTABA_MODELS_DIR` (keep honouring an
operator's own). The two can point at the same folder without clashing (the published copies
are `<org>__<name>/` folders; the cache's are `huggingface/`, `camel_tools/`, `share-copies/`).
