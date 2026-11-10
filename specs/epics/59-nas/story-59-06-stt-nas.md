# Story 59.06 — Speech to text on NAS CPUs: arm64, the model by tier, a measured speed

> Epic 59 · Phase 2 · Depends on: 59.01, 59.04 · Needed by: 59.09, 59.10, 59.11

## Why

Transcripts are what make Maktaba more than a player. On Linux, speech to text is faster-whisper
on CTranslate2 with `large-v3-turbo` (about 1.6 GB), on CUDA or CPU int8. Two things go wrong on
a NAS:
- **No engine on arm64:** the `stt` extra installs ctranslate2, faster-whisper and torch only on
  Linux x86_64 (`pipeline/pyproject.toml`), so an arm64 NAS has no speech engine.
- **Too heavy on x86_64 NAS CPUs:** on a J4125 (no AVX) or a 4-core Ryzen, `large-v3-turbo` on
  the CPU would take many hours per hour of audio, and its memory would crowd out the NAS's own
  services.

No published benchmark gives NAS numbers (research.md §6), so Maktaba must measure on the box.

## Description

**An engine on arm64.**
- The `stt` extra adds ctranslate2 (manylinux aarch64 wheels on PyPI) and faster-whisper on
  `platform_machine == 'aarch64'`.
- Torch, which only the second listen uses, stays x86_64 + CUDA. On arm64 the second listen is
  unavailable, and Settings says so.
- The `runtime.ml` pack gains an aarch64 build, published by release like the x86_64 one.
- CTranslate2 runs `int8` on arm64 through its ARM backend. The 59.15 `arm64` cell transcribes a
  real clip, so "it installs" and "it works" are both checked.

**The model by tier** (59.04):

| Tier | Default when speech is on | Offered |
|---|---|---|
| Light | `base` (off until turned on) | `tiny`, `base` |
| Standard | `small` int8; `base` without AVX2 | `tiny` … `medium` |
| Strong | `large-v3-turbo` (CUDA, or an AVX2 CPU) | all |

- **A CUDA GPU** raises the default to `large-v3-turbo` on any tier whose memory limit holds the
  worker (the model plus 300 MB), since the work runs on the GPU. A render device for video
  does not change the speech model.

- Models download from the existing model source: Hugging Face, the NAS copy, or the home's
  cache.
- Each is stored once under the data dir's models folder. Switching models keeps transcripts
  already made; only new jobs use the new model.
- Arabic accuracy drops with smaller models. Settings shows each model's size and a plain note:
  "Smaller models are faster but make more mistakes, especially in Arabic."

**A measured speed.**
- When speech is turned on, and again after a model change, Maktaba transcribes a bundled
  30-second clip (one Arabic, one English; public domain) with the chosen model and records the
  real-time factor, as audio seconds per wall second.
- Settings shows "This NAS transcribes about {x}× faster than real time with {model}" (or
  "{x}× slower").
- With the backlog's total duration, it estimates how long the backlog will take. If that is
  over 30 days, it suggests the next smaller model and shows both estimates. It also mentions that
  a computer with a GPU can join as a node (59.13).

**Running politely.**
- Transcription jobs are heavy (59.02). On Light and Standard, backlogs wait for quiet hours.
- A single new file is transcribed when it arrives, at `nice 10`, with one CTranslate2 thread
  per two cores (at least one).
- The worker's memory is the model's plus 300 MB. The pipeline refuses to start a second speech
  worker when the memory limit cannot hold two.

## Acceptance criteria

- **Given** the arm64 image with speech on, **then** the 30-second Arabic and English clips are
  transcribed, and each result's word error rate against its reference is recorded in the CI
  report. The first runs set a baseline per model and architecture; from then on, a rise of more
  than 5 points over the baseline fails the run. `base` is weak on Arabic, so no fixed threshold
  is set before the baseline exists.
- **Given** a Standard tier on a CPU without AVX2, **then** the default model is `base`, and
  Settings says why.
- **Given** a measured factor and a backlog estimated over 30 days, **then** Settings suggests the
  smaller model with both estimates.
