---
schema: 1
kind: pr
pr: 1112
title: "Epic 59 story 59.06: speech to text on NAS CPUs: arm64, the model by tier, a measured speed"
date: 2026-11-10
session: 291cfb08-ec1f-48d4-be42-5d32c32e13f1
agent: claude-code
---

## What happened
- The `stt` extra installs CTranslate2 and faster-whisper on Linux aarch64; torch and
  transformers (the second listen) stay x86_64 + CUDA. `build-release.sh` writes
  `constraints-stt.txt`; the aarch64 payload's wheelhouse carries its wheels (about 90 MB, no
  torch), and `check.sh` installs them `--no-index` and imports CTranslate2.
- The tier picks the speech model (`hostinfo.DefaultsFor`, `SpeechOf` in the supervisor):
  Light off until turned on, then `base` in the quiet hours only (`MAKTABA_QUIET_SPEECH`);
  Standard `small`, `base` without AVX2 or on ARM; Strong `large-v3-turbo` with AVX2 or CUDA;
  a CUDA GPU of 4 GB or more raises any tier. `[transcription] speech` and a size name in
  `model` override it. Light and Standard run transcription at nice 10 with a thread per two
  cores; no more speech workers than a quarter of the memory limit holds, and the log says why.
- Catalog packs `model.whisper-tiny`, `-base`, `-small`, pinned; `engine.SIZES` and `CT2_PINS`
  match them both ways.
- `stt/benchmark.py` measures the real-time factor and word error rate on two bundled clips;
  the server runs it when speech is on and its model unmeasured, and on an admin's Measure
  again; `GET/PUT /api/server/speech`, `POST /api/server/speech/measure` (admins, audited)
  add the backlog's estimate and the smaller model's past 30 days. Settings → This machine
  shows it in English and Arabic.
- NAS cells `arm64-stt` (Light on arm64: the engine offline from the wheelhouse, both clips
  with `base`) and `mem-6g-stt` (Standard, `small`, no OOM) both pass; merge 163d17ce9.

## What we learned
- eSpeak NG's Arabic is unintelligible to Whisper even fully vowelled (WER above 1.0 with
  `small`). The Arabic clip is LibriVox's public-domain *Kalila wa Dimna*, its reference
  checked against four Whisper models; the English one stays eSpeak.
- faster-whisper 1.2.1's own `decode_audio` breaks on PyAV 19 (`metadata_errors`); the measure
  decodes with ffmpeg as the workers do.
- A word-error baseline measured on a developer's Mac does not hold on CI: CTranslate2's ARM
  kernels differ by CPU (`base` Arabic 0.46 on an M-series in Docker, 0.52 on
  `ubuntu-22.04-arm`). The baseline comes from the cells' own first runs.
- The core venv has no NumPy: an engine install filtered to aarch64-marked pins dropped it.
- A measure that crashed on an import wrote no answer, so the server retried it every 10
  minutes; the measure now checks for the engine before importing it, and the server backs off
  6 hours from a failure with no answer.
- The NAS cells' runs took two pushes to go green because the PR's first CI was only read
  through a summary cut at 80 lines: the failed checks were past the cut.

## Keep doing
- A watcher prints every check that is not green, never a truncated list of all.
- Run the real engine in a `linux/arm64` container on the Mac before CI (it found the
  PyAV break and the Arabic clip problem in minutes).

## Stop doing
- Seeding a CI baseline from a local machine.

## Open
- `small/x86_64` and `base/aarch64` are in `tests/nas/stt-baseline.json`; other models and
  architectures are recorded only until their first run's numbers are added.
- No real NAS has run the measure yet: the device checklists (59.14) should record a
  DS223/DS423 and a J4125's speed.
