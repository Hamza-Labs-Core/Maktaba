# Epic 39 — contract: transcription on Linux (story 39.7)

> **Status:** contract for 39.7. **Builds on** Epic 38's engine for Windows and Linux: 38.8 (Whisper
> on CTranslate2, faster-whisper's batched pipeline, CUDA `float16` / `int8_float16`, CPU `int8`,
> behind the transcribe stage's transcriber interface) and 38.9 (the Cohere second listen through
> transformers on torch CUDA). This story is the Linux half: what differs on Linux, and the tests
> that prove it runs there.

Today the engine transcribes on Apple Silicon only (`engine/stages/transcribe.py:unavailable()`
refuses anything but Darwin/arm64; the `stt` extra is all `darwin/arm64`). After 38.8/38.9 the
same stage runs on Windows. Linux needs the following on top.

## 1. NVIDIA on Linux

| What | How |
|---|---|
| a GPU is there | `/proc/driver/nvidia/gpus/*/information` (no process started), then `nvidia-smi --query-gpu=index,memory.total,driver_version --format=csv,noheader,nounits` for VRAM and the driver |
| the driver is new enough | ctranslate2's CUDA 12 build needs driver ≥ 525. torch's CUDA 13 wheels, which the second listen uses, need ≥ 580. Below 525: CPU (`check` warns with the driver found). From 525 up to 580: Whisper on CUDA and the second listen off, with that reason in the transcript's metadata |
| the libraries | the venv's pip CUDA wheels (`nvidia-cublas-cu12`, `nvidia-cudnn-cu12`, from the `stt` extra's `linux` marker). ctranslate2 `dlopen`s `libcublas.so.12` / `libcudnn*.so.9` through the loader's search path, which does not include the venv. The supervisor therefore prepends the venv's `site-packages/nvidia/*/lib` to `LD_LIBRARY_PATH` for the pipeline roles on Linux (only those directories that exist). The pipeline does the same when run by hand (`maktaba_pipeline/cuda_libs.py`, before ctranslate2 is imported) |
| containers / no device | `/dev/nvidiactl` missing while `/proc/driver/nvidia` exists (a container without the GPU passed through): CPU, and `check` says why |

## 2. aarch64

ctranslate2's Linux aarch64 wheels are CPU-only, and NVIDIA's pip CUDA wheels for aarch64 are for
SBSA servers. An aarch64 machine (a Raspberry Pi 5, an Ampere box) plans **CPU** (`int8`) unless
`nvidia-smi` answers. A Jetson (CUDA without those wheels) plans CPU, and the PR records that
limitation. The `stt` extra's CUDA wheels carry `platform_machine == 'x86_64'` on Linux, so an
aarch64 venv does not download them.

## 3. The plan (`PlanSTT`) on Linux

38.8's `PlanSTT` sizes the workers from the GPU's memory, else from the RAM and cores. On Linux the
inputs come from §1 (and `/proc/meminfo`, `/proc/cpuinfo` / `runtime.NumCPU`). The tiers are 38.8's.
Linux adds nothing to them, except:

- `CUDA_VISIBLE_DEVICES` per worker when there are several GPUs (round robin), so two workers do not
  share one card while another sits idle.
- A CPU-only plan sets the second listen off under `[transcription] refine = "auto"` (38.9: a 2B
  model per stretch on the CPU is several times slower than the first listen); `on` keeps it.

## 4. The venv on Linux

- The constraints export already pins every platform's wheels with hashes. The `stt` extra's
  `linux` markers (38.8) add ctranslate2 / faster-whisper, plus the CUDA wheels on x86_64.
  `venv_bootstrap --extras stt,search` on Linux checks exactly those.
- torch from PyPI (the `search` extra and 38.9's second listen) brings its CUDA 13 libraries to
  every Linux x86_64 venv, NVIDIA or not (~3 GB). This is not changed here. It is listed under the
  epic's open decisions (a CPU-only torch variant).

## 5. Settings

No new keys beyond 38.8's (`[transcription] device` etc.). They apply on Linux unchanged, with the
same help text. The fingerprint rule of 38.8 (the setting's model name, not the format a backend
loads) is what keeps a Linux worker, a Windows PC and the Mac home from redoing each other's
transcripts.

## Tests

- Unit (Go): the NVIDIA detection from fake `/proc/driver/nvidia` trees and `nvidia-smi` output
  (one GPU, two, an old driver, no `/dev/nvidiactl`), `LD_LIBRARY_PATH` from a fake venv, and the
  plan on Linux per tier.
- Unit (Python): `cuda_libs.py` against a fake `site-packages/nvidia` tree.
- CI on ubuntu-22.04 (x86_64) and ubuntu-22.04-arm (aarch64): a venv from the release wheel with
  `stt,search`, then the transcribe stage on CPU with a small CTranslate2 model (the tiny Whisper
  stand-in 38.8's tests use) on a short Arabic clip.
- On a real NVIDIA Linux machine (recorded in the PR, when one is available): turbo on a lecture,
  its real-time factor and VRAM per worker, and the second listen on.