- **Given** a 6 GB limit, **then** only one speech worker starts, and the log says why.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | CI `arm64` cell (59.15) | arm64 image, `base` | transcribe the Arabic and English clips | text produced; WER recorded; no rise over 5 points from the stored baseline |
| T02 | pipeline unit | tier × avx2 table | default model | matches the table |
| T03 | pipeline unit | factor 0.5×, backlog 900 h | estimate | over 30 days → suggests next smaller |
| T04 | pipeline unit | limit 6 GB, `small` | plan workers | one |
| T05 | CI `mem-6g-stt` | Standard | transcribe | done; container not OOM-killed |
| T06 | packaging check | aarch64 `runtime.ml` | install | ctranslate2 imports; no torch |
| T07 | web unit | Settings speech panel | render | measured speed, model note, both languages |

## Strings

| Key | English | Arabic |
|---|---|---|
| `stt.speed.faster` | This NAS transcribes faster than real time with {model} (speed: {x}×). | يفرّغ خادم التخزين الشبكي هذا الكلام أسرع من الزمن الفعلي باستخدام {model} (السرعة: {x}×). |
| `stt.speed.slower` | This NAS transcribes slower than real time with {model} (speed: {x}×). | يفرّغ خادم التخزين الشبكي هذا الكلام أبطأ من الزمن الفعلي باستخدام {model} (السرعة: {x}×). |
| `stt.speed.busy` | Measured while the NAS was busy, so the real speed may be higher. | قيست السرعة وخادم التخزين الشبكي مشغول، لذا قد تكون السرعة الفعلية أعلى. |
| `stt.backlog.estimate` | Audio waiting: {hours} h. Estimated time: {days} d. | الصوت المنتظر (بالساعات): {hours}. الوقت المقدّر (بالأيام): {days}. |
| `stt.backlog.suggest` | With {model}, estimated time: {days} d. A computer with a GPU can also join this home and do the work. | باستخدام {model}، الوقت المقدّر (بالأيام): {days}. ويمكن أيضًا لحاسوب مزوّد بمعالج رسوميات أن ينضم إلى هذا الجهاز الرئيسي ويقوم بالعمل. |
| `stt.model.offline` | The model couldn't be downloaded. Copy it from another Maktaba computer (NAS copy), or put it in the models folder of Maktaba's data folder. | تعذّر تنزيل النموذج. انسخه من حاسوب Maktaba آخر (نسخة خادم التخزين)، أو ضعه في مجلد النماذج داخل مجلد بيانات Maktaba. |
| `stt.model.note` | Smaller models are faster but make more mistakes, especially in Arabic. | النماذج الأصغر أسرع لكنها تخطئ أكثر، لا سيما في العربية. |
| `stt.no_avx2` | This processor lacks AVX2, so a smaller model was chosen. | يفتقر هذا المعالج إلى AVX2، لذا اختير نموذج أصغر. |
| `stt.second_listen.unavailable_arm` | The second listen is not available on ARM processors. | الاستماع الثاني غير متاح على معالجات ARM. |

## Edge cases

- **No internet for the model download:** the NAS copy source (setup's existing step) or a model
  folder the household places in the data dir. `stt.model.offline` names both.
- **A CPU with AVX but not AVX2** (older Atoms): treated like no-AVX2.
- **The measurement runs while other work is busy:** it is repeated once if the CPU was over 80%
  busy, and shown with `stt.speed.busy` if it still was.

## Build plan

| File | Change |
|---|---|
| `pipeline/pyproject.toml` | `stt` extra on aarch64 (ctranslate2, faster-whisper) |
| `pipeline/src/maktaba_pipeline/stt/engine.py` | model by tier, threads, nice |
| `pipeline/src/maktaba_pipeline/stt/benchmark.py` (new) | the measured factor, estimates |
| `pipeline/src/maktaba_pipeline/stt/assets/` (new) | the two 30-second clips with licence notes |
| `.github/workflows/release.yml` | aarch64 `runtime.ml` |
| `web/src/i18n/{en,ar}.json`, `web/` | strings; speech panel |
